# Meme Radar 前端

像素风多链聪明钱终端（D108），Next.js 16 App Router + React 19 + TypeScript。架构说明见 [docs/engineering/14-frontend.md](../docs/engineering/14-frontend.md)，规范见 [AGENTS.md](AGENTS.md)。

## 运行

```sh
npm ci
npm run dev                                   # 未设置 RADAR_API_ORIGIN：使用内置 mock 接口
RADAR_API_ORIGIN=http://localhost:8080 npm run build && npm run start   # 连 Go API（构建时生效）
```

| 环境变量 | 说明 |
| --- | --- |
| `RADAR_API_ORIGIN` | Go API 地址，构建时写入转发规则；不设置则 `/api/v1/*` 由 `src/mock` 回答，页面显示“演示数据” |
| `RADAR_API_MOCK=0` | 禁用内置 mock（未配置 API 时返回 503） |
| `RADAR_MOCK_ADMIN_TOKEN` | mock 管理令牌，默认 `mock-admin`；真实后端用服务端 `RADAR_ADMIN_TOKEN` |

## 页面

| 路由 | 内容 | 里程碑 |
| --- | --- | --- |
| `/smart-money/inflow` | 资金流入：概览卡、链/窗口/DEX/平台/筛选（写入 URL）、排序、无限滚动（`query_id`，410 自动重查）、10 秒刷新可见行、观察列表 `?watch=1` | M1 |
| `/token/{chain}/{address}` | 头部、K 线（6 周期、价格/市值、USD/原生币、MA/EMA/BOLL/VOL/MACD/RSI、聪明钱买卖点、截图、全屏）、活动、持有者（覆盖范围）、流动性池/变化、行情摘要、聪明钱持仓、开发者、同名代币 | M1 |
| `/admin/health`、`/admin/import`、`/admin/members` | 运行质量、名单导入（校验→确认）、名单与排除管理 | M1 |
| `/smart-money/signals`、`/smart-money/leaderboard` | 占位（M3、M2） | — |

全局：搜索（`/` 聚焦）、中英文（Cookie）、计价切换与减少动效（设置）、底部栏主流币价。

## 验证

```sh
npm run check   # format → lint → api:check → typecheck → test → build → test:smoke
npm run api:types   # 修改 ../api/openapi.yaml 后重新生成类型
```

浏览器冒烟使用独立端口 3107 启动本次生产构建，浏览器语言 zh-CN。

## 最近一次验证（2026-10-19，Node 25.8.0）

- `npm run check` 通过：31 个单测（含 11 个 mock 契约测试）、14 个 Chromium 冒烟；冒烟 `--repeat-each=5` 共 70 次全部通过。
- 人工查看 1440×1000 桌面（中/英）与 390×844 手机的资金流入、Token 详情、管理后台截图，无控制台错误。
- 未验证：连接真实 Go API（后端未实现）、Node LTS、非 Chromium 浏览器。
