# RPC 提供方

[English — default](RPC_PROVIDERS.md) · [简体中文](RPC_PROVIDERS.zh-CN.md)

QuickNode 是 Solana、Base、BSC 主网的可选主 RPC，现有公共节点保留为自动兜底。未配置私有 URL 时，各角色继续使用公共节点。

## 配置

完整的、含鉴权信息的 HTTP 端点 URL 只放在被忽略的本地 .env，不进入跟踪的 YAML、源码、日志或浏览器代码：

    QUICKNODE_SOLANA_RPC_URL=
    QUICKNODE_BASE_RPC_URL=
    QUICKNODE_BSC_RPC_URL=

填入 QuickNode 提供的完整 HTTP URL。账户管理 API key 用于创建/管理端点，不能直接当 JSON-RPC URL；应用容器不需要管理凭证。

内嵌链配置通过 rpc_url_env 指定变量名，自定义配置也可使用此字段。设置后的 URL 必须是带主机名、没有 fragment 的有效 HTTP(S) URL；变量为空或未设置时保持仅公共节点。

| 链 | 私有主源变量 | 采集公共兜底 | 查询/回补公共兜底 |
| --- | --- | --- | --- |
| Solana | QUICKNODE_SOLANA_RPC_URL | https://api.mainnet-beta.solana.com | 同左 |
| Base | QUICKNODE_BASE_RPC_URL | https://mainnet.base.org | 同左 |
| BSC | QUICKNODE_BSC_RPC_URL | https://bsc-rpc.publicnode.com | https://bsc-dataseed.bnbchain.org |

采集以 ingest.rpc_url 兜底；回补以 backfill.rpc_url 兜底，省略时使用公共采集 URL；解析、计算、补全和 API 原生币余额查询以 lookup_rpc_url 兜底，省略时使用公共采集 URL。既有角色及方法限速保留，并由各次节点尝试共享。

显式 ingest --rpc-url 为该进程指定唯一端点，保留运维人员对私有分叉、自定义网络的控制。

## 切换行为

- 每次先尝试主源，每个不同的兜底端点至多尝试一次。
- 只允许项目明确列出的只读方法兜底；交易提交及未知方法不自动重放。
- 可兜底：连接/超时、HTTP 401/403/429/5xx、无法读取/解析的 JSON-RPC 响应，以及内部错误 -32603、方法不可用 -32601、节点不健康/限流 -32005。
- 非法参数、合约 revert、跳过的 slot、合法 null 及调用方取消保持原语义。
- 配置兜底时，单个节点尝试至多 15 秒，包含本地限速等待；仅公共节点时保留原 HTTP 客户端超时。上层截止时间更短时，在剩余尝试间分配。API 原有 2 秒期限保持不变。
- 全部失败时普通调用返回错误；持有人附属原生币余额沿用真实 null，不伪造零。
- 当前是按顺序兜底，没有持久熔断器；下一次仍先尝试主源。指标按实际尝试的主机统计，不含鉴权路径/查询参数；运行日志只记主机。
- HTTP 错误正文不写入错误；传输错误的 URL 包装及响应中反射的鉴权长度路径/查询值会脱敏。

尝试预算用于兼容本机实测冷建连延迟，不代表 200 ms 已达标；冷启动、网络、SQL 和 API 性能仍需单独验收。

## 验证

前端门禁使用项目支持的 Node 24。

    make check
    RADAR_TEST_DATABASE_URL=postgres://radar:radar@127.0.0.1:55432/postgres?sslmode=disable       make -C backend test-race

示例 URL 仅用于专用本地测试库；每项集成测试创建并删除自己的临时数据库，不能换成生产业务库。

测试覆盖私有优先、公共恢复、全部失败/空值、截止时间、业务错误、并发读取、脱敏、真实区块样本采集/回补，以及补全持久化。未设置测试变量时 PostgreSQL 测试会跳过；mock 通过不等于真实链路上线验收。

端点创建和有界真实身份/高度探测单独记录。用本地模拟主源故障配合真实公共节点响应验证切换，无需破坏实际 QuickNode 端点。

## 部署

部署已测试版本时，为现有角色进程提供三个运行 URL 变量，账户管理凭证单独保管。运行中的二进制或容器不会自动获取新代码或环境。

遵守仓库 worktree、合并、提交和部署规则；worktree 只做隔离测试和线上只读探测，部署由指定主工作区在授权及必要检查后执行。

协议依据：[QuickNode 鉴权](https://www.quicknode.com/docs/build-with-ai/quicknode-apis)、[端点清单](https://www.quicknode.com/docs/admin-api/endpoints/v0-endpoints)、[端点创建](https://www.quicknode.com/docs/admin-api/endpoints/v0-endpoints-post)。
