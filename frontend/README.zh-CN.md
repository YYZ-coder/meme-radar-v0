# Meme Radar 前端

[English](README.md) | **简体中文**

像素风多链聪明钱终端，Next.js 16 App Router + React 19 + TypeScript。架构、数据流及一致性边界见 [简略技术方案](../TECHNICAL_OVERVIEW.zh-CN.md)，规范见 [AGENTS.md](AGENTS.md)，接口契约见 [api/openapi.yaml](../api/openapi.yaml)。

## 运行

Node 24 LTS、npm 11，浏览器为 Chromium。

```sh
npm run setup   # npm ci + 安装 Chromium
npm run dev     # 开发服务器（127.0.0.1:3000），默认连内置 mock
```

| 环境变量                 | 作用                                                                                         |
| ------------------------ | -------------------------------------------------------------------------------------------- |
| `RADAR_API_ORIGIN`       | 设置后 `/api/v1/*` 与 `/healthz` 转发到该 Go API；后端不可达返回带 trace 的 503，不回退 mock |
| `RADAR_API_MOCK=0`       | 未设置 origin 时显式关闭 mock（所有接口返回 503）                                            |
| `RADAR_MOCK_ADMIN_TOKEN` | mock 管理令牌，默认 `mock-admin`                                                             |
| `RADAR_E2E_PORT`         | 浏览器冒烟使用的生产服务端口，默认 31337（31000–31999）                                      |

连 Go 后端：仓库根目录 `make dev-connected`（Go mock API :8080＋本开发服务器 :3000）。`npm run test:connected` 用 Playwright 同时拉起 `../backend/bin/radar`（需先 `make -C ../backend build`）与本次生产构建，跑同一套冒烟；`RADAR_E2E_API_PORT` 默认 31437。

## 页面

| 路由                                               | 内容                                                                                                                                                                             | 里程碑 |
| -------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------ |
| `/smart-money/inflow`                              | 资金流入：概览卡、链/窗口/DEX/平台/筛选（写入 URL）、排序、无限滚动（`query_id`，410 自动重查）、10 秒刷新可见行、观察列表 `?watch=1`                                            | M1     |
| `/token/{chain}/{address}`                         | 头部、K 线（6 周期、价格/市值、USD/原生币、MA/EMA/BOLL/VOL/MACD/RSI、聪明钱买卖点、截图、全屏）、活动、持有者（覆盖范围）、流动性池/变化、行情摘要、聪明钱持仓、开发者、同名代币 | M1     |
| `/admin/health`、`/admin/import`、`/admin/members` | 运行质量、名单导入（校验→确认）、名单与排除管理                                                                                                                                  | M1     |
| `/smart-money/signals`、`/smart-money/leaderboard` | 占位（M3、M2）                                                                                                                                                                   | —      |

全局：搜索（`/` 聚焦）、中英文（Cookie）、计价切换与减少动效（设置）、底部栏主流币价。

## 验证

```sh
npm run check       # format → lint → api:check → typecheck → test → build → test:smoke
npm run test:smoke  # 需先 build；Playwright 自行启动本次生产构建（reuseExistingServer: false）
npm run test:e2e    # build 后跑全部浏览器用例
npm run api:types   # 修改 ../api/openapi.yaml 后重新生成类型
```

生产冒烟覆盖桌面/移动端关键流程、名单准备中刷新恢复、激活、排除/恢复，以及旧名单数值刷新暂停后主动重查。报告在 `playwright-report/`，失败截图在 `test-results/`。非 Chromium 浏览器尚未验收。
