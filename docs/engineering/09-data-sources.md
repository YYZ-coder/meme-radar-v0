# 数据源与准备清单

> 决策D52（自建索引）、D14（验证预算USD100/月）。截至2026-10-16：**尚无已申请的服务商或套餐**，以下均为候选，未实测。

## 1. 各功能的数据来源

| 功能 | 来源 | 是否必需第三方 |
| --- | --- | --- |
| 区块、交易、执行结果 | 三链RPC（完整区块＋全回执，30天历史，D73） | 是：RPC供应商 |
| 成交、K线、交易额、净流入、聪明钱统计、信号、榜单、标签 | 本地从Raw计算 | 否 |
| 当前持仓初始快照 | RPC账户查询或索引服务（D46） | 视链而定，见[持仓](05-holdings.md) §4 |
| Token图标、简介、社交链接 | 链上元数据URI（Metaplex、Token-2022、ERC20），缺失时用DEX Screener等补充 | 可选 |
| 价格校验 | DEX Screener、Birdeye | 可选 |
| KOL名单 | 管理员导入 | 否 |
| 交易报价 | Solana Jupiter；Base/BSC聚合器待选（0x、1inch、OKX DEX等） | M5需要 |
| 安全检测、气泡图、AI | GoPlus等，待定（D65） | M5之后 |

## 2. RPC能力要求

| 链 | 必需方法 | 关注点 |
| --- | --- | --- |
| Solana | `getSlot`、`getBlocks`、`getBlock`（`maxSupportedTransactionVersion=1`、完整交易与meta）、`getSignaturesForAddress`、`getTransaction`、`slotSubscribe`或`blockSubscribe` | 主网已有v1交易，参数为0时整块报错`-32015`；30天前的`getBlock`历史可得性；每天约21.6万slot |
| Base | `eth_blockNumber`、`eth_getBlockByNumber(n, true)`、`eth_getBlockReceipts`或逐笔`eth_getTransactionReceipt`、`eth_subscribe newHeads`；补证需要`debug_traceTransaction`（callTracer）与归档`eth_getBalance` | 2秒出块，每天约4.3万块；批量回执方法是否开放；路由解包后发给用户的原生ETH在日志中不可见，必须用trace补证 |
| BSC | 同Base | 实测0.45秒出块（区块头`milliTimestamp`），每天约19.2万块；公共端点`eth_getLogs`被禁用（`-32005`），采集只用区块＋回执 |

Base官方公共RPC明确不适合生产（未带User-Agent的请求直接返回403）；BNB公共端点禁用`eth_getLogs`；Solana公共端点3并发即出现429。公共端点只用于小样本验证，实测记录见[Demo 01](../demos/01-ingestion.md)。

## 3. 候选供应商

| 供应商 | 链 | 已知计费（官方页面，2026-10-16） | 待核验 |
| --- | --- | --- | --- |
| [Helius](https://www.helius.dev/docs/billing/credits) | Solana | Free 1M credits/月；Developer $49/月含10M；`getBlock`按1 credit，DAS 10，Wallet API 100，WSS按流量另计 | 30天历史`getBlock`是否开放、全天全块的实际credit与流量 |
| [Alchemy](https://www.alchemy.com/pricing) | Base、BSC、Solana | 按CU计费 | `getAssetTransfers`内部转账支持Base但未列BSC；回执批量方法 |
| [QuickNode](https://www.quicknode.com/pricing) | 三链 | 按credit计费 | 每链端点数、逐方法credit、archive/trace是否需另开 |
| [Etherscan V2](https://docs.etherscan.io/supported-chains) | Base、BSC | 一个Key多链 | [holderlist](https://docs.etherscan.io/api-reference/endpoint/tokenholderlist)需PRO及以上且无固定块参数，不能当原子快照 |
| [Moralis](https://moralis.com/pricing/) | Base、BSC | 套餐分产品 | [Token holders](https://docs.moralis.com/data-api/evm/token/holders/token-holders)是否全量、能否固定块 |
| [Birdeye](https://data.birdeye.so/docs/quickstart) | Solana | 待查 | 持有人快照与价格校验 |
| [Jupiter](https://developers.jup.ag/docs/portal/plans) | Solana | 支持keyless低限速与免费Key | M5报价与限速 |

只需选一组能覆盖要求的组合，不要求全部开通。

## 4. 准备卡

| ID | 时机 | 内容 | 需要你做的 |
| --- | --- | --- | --- |
| PREP01 | M1 24小时验收前 | Solana 付费RPC（优先评估Helius）；开发期用公共端点（D75） | 公共端点撑不住时由你授权；Key只放后端秘密配置 |
| PREP02 | M1 24小时验收前 | Base＋BSC 付费RPC（同一家可开两条链） | 同上 |
| PREP03 | M2持有人页前 | Solana持仓快照来源（M1按D107不需要） | 待验证结果后再决定是否申请 |
| PREP04 | M2持有人页前 | Base/BSC持仓快照来源（M1按D107不需要） | 同上 |
| PREP05 | M1管理导入前 | 聪明钱/KOL名单文件 | 准备chain、address、label、tags、twitter、source |
| PREP06 | M1 | 元数据/价格校验（DEX Screener公开接口、Jupiter） | 暂不需要付费 |
| PREP10 | M1开工前 | Raw对象存储（S3兼容：AWS S3、Cloudflare R2、Backblaze B2等）；容量取舍见[技术方案§4](../TECHNICAL_PLAN.md) | M0实测后选定，需你授权开通 |
| PREP11 | M4前 | WalletConnect（Reown）projectId，用于EVM移动端钱包连接 | 免费注册，projectId可公开放前端 |
| PREP07 | M5 | Base/BSC交易聚合器 | 选型后申请Key |
| PREP08 | M5之后 | 安全检测、社交、AI | 待D65定方向 |

真实Key只放后端秘密配置，不进对话、仓库或前端。申请、购买、升级套餐需要你明确授权。

## 5. 验证顺序（M1第一周）

1. **小样本**：三链各取连续1,000个区块，核对交易数、meta/回执完整、hash链接；记录单块平均请求数、字节数、耗时。
2. **历史可得性**：取30天前的区块各100个，确认能取到完整数据。
3. **费用外推**：按每日区块数×单块成本得到前向与历史各自的月费用与存储；与USD100对比。
4. **快照样本**：各链挑3个Token，验证快照能否锚定链上位置、分页是否一致。
5. **结论**：费用不成立时给出取舍方案（缩短历史天数、降低历史并发、换供应商、提高预算），由你决定；不静默缩小前向范围。

每步执行前列出预计请求量和费用上限，达到上限即停止。
