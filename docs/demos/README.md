# 功能演示（Demo）

> 总导航：[docs/README](../README.md)。设计文档说「应该怎么做」，这里用真实链上数据演示「具体会遇到什么、怎么处理」。

每个功能点一份：**真实样本 → 正常流程 → 异常兜底流程 → 处理规则 → 验收用例**。

## 1. 样本来源

2026-10-16 13:42–13:56（UTC）从公开RPC只读抓取，未使用任何API Key：

| 链 | 端点 | 抓取内容 |
| --- | --- | --- |
| Solana | `api.mainnet-beta.solana.com` | slot 457658127–457658129完整区块；Pump曲线买入、PumpSwap成交、SOL/USDC成交；slot 457672911–457672950中一个Pump新币的前22笔成交及买家资金来源 |
| Base | `mainnet.base.org` | 区块52684548–52684550（含全部回执）；Uniswap v4成交、聚合器两跳买入、WETH/USDC成交 |
| BSC | `bsc-dataseed.bnbchain.org` | 区块127989024–127989026（含全部回执）；Four.meme创建＋首买、Four.meme买入、USDT→WBNB→Token两跳成交 |

| 文件 | 内容 |
| --- | --- |
| `samples/{chain}/block-header.json` | 区块头原文 |
| `samples/{chain}/block-size.json` | 实测单块大小（JSON、zstd压缩后） |
| `samples/{chain}/*.json` | 单笔交易原文（Solana为交易＋meta；EVM为交易＋回执） |
| `samples/solana/pump-launch-early-trades.json` | Pump新币`HWgMx…pump`创建后5个slot内的成交、同slot买家的资金来源核查 |
| `samples/decoded.json` | 解码结果：事件字段、价格、市值、手续费拆分 |

重新抓取与解码：

```bash
python3 scripts/fetch_samples.py          # 抓取；完整区块写到 .local/samples/（不进仓库）
python3 scripts/decode_samples.py         # 解码，生成 samples/decoded.json
python3 scripts/scan_pump_launch.py       # 扫描Pump新币早期成交（约3分钟），生成 samples/solana/pump-launch-early-trades.json
```

链上状态一直在变：重新运行会得到另一批区块和另一个新币，文档中的数字以仓库里已提交的样本为准。

由样本推出的报价资产美元价（用于本目录全部演示）：

| 资产 | 美元价 | 来源交易 |
| --- | --- | --- |
| SOL | 105.247 | Solana `4sewivDn…KE2v`：用户付248 USDC得到2.356362194 SOL |
| ETH | 2,454.07 | Base `0x8ee180bb…477e`：WETH/USDC 0.05%池（`0xd0b5…f224`）成交 |
| BNB | 729.40 | BSC `0x674ae793…ff21`：WBNB/USDT Pancake V2对（`0x16b9…0dae`）第一跳 |

## 2. 演示目录

| 文档 | 功能点 | 使用的真实样本 |
| --- | --- | --- |
| [01 区块采集](01-ingestion.md) | 完整区块抓取、落盘、断点、重组 | 三链区块头与实测大小；5个真实RPC错误（-32015、403、-32004、-32005、429） |
| [02 协议解析与归因](02-parsing.md) | 解码、方向、钱包归因、多跳归并 | Pump路由买入、PumpSwap反向池、Four.meme USDT报价与创建、v4经路由卖出、Base聚合器两跳、BSC两跳 |
| [03 计价与K线](03-pricing.md) | 报价资产美元价、Token价格、市值、主池、K线 | 三笔稳定币池成交；Pump、PumpSwap、Four.meme价格与市值 |
| [04 持仓](04-holdings.md) | 余额变化、快照基线、持有人 | Pump与PumpSwap成交的Token余额变化 |
| [05 FIFO账本与钱包统计](05-ledger.md) | 批次、卖出、未知成本、重算 | 以Pump真实买入为第一笔 |
| [06 聪明钱与信号](06-smart-money-signals.md) | 自动识别、名单切换、信号触发 | 构造钱包数据（链上动作格式同真实样本） |
| [07 地址标签](07-labels.md) | 开发者、狙击手、捆绑、老鼠仓 | Pump新币前22笔成交与资金来源；Four.meme创建＋首买 |
| [08 资金流入列表](08-inflow-query.md) | 筛选排序、固定顺序翻页、刷新 | 用样本Token组成的列表 |
| [09 实时推送](09-realtime.md) | 订阅、补发、改判 | 以样本成交生成的事件帧 |
| [10 登录与交易](10-auth-trading.md) | 签名登录、报价、签名、状态 | 以样本Token为交易对象 |

## 3. 样本中发现、已回写到设计的问题

| 发现 | 影响 | 处理 |
| --- | --- | --- |
| Solana主网已有**v1版本交易**，`maxSupportedTransactionVersion: 0`时`getBlock`直接报错`-32015` | 按旧参数采集会整块失败 | 采集参数改为当前最高版本，未知版本报警（[01](01-ingestion.md)） |
| Solana投票交易占交易数56%–70%，但只占压缩后体积19%–26% | 剔除投票交易省不了一半存储 | 修正[技术方案§4](../TECHNICAL_PLAN.md)取舍表 |
| BSC区块头`milliTimestamp`显示**出块间隔450ms**，每天约19.2万块 | 容量与吞吐估算偏低 | 修正容量表 |
| Base公共端点对无User-Agent的请求返回403 | 403不是限流，重试无效 | 区分可重试与不可重试错误（[01](01-ingestion.md)） |
| Pump新指令名`BuyExactQuoteInV2`、`BondingCurveV3`；交易由第三方路由程序转调 | 只按顶层指令识别会漏单 | 按内部指令＋事件识别（[02](02-parsing.md)） |
| PumpSwap池base为WSOL：指令名`Sell`实为买入Token | 按指令名判方向会反 | 统一按Token余额流向判方向（[02](02-parsing.md)） |
| Base v4成交中，用户收到的原生ETH在日志里不可见 | 无法仅凭日志证明收款方 | 需要`debug_traceTransaction`补证，RPC需支持trace（[02](02-parsing.md)） |
| Four.meme代币可以用USDT报价 | 不能假设曲线一律BNB计价 | 报价资产按事件与转账识别（[02](02-parsing.md)） |
| Pump代币总供应量9.55亿而非10亿（部分已销毁） | 市值不能用固定10亿 | 读取链上供应量（[03](03-pricing.md)） |
| 公共RPC平均1.1–1.4秒/次；Solana 3并发只有0.26块/秒并出现429 | 公共端点无法支撑采集 | 生产必须用付费节点（[01](01-ingestion.md)） |
| BSC公共端点`eth_getLogs`单块也返回`-32005 limit exceeded` | 依赖`eth_getLogs`的方案不可用 | 采集只用“区块＋回执”（[01](01-ingestion.md)） |
| Base“多池成交”人工核对后是聚合器两跳买入，含1%聚合器费与1.0%/1.3%两笔Token分成 | 只看swap事件数会误判为套利 | 套利需同一资产进出且净额为正（[02](02-parsing.md)） |
| Pump新币同slot买入的3个钱包与开发者无资金关联 | 现行“同slot即捆绑”规则会误判 | 已改为需要关联证据（D72，[07](07-labels.md)） |
| Four.meme最近200个BSC区块只有6个新币、15笔成交 | BSC标签样本不足 | M0扩大扫描范围 |
