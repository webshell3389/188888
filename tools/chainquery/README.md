# chainquery — 多链余额/代币查询工具（Web + API）

取证场景的多链资产核查工具：输入助记词或地址，一次查询 13 条链的原生币 + EVM/SPL/TRC-20 代币 + USDT/USDC 稳定币，带美元估值与导出。

## 快速开始

```powershell
cd chainquery
python server.py                # 默认 http://127.0.0.1:8787
# 浏览器打开 http://127.0.0.1:8787
```

可选参数：`--host 0.0.0.0 --port 9000`

## 支持范围

| 类别 | 内容 |
|---|---|
| 原生币 | BTC、ETH、BNB(BSC)、Base、POL(Polygon)、Arbitrum、Optimism、AVAX、LTC、DOGE、BCH、TRX、SOL（双路径） |
| EVM 代币 | Blockscout（ETH/Base/Polygon/Arbitrum/Optimism） |
| 稳定币 | **USDT/USDC 直连合约 balanceOf**（ETH/BSC/Polygon/Arbitrum/Base 九层） |
| SOL 代币 | 全 SPL 账户扫描 |
| TRON 代币 | 全 TRC-20 扫描 |
| 估值 | Binance 实时价（原生币）；代币用 Blockscout 汇率 |

## API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/chains` | 支持链列表 |
| POST | `/api/derive` | `{mnemonics:[...]}` → 仅派生地址（毫秒级） |
| POST | `/api/query` | `{mnemonics:[], addresses:[], chains:[], tokens:true, prices:true}` → `{job:id}` |
| GET | `/api/job?id=` | 任务进度/结果 |

示例：

```bash
curl -X POST http://127.0.0.1:8787/api/query -H "Content-Type: application/json" \
     -d '{"mnemonics":["word1 ... word12"],"tokens":true,"prices":true}'
# -> {"job":"abc123..."}
curl "http://127.0.0.1:8787/api/job?id=abc123..."
```

## 稳定性设计（实战教训固化）

- 每条链 **多 RPC 回退**（如 ETH 3 个、Polygon 2 个），单个节点挂不影响结果
- **429 退避重试**（TRON/Tronscan 限流常见）
- 稳定币不依赖浏览器 API 列表，**直接合约 balanceOf** 核验
- 单链失败仅记录错误（`errors` 字段），整体继续
- 查询任务化（job）+ 轮询，长任务不阻塞

## TronGrid API Key

TRON 查询支持 TronGrid API Key（避免 429 限流，可全速扫描 TRC-20）：

- **Key 无效时自动回退匿名模式**（收到 401/403 自动重试无 Key），不影响可用性
- 更换 Key：启动前设置环境变量 `TRONGRID_API_KEY=你的key`（或改 `engine.py` 顶部 `TRONGRID_KEY`）
- 请求头：`TRON-PRO-API-KEY`；有效 Key 从 https://www.trongrid.io 控制台获取

## 依赖

- Python 3.8+；`cryptography`（ed25519，SOL 派生用）
- 其余纯标准库；`tw_keystore_tool.py`（keccak/AES/BIP32/BIP39 原语）随包附带

## 说明

- TON 链未包含（地址派生需额外实现）
- 地址自动识别：`0x…`→EVM 全链、`bc1…/1…/3…`→BTC、`ltc1…`→LTC、`T…`→TRON、`D…`→DOGE、base58 32-44 位→SOL
- 仅供授权取证/研究使用
