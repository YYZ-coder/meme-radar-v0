# 可观测性：错误码、日志、trace-id、panic 兜底、Grafana

> 决策 D112。代码：后端 `backend/internal/obs/`（错误码目录、日志、trace、安全协程）与 `internal/httpapi/middleware.go`；前端 `src/lib/api/client.ts`、`src/components/ui/state.tsx`、`src/app/error.tsx`；日志栈 `ops/` 与 `docker-compose.yml` 的 `observability` profile。
> **所有后端包都必须通过 `internal/obs` 记日志、返回错误、启动协程**；新增错误码属于契约变更（§1.3）。

## 1. 错误码目录

### 1.1 API 错误码（`error.code`）

响应体 `{"error":{"code","message","field","trace_id"}}`。`code` 为 lower_snake，取值即 `api/openapi.yaml` 的 `ErrorCode`；前端按 code 从 `messages.errors` 取文案，不显示后端 `message` 原文。

| code | HTTP | 含义 | 前端处理 |
| --- | --- | --- | --- |
| `invalid_param` | 400 | 参数格式/取值非法，`field` 指出字段 | 表单标红该字段 |
| `invalid_range` | 400 | min > max | 筛选抽屉标红 |
| `unauthorized` | 401 | 未登录（M4 起） | 提示连接钱包 |
| `forbidden` | 403 | 管理令牌缺失或错误（D106） | 退出管理后台 |
| `not_found` | 404 | 资源或路由不存在 | 空状态 |
| `import_closed` | 409 | 导入报告已确认或过期 | 提示重新上传 |
| `query_expired` | 410 | 资金流入快照过期或不在本实例 | 回到顶部并重查 |
| `file_too_large` | 413 | 文件 >5MB | 提示 |
| `too_many_rows` | 413 | 文件 >10,000 行 | 提示 |
| `invalid_file` | 400 | 文件无法解析 | 提示 |
| `secret_detected` | 400 | 文件疑似含私钥/助记词，整文件拒收 | 提示 |
| `rate_limited` | 429 | 单 IP 每分钟 >60 次新查询；带 `Retry-After` | 保留已加载数据，提示稍后 |
| `busy` | 503 | 查询 >2 秒；带 `Retry-After` | 保留已加载行，给重试 |
| `api_unavailable` | 503 | 上游不可用（前端代理/mock 关闭） | 重试 |
| `internal` | 500 | 未预期错误或 panic；细节只进日志 | 显示 trace_id，给重试 |

前端另有两个本地码：`network`（请求未到达）、`invalid_response`（响应未通过 zod 校验）。

### 1.2 内部事件码（日志 `code` 字段）

格式 `area.reason`，只出现在日志与告警，不返回给客户端。“告警”列为是的码会进入管理后台运行质量和 Grafana 告警。

| code | 级别 | 告警 | 说明 / 关联 |
| --- | --- | --- | --- |
| `ingest.rpc_retryable` | warn | 否 | 限流/超时/5xx，`block_jobs.last_error_class=retryable` |
| `ingest.rpc_config` | error | 是 | 鉴权/方法不支持，`config` |
| `ingest.block_not_found` | warn | 否 | `not_found`（含 Solana 跳过 slot 的判定前） |
| `ingest.history_unavailable` | error | 是 | 节点无历史，`history_unavailable`；缺口 `collection_gaps.reason=history_unavailable` |
| `ingest.block_incomplete` | warn | 否 | 回执数≠交易数等，`incomplete` |
| `ingest.reorg_detected` | warn | 否 | 写 `reorg_events`，触发重解析 |
| `ingest.gap_recorded` | error | 是 | 重试耗尽，`collection_gaps.reason=retry_exhausted` |
| `ingest.lease_expired` | warn | 否 | 任务租约过期被重新领取 |
| `ingest.budget_paused` | error | 是 | D104 预算暂停，`collection_gaps.reason=budget_paused` |
| `parse.unknown_discriminator` | warn | 否 | 未知判别码/topic，只计数 |
| `parse.decode_failed` | error | 是 | 已知指令解码失败，隔离该交易 |
| `parse.conservation_failed` | error | 是 | 守恒校验失败，写 `parse_quarantine` |
| `parse.internal` | error | 是 | 解析器内部错误，写 `parse_failures`，模块停在该位置 |
| `parse.attribution_pending` | info | 否 | 归因需补证，入 `attribution_tasks` |
| `parse.reparse` | info | 否 | 按区间重解析（重组/升级） |
| `compute.price_stale` | warn | 否 | 报价资产价 >5 分钟（D54） |
| `compute.price_missing` | error | 是 | >30 分钟无价，停止输出美元值 |
| `compute.main_pool_switched` | info | 否 | 主池切换（D55） |
| `compute.recompute` | info | 否 | 重算任务执行 |
| `holding.partial_coverage` | info | 否 | Token 早于下界 L，持有人标 partial（D107） |
| `holding.negative_balance` | error | 是 | 余额重放出现负数，数据异常 |
| `smartmoney.import_applied` | info | 否 | 导入确认 |
| `smartmoney.list_version_changed` | info | 否 | 名单版本变化 |
| `api.slow_query` | warn | 否 | 查询接近或超过 2 秒 |
| `api.snapshot_expired` | info | 否 | 返回 410 |
| `api.admin_denied` | warn | 否 | 管理接口 403 |
| `runtime.panic_recovered` | error | 是 | 任意 panic 被兜住（含 stack） |
| `runtime.goroutine_restarted` | warn | 是 | 守护者重启了 worker |
| `runtime.db_unavailable` | error | 是 | 数据库不可达 |

### 1.3 变更规则

新增或修改错误码 = 契约变更：同时改 `backend/internal/obs/errors.go` 的常量与 `Catalog`、本表、（API 码）`api/openapi.yaml` 的 `ErrorCode` 与前端 `messages.errors`。`TestAPICodesMatchOpenAPI`、前端 i18n 测试与 `npm run api:check` 会拦住不一致。

## 2. 日志格式

- slog JSON，一行一条，写 stdout；配置 `log.file`（或 `RADAR_LOG_FILE`）时同时写文件，`make -C backend run` 默认写 `.local/logs/radar.log`。
- 固定字段：`time`、`level`、`msg`、`service`(radar)、`role`(all/ingest/parse/compute/api)、`version`(git sha)、`trace_id`、`parent_trace_id`（有父 trace 时）、`code`（错误与事件码）。
- 业务字段（用 `obs.Key*` 常量，不写字面量）：`chain`、`module`、`job_id`、`block`、`position`、`token`、`wallet`、`query_id`、`list_version`、`task`、`err`、`stack`、`duration_ms`。
- HTTP 访问日志 `msg="http request"`：`method`、`route`（匹配到的路由模板，不是原始路径）、`status`、`duration_ms`、`bytes`、`ip_hash`（IP 的 SHA-256 前 12 位，不记原始 IP）。
- 脱敏：键名含 authorization、password、secret、private、mnemonic、seed、api_key、cookie、bearer 或以 `_token` 结尾的值写为 `[redacted]`（`token` 键是 Token 地址，不脱敏）。仍然禁止把请求体、RPC Key、DSN 整体写进日志。
- 级别：错误码的级别由 `Catalog` 决定；请求日志 5xx 为 error、4xx 为 warn、其余 info。

```json
{"time":"2026-10-19T12:00:01.2Z","level":"WARN","msg":"http request","service":"radar","role":"api","version":"abf3e39","method":"GET","route":"GET /api/v1/smart-money/inflow","status":410,"duration_ms":3,"bytes":96,"ip_hash":"4f1a…","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}
```

## 3. trace-id

- 格式：32 位小写十六进制，与 W3C `traceparent` 的 trace-id 相同，以后可无缝接入 OpenTelemetry（M1 不引入）。
- HTTP：采用请求中合法的 `X-Trace-Id`，否则取 `traceparent`，都没有就新生成；写入 ctx、每行日志、响应头 `X-Trace-Id`、错误体 `error.trace_id`。
- 前端：每个请求生成 trace-id 并通过 `X-Trace-Id` 发送；`ApiError.traceId` 优先用响应头；错误状态显示可复制的追踪 ID；5xx 与网络错误在控制台打印 code 和 trace_id。
- 后台：`obs.Go` / `obs.Loop` / `obs.Supervise` 的每次执行、每次循环迭代都生成新 trace-id，父 trace 记为 `parent_trace_id`。处理具体对象时用 `obs.WithAttrs(ctx, chain, block, job_id…)` 附加字段。
- 只用 `slog.*Context(ctx, …)` 记日志，否则带不上 trace。

## 4. panic 兜底

| 位置 | 机制 | 结果 |
| --- | --- | --- |
| HTTP 请求 | 中间件 recover | 返回 500 `internal`（带 trace_id，不泄露 panic 内容），记 `runtime.panic_recovered`＋stack，服务继续 |
| 一次性协程 | `obs.Go(ctx, task, fn)` | panic 转为返回错误并记日志 |
| 周期任务 | `obs.Loop(ctx, task, interval, fn)` | 每次迭代独立 recover，一块坏数据只影响本次迭代 |
| 常驻 worker | `obs.Supervise(ctx, task, fn)` | panic 或返回错误后指数退避（1s→1min）重启，记 `runtime.goroutine_restarted` |
| 启动阶段 | `main` 的 recover | 记录后以退出码 2 退出（启动失败应立即暴露） |
| 前端渲染 | `app/error.tsx`（路由级）、`app/global-error.tsx`（根布局级） | 显示兜底页与 digest，可重试；外壳导航不受影响 |

- 禁止裸 `go` 语句：`make -C backend lint-go` 检查（`internal/obs` 与测试除外），已纳入 `make check` 和 pre-commit。
- 不要用 recover 吞掉业务错误；能返回 error 的地方返回 `obs.Error`。

## 5. Grafana 查看日志

```sh
make obs-up                  # Loki + Alloy + Grafana（docker compose --profile observability）
make -C backend run          # 日志写到 .local/logs/radar.log，Alloy 采集
open http://localhost:3300   # Grafana（端口可用 GRAFANA_PORT 改；匿名只读；管理员密码 GRAFANA_ADMIN_PASSWORD，默认 radar-dev）
```

- Alloy 采集 `.local/logs/*.log` 和带标签 `radar.logs=true` 的容器 stdout，解析 JSON；只把低基数字段 `level`、`service`、`role`、`code` 设为 Loki label，`trace_id`、地址等留在日志行里。
- 预置看板 “Meme Radar / Radar logs”：错误数、panic 数、worker 重启数、HTTP 5xx、按 code 的时序、日志面板（变量 trace_id / level / code）。Loki 数据源配置了 `trace_id` 派生字段，点日志里的 trace_id 跳到同一 trace 的全部日志。
- 2026-10-19 本地实测：带 `X-Trace-Id` 的请求在 Loki 中按 trace_id 查到访问日志与业务日志，看板 6 个面板查询均可执行。Grafana 默认端口 3300（避开本机另一项目占用的 3001），API 本地验证用 `RADAR_HTTP_ADDR=:18080`（8080 同样被占用）。
- 本地保留 7 天，仅用于开发；生产部署另定（[运维§5](10-operations.md)）。

常用 LogQL：

```text
{service="radar"} | json | trace_id="4bf92f3577b34da6a3ce929d0e0e4736"   # 一个请求/任务的全部日志
{service="radar", code="runtime.panic_recovered"} | json | line_format "{{.task}} {{.err}}"
sum by (code) (count_over_time({service="radar", level="ERROR"}[5m]))
{service="radar"} | json | msg="http request" | status >= 500
{service="radar", code=~"ingest.*"} | json | chain="bsc"
```

## 6. 验收要点

- 任意请求响应有 `X-Trace-Id`；同一 trace 能在 Grafana 查到访问日志及其产生的业务日志。
- 处理器 panic 返回 500 JSON，进程继续服务；`obs.Loop` 中某次迭代 panic 后下一次迭代照常执行。
- 错误码三处一致（Go 目录、openapi、前端 messages），测试通过。
- 日志中没有 admin token、Authorization、私钥类字段的明文。
