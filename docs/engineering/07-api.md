# REST API

> **正式契约是 `api/openapi.yaml`（v1.0.0-m1，D109）**，字段、枚举、必填以它为准；本文件只说明端点范围与通用规则。前端从契约生成类型（`npm run api:types`），`npm run api:check` 防止漂移。
> 行为参考实现：`frontend/src/mock/`（D110）。演示：[Demo 08 资金流入列表](../demos/08-inflow-query.md)。

## 1. 通用规则

| 项 | 规则 |
| --- | --- |
| 前缀 | `/api/v1`；`/healthz` 不带前缀 |
| 金额 | 十进制字符串；列表行与详情带 `currency`（`USD` 或 `SOL/ETH/BNB`）；未知为 `null` |
| 计价切换 | `currency=usd|native`，默认 `usd`（D53） |
| 地址 | Solana base58；EVM EIP-55 校验和 |
| 时间 | RFC 3339 UTC；K 线时间戳为 Unix 秒 |
| 分页 | 资金流入：`query_id`＋`offset`（§3）。其他列表：`cursor`＋`limit`（默认 50、最大 100），响应 `next_cursor`，到底为 `null` |
| 名单版本 | 资金流入与概览带 `list_version`（不透明字符串），翻页沿用首次查询的版本（D30） |
| 错误 | `{ "error": { "code", "message", "field", "trace_id" } }`；`code` 取值见契约 `ErrorCode` 与[错误码目录](18-observability.md)；400 参数（`field` 指出字段）、403、404、409、410、413、429、500、503 |
| trace | 每个响应带 `X-Trace-Id`；请求可带合法的 `X-Trace-Id`/`traceparent` 沿用（[可观测性§3](18-observability.md)） |
| 鉴权 | 公开数据匿名；管理接口 `Authorization: Bearer <RADAR_ADMIN_TOKEN>`（D106），缺失或错误 403；M4 起用户数据走会话 Cookie |
| 缓存 | 公开列表可短缓存（≤5 秒）；用户与管理数据 `Cache-Control: no-store` |

## 2. M1 端点

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/healthz` | 进程与数据库健康 |
| GET | `/market/natives` | SOL/ETH/BNB 美元价（底部栏，D54 stale 规则） |
| GET | `/search?q=` | Token 与钱包候选；同名不合并 |
| GET | `/smart-money/overview` | 概览卡片：聪明钱净流入及环比、活跃聪明钱、收录/新增 Token、买入占比、16 桶柱图 |
| GET | `/smart-money/inflow/facets` | DEX、发射平台候选及窗口成交额 |
| GET | `/smart-money/inflow` | 资金流入列表（§3） |
| POST | `/tokens/batch` | 按 Token（≤100）取当前列表数值，供可见行 10 秒刷新与观察列表 |
| GET | `/tokens/{chain}/{address}` | 头部、行情、窗口摘要（5m/1h/4h/24h，全部钱包口径）、开发者、持有人覆盖、同名代币 |
| GET | `/tokens/{chain}/{address}/candles` | K 线：只接受 1s/30s/1m/5m/1h/1d（D105），`metric=price|market_cap`，含主池切换点 |
| GET | `/tokens/{chain}/{address}/activities` | 成交与加减池；`label=all|smart_money`（其余标签 M3），带各标签数量；未归因成交只在 all |
| GET | `/tokens/{chain}/{address}/holders` | 持有者与覆盖范围（D107：full / partial） |
| GET | `/tokens/{chain}/{address}/pools` | 流动性池（含毕业曲线、主池标记） |
| GET | `/tokens/{chain}/{address}/liquidity-events` | 流动性变化 |
| GET | `/tokens/{chain}/{address}/smart-money` | 该 Token 的聪明钱持仓与买卖额 |
| POST | `/admin/smart-money/imports` | 上传 CSV/JSON（≤10,000 行、≤5MB），只校验，返回逐行报告；疑似私钥/助记词整文件拒收 |
| POST | `/admin/smart-money/imports/{id}/apply` | 确认，生成新名单版本（D92）；重复确认 409 |
| GET | `/admin/smart-money/members` | 名单查询（链、来源、标签、地址/名称） |
| DELETE | `/admin/smart-money/members/{chain}/{address}` | 去掉导入来源，生成新版本 |
| GET / POST | `/admin/smart-money/exclusions` | 排除名单查询 / 排除地址（生成新版本） |
| DELETE | `/admin/smart-money/exclusions/{chain}/{address}` | 恢复 |
| GET | `/admin/health` | 每链采集进度、积压、缺口原因、最近错误；解析进度与隔离；失败任务；预算状态（D104） |
| POST | `/admin/parse-failures/{id}/retry` | 重试失败任务（202） |

M2 起的牛人榜、钱包分析、信号、代币信息卡，M4 的账号/关注/提醒/WS 票据，M5 的交易端点，范围见[里程碑](11-milestones.md)；进入对应里程碑时先加入 `api/openapi.yaml` 再实现。实现说明见[登录与交易](15-auth-trading.md)、[实时推送](08-realtime.md)。

## 3. 资金流入分页（技术方案 §3.4）

1. 首次请求（不带 `query_id`）按筛选与排序取前 2,000 个 Token ID，存进程内 LRU（TTL 10 分钟），返回 `query_id` 与第一批。
2. 翻页只传 `query_id`＋`offset`，其余参数忽略；数值为当前值，顺序不变；`next_offset` 到底为 `null`。
3. 排序值相同按 Token ID 升序；排序列为 `null` 时无论升降序都排最后；设置了某字段的 min/max 时该字段为 `null` 的 Token 被排除（Demo 08 §3）。
4. 兜底：`query_id` 过期或不在本实例 → 410，前端回到顶部并用 URL 条件重查；查询 >2 秒 → 503＋`Retry-After`，前端保留已加载行；单 IP 每分钟 >60 次新查询 → 429（翻页与 batch 不计）；匹配超过 2,000 → `truncated=true`。

响应示例（节选，字段以契约为准）：

```json
{
  "query_id": "q_8f3a",
  "list_version": "42",
  "as_of": "2026-10-16T13:45:00Z",
  "window": "1h",
  "total": 3,
  "truncated": false,
  "offset": 0,
  "next_offset": null,
  "items": [
    {
      "token": { "chain": "solana", "address": "GX1JGwFmrzgkv6MBjwZZdZt3qBcWi1d5ZuAUS3KFpump", "symbol": "GX1J", "name": "…" },
      "currency": "USD",
      "price": "0.0000322262",
      "market_cap": "30786.83",
      "liquidity": "14582.94",
      "sm_holders": 7,
      "sm_trades": 23,
      "net_inflow": "2052.62"
    }
  ]
}
```

`liquidity: null` 表示未知，前端显示“--”。

## 4. 字段来源对照与缺口（2026-10-19 核对）

前端 ↔ 契约已由生成类型、zod `satisfies` 与 mock 契约测试锁定。下表核对**契约 ↔ 迁移 `00001`–`00008`**：能否从库里产出每个响应字段。缺口由对应卡在自己的迁移号段补齐（[16](16-m1-agent-tasks.md)），属新增表/列，不改已有迁移。

| 契约字段 | 来源 | 状态 |
| --- | --- | --- |
| `InflowRow` 大部分字段、`sparkline` | `token_window_current` | ✓（1m/5m 的 20 点走势取 `candles_hf`，分钟统计粒度不够） |
| `InflowRow` 原生币计价 | USD 值 ÷ `quote_asset_prices` 最新价，在 API 换算 | ✓ |
| `Facet.volume_usd`（每个 DEX 的窗口成交额） | 无按池/DEX 的窗口统计 | **缺 G1** |
| `Pool.liquidity_usd / reserve_*`；D55 主池所需每池 1 小时平均流动性 | 无池状态表 | **缺 G2** |
| `TokenDetail.ath_market_cap / ath_since` | 无 | **缺 G3** |
| `Overview.active_wallets`（跨 Token 去重聪明钱数） | `sm_minute_stats.wallet_count` 按 Token 计，跨 Token 相加会重复 | **缺 G4** |
| `Overview.inflow_bars`（1m/5m 窗口 16 桶） | 分钟粒度不够 | **缺 G5**：1m/5m 直接查 `economic_actions`，其余用分钟统计 |
| `Overview` 其余、`WindowSummary`（含 `traders`） | `sm_minute_stats`、`token_minute_stats.trader_sketch` | ✓ |
| `Member.tags / added_at` | `smart_money_members` 无此两列；KOL 标签按 D93 应进 `wallet_labels` | **缺 G6** |
| `ParseFailure.id` | 复合主键 | ✓ 编码为 `chain:module:block:tx` |
| `ChainHealth.head`、`Budget.spent_usd` | 链头只在内存；无请求量/费用表 | **缺 G7** |
| `ParserHealth.unknown_discriminators` | 只有日志计数 | **缺 G8** |
| `ImportReport`（确认前的校验结果） | 无表；进程内保存会在重启/多实例时丢失 | **缺 G9** |
| `Activity.dex`、`Pool.dex`、`quote_symbol` | `protocol` 文本、`quote_asset` 字节 → 需代码常量表 | **缺 G10**：放 `internal/domain` |
| `ActivitiesResponse.label_counts` | 统计范围未定义 | **定义**：该 Token 近 24 小时、与当前 `kind` 一致的条数 |
| `Holder.native_balance` | 按需查 RPC（D79），未查到为 `null` | ✓（缓存 5 分钟，单页最多查 50 个） |
| 其余（Token 身份、K 线、活动、持有者、覆盖范围、名单、排除、隔离、采集进度） | 已有表 | ✓；枚举（side、attribution、coverage、pool status）与契约一致 |

| 缺口 | 负责卡 | 建议 |
| --- | --- | --- |
| G1、G2、G3、G4 | C1 | `pool_minute_stats`(chain, pool, minute, volume_usd, trade_count, liquidity_usd)；`pool_state`(chain, pool, reserve_token, reserve_quote, liquidity_usd, updated_block)；`token_window_current` 或 `tokens` 加 `ath_market_cap_usd`、`ath_at`；`sm_chain_minute_stats`(list_version, chain, minute, buy_usd, sell_usd, wallet_sketch) |
| G5、G9 | D | 1m/5m 概览柱图直接聚合 `economic_actions`；`smart_money_imports`(id, file_name, rows JSONB, totals, expires_at, applied_at) |
| G6 | D | `smart_money_members` 加 `added_at`；导入的 `tags` 写 `wallet_labels`（kind=kol 等），读名单时合并返回 |
| G7 | A | `chain_heads`(chain, head, observed_at)；`rpc_usage`(chain, provider, day, requests, est_cost_usd) |
| G8 | B0 | `parse_progress` 加 `unknown_discriminators BIGINT` 或单独计数表 |
| G10 | P0 | `domain.QuoteAssets`（地址→符号、小数位）、`domain.ProtocolNames`（protocol→DEX 显示名） |
