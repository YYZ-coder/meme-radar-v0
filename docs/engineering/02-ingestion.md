# 区块采集与断点恢复

> 决策D21、D22、D47、D66、D71、D73、D81、D83、D87。表结构见[数据模型§3](06-data-model.md)。
>
> 演示（真实链上样本＋异常兜底）：[Demo 01 区块采集](../demos/01-ingestion.md)。

## 1. 采集单位

| 链 | 一个Raw区块包含 |
| --- | --- |
| Solana | slot、blockHeight（与slot分开存，可缺失）、blockhash、parentSlot、blockTime、全部交易及meta（指令、内部指令、日志、前后余额、费用、错误） |
| Base / BSC | blockNumber、blockHash、parentHash、timestamp、全部完整交易对象、每笔交易的receipt（全部日志、status、gas与费用字段） |

EVM需另取receipt组成完整数据包；任一receipt缺失不算完整。Solana被跳过的slot（没有出块）只计数，不写`raw_blocks`；请求失败不能当作跳过。

## 2. 两条采集任务

| 任务 | 范围 | 采集方向 |
| --- | --- | --- |
| forward | 启动锚点A及之后，追随链头 | 旧→新 |
| history | A之前30天（D73；按链上时间定位下界L并记录实际位置） | 新→旧 |

- A在首次启动时为每链选定并持久保存到`chain_anchors`（位置、hash、链上时间、L），重启不重选（D74）。
- 两任务共用采集器、Raw存储和区块去重，各自记录进度；历史任务有独立资源配额，不挤占前向。
- 30天是采集目标，不是保存期限（D24）。
- 采集方向与解析方向无关：解析始终按旧→新（D49），历史解析从L起等待所需Raw，见[解析§3](03-parsing.md)。

## 3. 调度：区间任务（D81）

每1,000个位置建一条`block_jobs`（Solana 30天历史约6,500条，而不是650万条）。区间内每个块的结果看`raw_blocks`，重试耗尽的块记`collection_gaps`。

```mermaid
flowchart TD
    T["定时器每2秒 / WS新块通知（只唤醒）"] --> H["查询链头H：Solana getSlot(confirmed)；EVM eth_blockNumber"]
    H --> F{"forward最后一个区间的range_end ≤ H？"}
    F -->|是| NF["按1,000个位置补建区间任务（ON CONFLICT DO NOTHING）"]
    F -->|否| HS
    NF --> HS{"history未到L且历史配额有余？"}
    HS -->|是| NH["向更旧方向补建区间任务，最多领先N个"]
    HS -->|否| WAIT["等待下一次触发"]
    NH --> WAIT
```

领取与执行：

```mermaid
flowchart TD
    A["worker：FOR UPDATE SKIP LOCKED领取pending或租约过期的任务"] --> B["status=running，lease_until=now+60秒，worker_id；立即提交"]
    B --> C{"Solana？"}
    C -->|是| D["getBlocks(range)取得实际出块slot；未列出的slot计入skipped_count"]
    C -->|否| E["区间内每个块号都需要抓取"]
    D --> G["逐块抓取（有界并发）→ 校验 → 写对象 → 写raw_blocks，done_count+1"]
    E --> G
    G --> R{"某块重试耗尽？"}
    R -->|是| GP["写collection_gaps，gap_count+1"]
    R -->|否| N
    GP --> N{"done+skipped+gap = 区间大小？"}
    N -->|否| L["续租（每20秒）后继续"]
    L --> G
    N -->|是| Z["status=done；推进连续进度"]
```

- forward追链头时，链头所在区间在到达`range_end`前保持running并按租约续期。
- Solana `getBlocks`只对≤确认链头的范围调用，避免把“尚未出块”误计为跳过；单块返回`-32007`/`-32009`时同样计入`skipped_count`。
- 每个块的写入顺序：先写zstd对象（键`{chain}/{yyyy-mm-dd}/{position}-{hash}.zst`），再在一个事务里写`raw_blocks`并更新任务计数（D66）。对象已写、元数据未提交时，重试覆盖同一键，幂等。

任务状态：

```mermaid
stateDiagram-v2
    [*] --> pending: 补建区间
    pending --> running: 领取（租约60秒）
    running --> running: 续租
    running --> pending: 租约过期（worker崩溃）/ 可重试错误，next_attempt_at退避
    running --> done: done+skipped+gap = 区间大小
    running --> failed: 配置/权限错误（不重试）
    failed --> pending: 修复配置后人工恢复
    done --> [*]
```

## 4. 单块抓取的异常分类

实测例子见[Demo 01 §3](../demos/01-ingestion.md)。

```mermaid
flowchart TD
    A["请求RPC"] --> B{"结果"}
    B -->|成功| V{"完整性校验"}
    B -->|超时 / 429 / 5xx / Solana -32004| R1["可重试：退避1s→60s加抖动；429同时降并发"]
    B -->|401 / 403 / Solana -32015| F1["配置/权限错误：任务failed，该链或供应商暂停并告警"]
    B -->|Solana -32007 / -32009| K["slot被跳过：skipped_count+1"]
    B -->|Solana -32001 / EVM历史不可用| G1["collection_gaps：history_unavailable；换有历史能力的供应商"]
    V -->|回执数≠交易数 / 回执blockHash不符| R3["丢弃结果重试；3次后换供应商"]
    V -->|parentHash与已存前一块不符| RG["进入重组处理（§5）"]
    V -->|通过| S["写对象 → 写raw_blocks"]
    S -->|对象存储失败| R4["重试；持续失败则暂停领取（背压）"]
    R1 --> L{"该块超过上限（默认8次）？"}
    L -->|否| A
    L -->|是| G2["collection_gaps：retry_exhausted；后台低频重试"]
```

| 类别 | 例子 | 处理 | `last_error_class` |
| --- | --- | --- | --- |
| 可重试 | 超时、HTTP 429/5xx、Solana `-32004` | 指数退避，429降并发；8次后记缺口 | `retryable` |
| 配置/权限 | Solana `-32015`、HTTP 401/403 | 不重试；暂停并告警，修复后从断点继续 | `config` |
| 数据不存在 | Solana `-32007`/`-32009` | 计入跳过数 | `not_found` |
| 历史不可用 | Solana `-32001`、EVM历史缺失 | 记缺口，换供应商 | `history_unavailable` |
| 结果不完整 | 回执缺失或blockHash不符 | 丢弃重试，3次后换供应商 | `incomplete` |

重试计数：单块的重试次数记在worker内存里（区间行的`attempts`只记整个区间被领取的次数）；worker崩溃后由新worker从0重新计数，最多多试一轮，不影响正确性。

缺口的后续：

- 缺口块不阻塞采集区间完成，也不阻塞连续进度；后台按低频重试，补齐后写`collection_gaps.resolved_at`。
- 解析遇到未解决的缺口块时不无限等待：跳过该块并记录覆盖缺口；依赖缺口区间的计算（如标签的Launch窗口）等待补齐；补齐后该块按“迟到事实”处理，派生结果从该位置重算。
- 管理后台展示未解决缺口数与最老缺口；M1验收要求24小时内无未解释缺口。

## 5. 确认与重组

- 确认等级：Solana `confirmed`；Base/BSC延后12个区块（D71）。
- EVM未确认块照常抓取、解析，事实直接写正式表（D87），`raw_blocks.confirmation=pending`；满12块后改为`confirmed`。连续进度只按已确认位置推进。
- 区块身份为(chain, position, block_hash)；同一位置只有一个`branch=canonical`。

```mermaid
sequenceDiagram
    participant W as ingest worker
    participant N as RPC
    participant D as PostgreSQL
    participant P as parse
    participant C as compute
    W->>N: 抓取位置p
    N-->>W: 区块p（parentHash ≠ 已存p-1的hash）
    loop 向前回溯，直到hash一致（最多64块）
        W->>N: 抓取位置k（新分支）
        W->>D: 比对raw_blocks中位置k的canonical hash
    end
    alt 64块内仍不一致
        W->>D: 该链采集暂停，告警，等待人工处理
    else 找到共同祖先c
    Note over W: 得到共同祖先c
    W->>D: 事务：c之后的旧块branch=orphaned；写reorg_events(c, old_tip, new_tip)
    W->>D: 同一事务：删除各事实表block_num > c的行；parse_progress回退到c；写recompute_jobs(from=c)
    W->>D: 同一事务：为被撤销的已发布事件写event.reverted
    W->>N: 按正常流程抓取新分支c+1…新链头
    P->>D: 从c+1重新解析
    C->>D: 从c起重算派生结果
    end
```

- 旧分支Raw保留（对象不删），便于排查。
- 重组深度超过确认深度（已confirmed的块被替换）属于异常：照常处理，同时告警。
- 回溯最多64块；64块内找不到共同祖先说明数据源异常，暂停该链并告警，人工处理。

## 6. 进度表

`collection_progress`按(chain, task_kind, run_id)一行：固定范围、连续完成位置与该位置hash、generation（防止过期worker覆盖）、状态、最近错误。

- forward的连续完成位置：从A起，所有区间都done且最高位置已确认的最大位置。
- history的连续完成位置：从A-1向L方向连续done的最小位置。
- 示例：区间[100,101,102,103]中102重试中、103已完成，连续位置停在101；102成功或转入缺口后推进到103。

## 7. 验收要点

- 关闭WS后RPC仍能补齐；重复通知不重复落盘。
- 102失败、103成功时连续位置停在101，102补齐（或转入缺口）后推进到103。
- 写对象、写元数据、更新任务三步之间任意时刻崩溃，重启后状态一致、不重复。
- worker崩溃后租约过期，任务被其他worker重新领取。
- Solana区间的done+skipped+gap与区间大小一致；“尚未出块”不被计为跳过。
- 同位置换hash按重组处理：旧分支标orphaned，c之后的事实删除并重新解析，被撤销事件推送`event.reverted`。
- 历史任务失败不影响前向进度。
