# 前端架构

> 页面规格见[产品总览](../product/00-overview.md)，工程规范见[前端规范](../../frontend/AGENTS.md)，运行与验证见 `frontend/README.md`。视觉 D108，数据来源 D110。

## 1. 选型

| 用途 | 选型 | 说明 |
| --- | --- | --- |
| 框架 | Next.js 16 App Router、React 19、TypeScript strict | 页面路由为 Server Component，交互部分为 Client Component |
| 视觉 | 像素风终端（D108）：`src/app/globals.css` 设计变量＋组件样式；Silkscreen 本地字体；`components/pixel-art.tsx` 像素头像、雷达、迷你走势 | Tailwind 已接入但当前未使用原子类；新样式继续写在 `globals.css` 对应分节 |
| 弹窗与下拉 | 原生 `<dialog>`（`components/ui/dialog.tsx`）、自写多选下拉 | 不引入 Radix：现有需求原生已满足（焦点、Esc、遮罩） |
| 数据请求 | TanStack Query | 无限滚动、10 秒刷新、失败重试 |
| 长列表 | TanStack Virtual（窗口滚动） | 资金流入最多 2,000 行 |
| 国际化 | next-intl，不带语言前缀路由 | 语言存 Cookie `NEXT_LOCALE`（B11） |
| K 线 | TradingView Lightweight Charts v5，动态导入 | 保留 TradingView 署名 |
| API 类型 | openapi-typescript 生成 `src/lib/api/schema.gen.ts`；zod 在边界校验 | 生成文件不手改，`npm run api:check` 防漂移 |
| 钱包（M4–M5） | `@solana/wallet-adapter-react`；wagmi＋viem | 未安装，到 M4 再加 |

## 2. 目录

```text
frontend/src/
├── app/
│   ├── layout.tsx                    # next-intl、Providers、AppShell
│   ├── api/v1/[...path]/route.ts     # 内置 mock 接口（D110）
│   ├── smart-money/{inflow,signals,leaderboard}/page.tsx
│   ├── token/[chain]/[address]/page.tsx
│   └── admin/{layout,health,import,members}
├── components/                       # icon、pixel-art、token-avatar、shell/（顶栏、侧栏、状态栏、搜索）、ui/（dialog、toast、address、state、multi-select）
├── features/
│   ├── smart-money/                  # 共享头部：介绍、概览卡、页签、链选择；M2/M3 页签占位
│   ├── inflow/                       # URL 状态、快照分页、可见行刷新、表格、筛选抽屉、侧栏
│   ├── token/                        # 头部、K 线与指标、活动/持有者/流动性页签、右侧栏
│   └── admin/                        # 令牌门禁、运行质量、名单导入、名单管理
├── lib/
│   ├── api/                          # schema.gen.ts（生成）、types、schemas（zod）、client、queries
│   ├── format/                       # 金额、价格下标零、百分比、币龄、地址
│   ├── chains.ts、preferences.ts、watchlist.ts、local-store.ts
├── i18n/                             # 语言解析（Cookie → Accept-Language → zh-CN）
├── messages/{zh-CN,en}.json
└── mock/                             # 契约的确定性实现与契约测试
```

## 3. 数据获取

| 场景 | 方式 |
| --- | --- |
| 接口地址 | 浏览器只请求同源 `/api/v1/*`。构建时设置 `RADAR_API_ORIGIN` → `next.config.ts` 转发到 Go；不设置 → 内置 mock，响应带 `x-radar-mock: 1`，页面显示“演示数据”。生产由反向代理直接转发到 Go |
| 首屏 | M1 由客户端请求（页面骨架服务端渲染）；服务端预取在接入真实 API 后按性能数据再定 |
| 资金流入 | URL 是筛选状态的唯一来源；首批建快照、翻页 `query_id`＋`offset`；410 时回到顶部、提示并重查；503/429 保留已加载行并给出重试 |
| 定时刷新 | 可见行 Token（防抖 400ms）每 10 秒 `POST /tokens/batch`，按 Token 更新数值、不重排；可暂停 |
| 观察列表 | localStorage（D111），用 `/tokens/batch` 取值 |
| 管理后台 | 令牌只存 sessionStorage；403 自动退出 |
| 实时（M4） | 单例 WS 客户端，事件按 `event_id` 去重写入 Query 缓存；断线退回轮询 |

## 4. 展示与格式化

- 金额为十进制字符串，只在展示时格式化；比例类数值由后端给出（D109），前端不做金额运算。K 线指标（MA/EMA/BOLL/VOL/MACD/RSI）属于图表展示计算。
- 极小价格下标零：`0.0₅412` 表示 0.00000412。
- 大数：中文“万/亿”，英文 K/M/B；百分比 2 位；未知显示“--”，已知 0 显示 `$0`。
- 时间：币龄 12d、3mo、23h39m；悬停显示 UTC 与本地时间。涨绿跌红并带正负号。

## 5. 国际化

- 文案全部在 `messages/*.json`；ESLint 规则禁止 JSX 中直接写字母或汉字。
- 单测检查两份翻译文件键集合与占位符一致。
- 链上数据（Token 名称、符号）原样显示，不翻译。

## 6. 钱包与交易（M4–M5）

- 钱包连接只在 Client Component 内；登录签名流程见[登录与交易](15-auth-trading.md)。
- 同一页面可同时连接一个 Solana 钱包和一个 EVM 钱包；登录身份只绑定一个钱包（D97、D98）。
- 会话失效（401）清空用户数据缓存；多标签页经 `BroadcastChannel` 同步。

## 7. 性能

- 资金流入表格虚拟化；行组件 memo，刷新只更新数值。
- K 线组件与图表库动态导入，不进首屏包。
- 图标：M1 按地址生成像素头像；接入 `image_url` 时走 Next.js `Image` 与后端代理。

## 8. 测试与验收

| 层 | 内容 |
| --- | --- |
| 单测（Vitest） | 格式化、URL 状态、指标、i18n 键一致、mock 契约测试（每个端点响应通过 zod 校验，翻页不重不漏、null 排序、410、导入不翻倍、403） |
| 浏览器冒烟（Playwright，生产构建） | 链/窗口/排序/筛选写入 URL 并刷新恢复；无限滚动与 410 恢复；503 重试；观察列表；中英文 Cookie；搜索；Token 详情（K 线、页签、覆盖范围、404）；管理后台全流程；390px 手机无横向溢出 |
| 门禁 | `npm run check`：format → lint → api:check → typecheck → test → build → smoke |

接真实后端后的验收见 [16 §5](16-m1-agent-tasks.md) 与 [17](17-m1-acceptance.md)。
