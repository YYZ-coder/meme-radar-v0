# M1 验收 agent（开发全部合并之后）

> 开发任务见[16 M1并行任务拆分](16-m1-agent-tasks.md)。本文件给**最后一个 agent**：它不写功能，只核对“做了的是否符合文档、没做的是否被发现”。它的核对代码必须独立于开发实现，不能调用 `internal/pricing`、`internal/parse` 等业务包来当期望值。

## 1. 开始条件

- [16](16-m1-agent-tasks.md) 的 P0、A、B0、B1、B2、C1、C2、D、I 都已合并到同一分支（或 `main`）。
- `docker compose up -d postgres` 可用，空库能跑完迁移。
- 公共 RPC 可访问；并发不超过每链 2。
- 前端用 `RADAR_API_ORIGIN=http://localhost:8080` 构建（否则页面连的是内置 mock，不能算验收）。
- 环境变量 `RADAR_ADMIN_TOKEN` 已设置（D106），且不写入仓库。

任一条件不满足就停，在报告里写缺什么，不改代码绕过。

## 2. 工作流程

1. 干净检出，执行根目录 `make check`。失败则验收不通过，把失败命令和最后 50 行记入报告，退回对应包的 agent。
2. 按下面的追溯表逐条核对。每条只能是：通过（附证据）、失败（附输入、期望、实际）、未测（写原因）。不许留空。
3. 数字类用本文件第 4 节的独立 SQL / 独立脚本重算，与 API 和页面对比。
4. 跑第 5 节的端到端冒烟（短时真实链）。
5. 写验收报告（第 6 节模板），路径 `docs/engineering/m1-acceptance-report.md`。
6. 有失败项时列出负责的卡（P0、A、B0–B2、C1–C2、D、I）和建议退回内容，不自行大改实现。只允许修报告、测试夹具和明显的文档笔误。

## 3. 需求追溯表

| 编号 | 要求 | 证据 |
| --- | --- | --- |
| D2 | 三链协议都有解码测试：Pump、PumpSwap、LaunchLab、Jupiter；Clanker、Uniswap v4；Four.meme、Pancake | B1/B2 测试名列表；`decoded.json` 7 笔对账 |
| D22/D66 | 完整区块进 Raw，PG 只存元数据 | 查 `raw_blocks.object_key` 能读到 zstd 对象 |
| D53 | 金额按 Token 汇总、美元展示 | 多池 Token 只有一行，额为合计 |
| D54 | 报价资产每分钟成交量加权；>5 分钟 stale；>30 分钟不输出 | C 包对账表：SOL/ETH/BNB 与 Demo 03 |
| D55 | 主池切换三情形：不足 20%、满 10 分钟、毕业立即切 | C 包测试 |
| D56 | 交易数/额/净流入跨全部池 | 独立 SQL 与 API 一致 |
| D71 | 未确认先展示，确认后改状态 | `raw_blocks.confirmation` |
| D73 | 历史上界是 A 之前 30 天 | `chain_anchors.history_start_position` |
| D77 | 库内 BYTEA，API 为 base58 / 0x 校验和 | 抽 3 个地址对比 |
| D78 | 事实主键是链上位置 | 抽一笔成交主键与样本一致 |
| D79 | 未跟踪 Token 不写 `balance_changes` | 白名单报价资产无余额行 |
| D81 | 任务按 1,000 位置区间 | `block_jobs` 行数与区间 |
| D84 | 失败交易记手续费、无其他事实 | 构造一笔失败交易 |
| D85 | 事实表无美元列 | `information_schema.columns` 查询 |
| D87 | 重组后旧事实删除并重解析 | A/B 重组测试 |
| D104 | 预算开关能停历史并登记 `budget_paused` | 配置触发后的缺口行 |
| D105 | K 线只有 6 个周期 | API 拒绝其他 interval |
| D106 | 管理接口无 token 为 403，正确 token 为 200 | curl 两条 |
| D107 | 采集范围内新币有持有人；更早的 Token 为未知而非 0 | 页面与 API |
| 资金流入 | `query_id` 翻页不重不漏；过期 410；>2 秒 503；未知显示 `--` | D 包测试 + 页面 |
| 名单 | 导入两次不翻倍；版本切换已打开的分页不变 | D 包测试 |
| 前端 | 表头顺序与 [资金流入](../product/01-inflow.md) 一致；中英文切换保持；桌面和移动可操作；连真实 API 时响应无 `x-radar-mock` 头、页面不显示“演示数据” | `RADAR_API_ORIGIN` 构建后的 Playwright + 截图 |
| 契约 | 每个 M1 端点响应符合 `api/openapi.yaml`；`frontend` 的 `npm run api:check` 通过 | D 卡契约测试输出 |
| D112 trace | 每个响应有 `X-Trace-Id`；同一 trace 在 Grafana 查到访问日志与业务日志 | curl 响应头＋LogQL 截图 |
| D112 panic | 处理器/任务 panic 不致进程退出；`make -C backend lint-go` 通过（无裸 go） | 测试输出 |
| D112 错误码 | 所有错误响应的 code 在 `ErrorCode` 内，日志 code 在目录内 | `TestAPICodesMatchOpenAPI`；`{service="radar"} \| json \| code!=""` 抽查 |
| 安全 | 仓库、日志、数据库无私钥、助记词、admin token | `git grep` 与日志抽查 |

## 4. 独立核对（不调用业务包）

验收 agent 在 `backend/testdata/acceptance/` 写只读 SQL 和脚本，期望值来自文档算例：

| 核对 | 期望来源 | 做法 |
| --- | --- | --- |
| 报价资产价 | [Demo 03](../demos/03-pricing.md)：SOL 105.247、ETH 2,454.07、BNB 729.40 | SQL 对样本成交做成交量加权，与 `quote_asset_prices` 比较，允许最后一位舍入 |
| 市值 | 同 Demo：主池价 ×（总供应 − 黑洞） | 独立计算 vs API `market_cap_usd` |
| 窗口合计 | [Token指标](../metrics/token-metrics.md)、D56 | `SELECT` 汇总 `pool_trades` 关联分钟价，对比 `token_window_current` |
| 资金流入分页 | [Demo 08](../demos/08-inflow-query.md) | 同一 `query_id` 翻完 2,000 内的 ID，集合相等且顺序不变 |
| 解析 | `docs/demos/samples/decoded.json` | 脚本读 JSON 期望，查事实表，逐字段 diff |

差异超过文档允许的舍入（0.1%）即失败，不改期望值去迎合实现。

## 5. 端到端冒烟（短时，不是 24 小时）

24 小时无缺口是 M1 里程碑验收，依赖付费节点（D75），本 agent 不假装跑完。短时冒烟：

1. 空库启动本次构建的 `bin/radar`。
2. 每链只采最近约 30 个已确认区块（并发 ≤2），解析并等到派生水位追上。
3. 若窗口内没有“发行→迁移→DEX”全链路，就改用 `docs/demos/samples` 回放补齐，并在报告里写明哪一段是回放。
4. 打开资金流入与其中一个 Token 详情，桌面和移动各截一张图。
5. 用 admin token 导入 3 个地址，确认列表聪明钱计数与 SQL 一致，然后用错误 token 确认 403。
6. 停进程，确认无残留监听。

## 6. 报告模板

写入 `docs/engineering/m1-acceptance-report.md`：

```markdown
# M1 验收报告

- 日期：
- 代码版本（git rev-parse HEAD）：
- 环境：Go / Node / Postgres 版本，机器

## 结论
通过 / 不通过。不通过时列出必须退回的包。

## make check
命令与最后结果。

## 追溯表
| 编号 | 结果 | 证据或失败详情 |
（第 3 节每一行）

## 独立核对
| 项 | 期望 | 实际 | 差异 |

## 端到端
采了哪些块、哪些是回放、截图路径、403 结果。

## 未测与原因
- 24 小时运行：未测，原因…
- …

## 退回
| 包 | 问题 | 建议 |
```

## 7. 通过标准

- 追溯表没有“失败”。
- “未测”只允许出现在预先声明依赖外部条件的项：24 小时运行、付费节点、M2 以后的快照供应商。
- 独立核对全部在舍入范围内。
- 报告已提交，且没有把未跑的检查写成通过。
