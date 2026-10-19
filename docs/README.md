# 文档导航

**状态（2026-10-19）**：设计已定稿；迁移 `00001`–`00008` 已落地；M1 API 契约冻结；前端 M1 页面完成（连内置 mock）；后端业务按 [16](engineering/16-m1-agent-tasks.md) 待开发。v0.x 的 `docs/design/` 已废弃，勿再引用。

## 怎么读

| 顺序 | 文档 | 用途 |
| --- | --- | --- |
| 1 | [DECISIONS](DECISIONS.md) | 范围与决策唯一来源（引用编号如 D55） |
| 2 | [技术方案](TECHNICAL_PLAN.md) | 目标、进程模型、容量、选型、异常总表 |
| 3 | [数据模型](engineering/06-data-model.md) | 表结构说明；权威 DDL 在 `backend/migrations/` |
| 4 | `product/` → `metrics/` → 其余 `engineering/` | 页面 → 口径 → 实现 |
| 5 | [demos/](demos/README.md) | 真实样本 + 流程 + 异常兜底 + 验收 |

三层分工：**产品**（做成什么样）→ **指标**（每个数字怎么算）→ **工程**（怎么做出来）。决策论证只写在 DECISIONS，其他文档引用编号。

## 当前进度

| 项 | 状态 |
| --- | --- |
| M0 公共 RPC 实测、30 天历史取舍（D73） | 锚点与取舍已定；每链 1,000 块实测与 30 天前历史可得性**未做**（[里程碑 M0](engineering/11-milestones.md)） |
| 设计文档（product / metrics / engineering / demos） | 已定稿 |
| 数据库迁移 `00001`–`00008` | 已落地并本地校验 |
| M1 API 契约 `api/openapi.yaml` | v1.0.0-m1 已冻结（D109） |
| M1 前端（资金流入、Token 详情、全局、中英文、管理后台） | 完成，连内置 mock（D110）；`npm run check` 通过 |
| M1 后端（采集、解析、计价、持仓、接口） | 未开始；任务卡见 [16](engineering/16-m1-agent-tasks.md) |
| 待拍板 | 大仓/小仓买入阈值（DECISIONS §9）；Base/BSC 聚合器（M5前）；Raw 保存期限（D24）；平台交易费（D69）。M1 管理鉴权与持有人范围已有默认（D106、D107） |

## 产品页面（`product/`）

| 文档 | 内容 | 里程碑 |
| --- | --- | --- |
| [00 总览](product/00-overview.md) | 页面地图、路由、全局布局与展示规则 | — |
| [01 资金流入](product/01-inflow.md) | 列表、筛选、排序 | M1 |
| [02 信号](product/02-signals.md) | 信号规则与卡片 | M3 |
| [03 牛人榜](product/03-leaderboard.md) | 榜单与资格 | M2 |
| [04 Token详情](product/04-token-detail.md) | 头部、K线、页签、侧栏 | M1/M3 |
| [05 钱包分析](product/05-wallet-analysis.md) | 资产、绩效、盈亏、活动 | M2 |
| [06 监控/合集/关注](product/06-tracker.md) | 关注、合集、黑名单、提醒 | M4 |
| [07 快捷交易](product/07-trading.md) | 报价、签名、状态 | M5 |
| [08 管理后台](product/08-admin.md) | 名单导入、运行质量 | M1 |

## 指标口径（`metrics/`）

| 文档 | 内容 |
| --- | --- |
| [Token指标](metrics/token-metrics.md) | 价格、市值、涨跌、流动性、交易数/额、净流入 |
| [钱包指标](metrics/wallet-metrics.md) | FIFO、收益、胜率、归因 |
| [聪明钱](metrics/smart-money.md) | 导入与自动识别、名单版本 |
| [地址标签](metrics/labels.md) | 开发者、狙击手、捆绑、老鼠仓、鲸鱼、专业交易员、KOL |

## 工程实现（`engineering/`）

| 文档 | 内容 |
| --- | --- |
| [01 架构](engineering/01-architecture.md) | 技术栈、模块、目录、风险 |
| [02 采集](engineering/02-ingestion.md) | 完整区块、前向与历史、断点、重组 |
| [03 解析](engineering/03-parsing.md) | 协议、归因、补证与重算 |
| [04 计价](engineering/04-pricing.md) | 美元价、主池、K线 |
| [05 持仓](engineering/05-holdings.md) | 快照基线与增量 |
| [06 数据模型](engineering/06-data-model.md) | 表说明；DDL → `backend/migrations/` |
| [07 API](engineering/07-api.md) | REST端点 |
| [08 实时推送](engineering/08-realtime.md) | WebSocket |
| [09 数据源](engineering/09-data-sources.md) | RPC供应商与准备卡 |
| [10 运维](engineering/10-operations.md) | 预算、安全、监控 |
| [11 里程碑](engineering/11-milestones.md) | M0–M5任务与验收 |
| [12 测试](engineering/12-testing.md) | 测试用例与发布 |
| [13 派生计算](engineering/13-analytics.md) | FIFO、聪明钱、标签、信号、榜单、列表投影 |
| [14 前端架构](engineering/14-frontend.md) | 像素风视觉、目录、数据获取与 mock、国际化、K 线、测试 |
| [15 登录与交易](engineering/15-auth-trading.md) | 签名登录、会话、关注提醒、交易实现 |
| [16 M1 任务卡](engineering/16-m1-agent-tasks.md) | 9 张自包含任务卡（P0、A、B0–B2、C1–C2、D、I）、公约、自检、交接模板 |
| [17 M1 验收 agent](engineering/17-m1-acceptance.md) | 全部合并后的追溯表、独立核对、验收报告 |
| [18 可观测性](engineering/18-observability.md) | 错误码目录、日志格式、trace-id、panic 兜底、Grafana＋Loki |

### 迁移文件对照

| 文件 | 层 |
| --- | --- |
| `00001_extensions_types.sql` | 扩展、`chain_id`、分区辅助函数 |
| `00002_collection.sql` | 采集层 |
| `00003_facts.sql` | 事实层（按链分区） |
| `00004_entities.sql` | 实体与解析控制 |
| `00005_derived.sql` | 派生（价、K线、持仓、FIFO） |
| `00006_lists_signals.sql` | 名单、标签、信号、榜单 |
| `00007_users.sql` | 用户、会话、合集、提醒、交易意图 |
| `00008_event_stream.sql` | 统一事件流 |

启动 `radar` 时自动执行 goose up。列名避开保留字：`time_window`、`candle_interval`。

## 功能演示（`demos/`）

每个功能点：真实链上样本（2026-10-16 公共 RPC）→ 正常流程 → 异常兜底 → 验收。索引与复现命令见[功能演示](demos/README.md)。

| 文档 | 功能点 |
| --- | --- |
| [01 区块采集](demos/01-ingestion.md) | 区块头、实测大小、单块时序、RPC错误分类、重组 |
| [02 协议解析与归因](demos/02-parsing.md) | 7笔真实成交逐字段解码与守恒校验 |
| [03 计价与K线](demos/03-pricing.md) | 报价资产价、市值、主池、K线 |
| [04 持仓](demos/04-holdings.md) | 交易前后余额、基线、持有人 |
| [05 FIFO账本](demos/05-ledger.md) | 批次、未知成本、重算 |
| [06 聪明钱与信号](demos/06-smart-money-signals.md) | 自动识别、名单版本、信号触发与冻结 |
| [07 地址标签](demos/07-labels.md) | 真实新币的开发者、狙击手、捆绑判定 |
| [08 资金流入列表](demos/08-inflow-query.md) | 固定顺序翻页、未知值、410 |
| [09 实时推送](demos/09-realtime.md) | 快照衔接、补发、改判 |
| [10 登录与交易](demos/10-auth-trading.md) | SIWE/SIWS、报价、状态 |


## 写作约定

1. 决策只改 DECISIONS；其他文档引用编号，不重复论证。
2. 指标定义只写在 `metrics/`；产品页只引用字段名。
3. 表结构以 `backend/migrations/` 为准；`06-data-model` 是说明与对照。
4. 每个指标附可手算算例；演示中构造数据必须标「构造」，真实数据须能从 `demos/samples/` 复现。
5. 未实现、未实测的内容不写成「已完成」或「已验证」。
6. 真实 Key、私钥不写入任何文档。
