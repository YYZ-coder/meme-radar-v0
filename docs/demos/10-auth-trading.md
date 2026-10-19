# Demo 10：钱包登录与快捷交易

> 设计：[登录与交易](../engineering/15-auth-trading.md)、[快捷交易](../product/07-trading.md)。报价以样本A成交后的Pump曲线真实状态计算；nonce、会话等为演示值。**后端不接触私钥，不代签。**

## 1. 登录

### 1.1 EVM（SIWE，EIP-4361）

```text
meme-radar.example wants you to sign in with your Ethereum account:
0x2046d4ee2268e5583be790fde46095bdaf4eaec6

Sign in to Meme Radar.

URI: https://meme-radar.example
Version: 1
Chain ID: 8453
Nonce: 7f3c9a1e5b2d4f60
Issued At: 2026-10-16T13:50:00Z
Expiration Time: 2026-10-16T13:55:00Z
```

### 1.2 Solana（SIWS）

```text
meme-radar.example wants you to sign in with your Solana account:
7LMk5tKCGXgvweApmksAvfoAVAAXQWGnJdSKTn1bGLqg

Sign in to Meme Radar.

URI: https://meme-radar.example
Version: 1
Chain ID: mainnet
Nonce: 2b8e41d07c9a3f55
Issued At: 2026-10-16T13:50:00Z
Expiration Time: 2026-10-16T13:55:00Z
```

### 1.3 校验与异常

```mermaid
flowchart TD
    A["POST /auth/verify"] --> B{"domain与URI ＝ 本站？"}
    B -->|否| X1["401 domain_mismatch"]
    B -->|是| C{"nonce存在且未使用、未过期？"}
    C -->|否| X2["401 nonce_invalid（重放或超过5分钟）"]
    C -->|是| D{"签名校验"}
    D -->|EVM EOA：EIP-191恢复地址 ＝ 消息地址| E["通过"]
    D -->|EVM恢复失败| F{"地址有合约代码？"}
    F -->|是| G["EIP-1271 isValidSignature"]
    G -->|0x1626ba7e| E
    G -->|其他| X3["401 bad_signature"]
    F -->|否| X3
    D -->|Solana ed25519| E
    E --> H["消费nonce（同一事务）；找到或创建用户；生成随机token，哈希写auth_sessions；token写入Cookie"]
```

| 用例 | 期望 |
| --- | --- |
| 同一签名提交两次 | 第二次`nonce_invalid` |
| 修改消息中一个字符 | `bad_signature` |
| 6分钟后提交 | `nonce_invalid` |
| 钓鱼站转发消息（domain不同） | `domain_mismatch` |

## 2. 快捷交易：在Pump曲线买入0.1 SOL的`GX1J…pump`

### 2.1 报价

曲线状态（样本A成交后，真实）：虚拟SOL 99.279611809，虚拟Token 324,235,762.323533；费率95＋30bps，从链上费率程序读取。

| 项 | 计算 | 值 |
| --- | --- | --- |
| 支付 | 用户输入 | 0.1 SOL（$10.52） |
| 费用 | 0.1 − 0.1÷1.0125 | 0.0012346 SOL |
| 进入曲线 | 0.1÷1.0125 | 0.0987654 SOL |
| 预计到账 | 324,235,762.32 × 0.0987654 ÷ (99.279612＋0.0987654) | **322,235.94个** |
| 最小到账（滑点10%） | ×0.9 | 290,012.35个 |
| 价格影响 | 成交价 ÷ 现价 − 1 | 0.10% |
| 网络费 | 基础费＋P2优先费（演示） | 约0.000105 SOL |

曲线剩余可售44,335,762个，按当前状态大约再买入15.7 SOL就会耗尽并毕业。报价时如果买入额超过剩余，提示“超过曲线剩余，部分将无法成交”。

### 2.2 正常流程

```mermaid
sequenceDiagram
    participant F as 前端
    participant A as API
    participant W as 钱包
    participant C as Solana
    F->>A: POST /trade/quote {token: GX1J…, side: buy, amount: 0.1 SOL, slippage: 10%}
    A->>A: 读取曲线状态与费率；检查Token在支持列表、curve未complete
    A-->>F: quote_id=tq_51，预计322,235.94，最小290,012.35，30秒有效
    F->>A: POST /trade/build {quote_id, wallet: 7LMk…}
    A->>A: 接收地址＝当前钱包；构造buy指令＋最新blockhash
    A-->>F: 未签名交易（base64）
    F->>W: signAndSendTransaction
    W->>C: 广播
    W-->>F: signature
    F->>A: POST /trade/submitted {id, signature}
    loop 每2秒，最多90秒
        F->>A: GET /trade/{id}
        A->>C: getSignatureStatuses
    end
    A-->>F: confirmed，实际到账（解析器解出的数量）
```

### 2.3 异常兜底

```mermaid
flowchart TD
    A["交易流程中的异常"] --> B{"阶段"}
    B -->|报价| C{"情况"}
    C -->|曲线已complete（毕业）| C1["改走PumpSwap/Jupiter重新报价，提示“已上DEX”"]
    C -->|Token不在支持列表| C2["400，不报价"]
    C -->|滑点>50%| C3["400"]
    B -->|构造| D{"quote已过期（>30秒）？"}
    D -->|是| D1["410，前端自动重新报价并让用户再次确认"]
    B -->|签名| E{"用户拒签"}
    E --> E1["状态cancelled，不重试"]
    B -->|上链| F{"结果"}
    F -->|成功| F1["confirmed；按实际到账更新"]
    F -->|链上失败：滑点超限| F2["failed，显示“价格变化超过滑点”，网络费已扣"]
    F -->|余额不足（模拟时发现）| F3["构造阶段即拒绝，不让用户签名"]
    F -->|blockhash过期仍未上链| F4["failed（未上链，无费用）"]
    F -->|90秒仍查不到| F5["unknown：提示“请勿重复提交”，后台继续查询"]
```

| 情况 | 处理 |
| --- | --- |
| 钱包只能签名不能发送 | 前端把已签名交易提交到`/trade/{id}/broadcast`，后端只转发 |
| 用户连点两次 | 同一quote_id只能build一次；第二次返回已有交易 |
| BSC Four.meme卖出前需授权 | 先构造approve交易，确认后再构造卖出 |
| Base的Token走v4 hook池 | 报价由聚合器给出；hook额外收费（如[02](02-parsing.md)样本F的1.3%）计入预计到账 |
| 本应用发起的交易也被采集到 | 通过签名关联，统计只计一次 |

## 3. 验收用例

| 用例 | 期望 |
| --- | --- |
| 0.1 SOL报价 | 预计322,235.94个，最小290,012.35个（同一曲线状态） |
| 报价后31秒构造 | 410，重新报价 |
| 拒签 | cancelled |
| 构造用其他接收地址 | 400 |
| 90秒未确认 | unknown，不提示重试 |
