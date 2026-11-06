# iOS 钥匙串取证工具包（2026-10-03 更新）

> 适用对象：`pe_worker_v4` 导出的取证 JSON（keychain + sandbox）
> 本包内容：**4 个分析工具 + 2 个文档**。不含证据数据、不含任何分析结果。
> 运行环境：Python 3.8+，**纯标准库**（keccak / AES / scrypt / BIP32 / BIP39 均为自带实现）；
> `recover_all.py` 的 Tonkeeper 恢复需要 `pynacl`（`pip install pynacl`，可选）。

---

## 一、目录结构

```
forensics_toolkit_20260930/
├── README.md                     ← 本文件（使用引导）
├── docs/
│   └── FORENSICS_PLAYBOOK.md     ← 方法论 SOP（格式解剖/流水线/证实思路/陷阱）
└── tools/
    ├── smart_forensics.py        ★ 识别工具（画像/解析/keystore 自动解密）
    ├── recover_all.py            ★ 全合一恢复（9 类钱包助记词 + 口令候选引擎）
    ├── tw_keystore_tool.py       keystore 独立工具（被主工具复用）
    ├── extract_passwords.py      明文密码提取
    └── bip39_english.txt         BIP39 词表（主工具依赖，勿删）
```

---

## 二、快速开始

```powershell
# 1) 识别：包画像 + 条目解码 + 密钥识别 + keystore 自动解密 + 验证 + 报告
python tools/smart_forensics.py "<证据.json 或 目录>" --out smart_out

# 2) 全合一恢复：9 类钱包助记词一键恢复（自动收集候选口令 + 内置常见 PIN）
python tools/recover_all.py "<证据.json>" --pw 候选口令1 候选口令2 --out out
#    可选: --words 社工词表.txt   （每行一个口令）
#    可选: --deep-pin             （追加全量 0000-9999 四位 PIN，慢）

# 3) 明文密码提取（在证据所在目录运行，输出到当前目录）
python tools/extract_passwords.py

# 4) 单独处理 keystore（可选，主工具已覆盖）
python tools/tw_keystore_tool.py "<证据.json>"
```

**输出说明**：
- `smart_out/<包名>_smart.json` — 每包机器可读报告（含密钥材料、keystore 解密结果、验证状态）
- `plaintext_passwords.{txt,csv,md}` — 明文密码清单

---

## 三、主工具能力一览（smart_forensics.py）

| 能力 | 说明 |
|---|---|
| 包画像 | 设备 UUID / 时间 / 表规模 / 沙盒 / 品牌指纹（30+ 品牌库） |
| 全条目解码 | dataHex 智能解码：JSON / bplist / ASCII / BIN |
| 密钥识别 | 明文助记词、熵（JSON/裸hex）、扩展私钥、ed25519、hex seed、keystore、保险库 |
| 熵→助记词 | 自动推导（Phantom 系 JSON entropy、Bitpie 系裸 hex） |
| keystore 解密 | 自动匹配钥匙串内密码 → scrypt/PBKDF2 → AES-CTR 解密 → MAC 校验 |
| 证实 | BIP39 checksum + PBKDF2 seed 比对 + 地址派生交叉核对（MATCH 判定） |
| 报告 | 每包一份 JSON（含建议与状态） |

---

## 四、全合一恢复（recover_all.py）

**覆盖 9 类钱包**（源码级/标准方案，全部带验证链）：

| 钱包 | 方案 | 口令 |
|---|---|---|
| Phantom | 钥匙串熵 → 助记词（+seed 缓存核对） | 无需 |
| Uniswap | 钥匙串明文 + zprv 同源核对 | 无需 |
| Bitpie | seedPhraseEntropy 熵推导（+userHDseed 核对） | 无需 |
| Exodus | 钥匙串明文 | 无需 |
| Coin98 | WALLET_SECURE_BACKUP 明文（+TRON 地址核对） | 无需 |
| Trust Wallet / **Bybit Web3** | keystore v3（沙盒文件或**钥匙串内嵌 JSON**；scrypt/PBKDF2+AES-CTR+keccak MAC） | 自动（包内口令优先） |
| imToken | keystore v3（PBKDF2-SHA256 262144+AES-CTR+MAC） | 候选扫描 |
| MetaMask | quick-crypto（PBKDF2-SHA512+AES-256-CBC/PKCS7） | 候选扫描 |
| Tonkeeper V1 | scrypt(N=16384,r=8,p=1,dkLen=32)+tweetnacl（nonce=salt[:24]） | 候选扫描（常见 4 位 PIN） |

**口令候选引擎**（自动合并去重）：
1. `--pw` 显式候选；2. 包内明文口令（网站口令 / keystore 密码 / JSON password 字段）；
3. 内置常见 PIN（4/6/8 位 + 数字加 `.`/`!` 变体）；4. `--words` 词表；5. `--deep-pin` 全量四位。

**输出**：控制台汇总 + `<包名>_allin1.json` + `<包名>_allin1.md`（含每个钱包的验证链与命中口令）。

### 4.1 批量模式（2026-11 完善）

`recover_all.py` 现在可以直接吃目录，一次跑完全部包：

```bash
python tools/recover_all.py /path/to/war_dir --out out --resume
# 可选: --pattern "2026110*.json"  --limit 100
# Tonkeeper V1 全量 4 位 PIN（仅作用于 Tonkeeper，不会拖慢其他 keystore）:
python tools/recover_all.py /path/to/war_dir --out out --resume --deep-pin
```

- 输出 `out/SUMMARY.md`、`out/summary.json`、`out/wallets.csv`（按唯一助记词聚合）
- `--resume` 跳过已有 `<包名>_allin1.json`，可断点续跑
- 8MB 截断包自动抢救 keychain + 完整 sandbox 条目
- Tonkeeper 兼容 `mnemonics_vault_*` 与 `wallets_chunk_*` 两种形态，一个保险库多组助记词全部返回
- 新增 JSON 字段扫描：`mnemonic/seedPhrase/privateKey`（Exodus 等）
- `--deep-pin` 只对 Tonkeeper V1 追加 0000-9999，其他 keystore 仍只用基础候选
- Python 3.6 无 `hashlib.scrypt` 时自动回退 `cryptography` / 纯 Python

> 已知局限：Tonkeeper V2（`v2mnemonics_*`，Argon2id+AES-GCM）仅检测标注；
> Solflare / Bitget / HashKey 为钥匙串/自研方案（非口令派生），不在本工具范围。

---

## 五、纪律与建议

1. **单证据包口径**：结论只用包内数据，逐包独立验证；跨包关联需另立课题。
2. **提取与口令扫描分离**：默认只做明文提取 / 自动解密；口令候选扫描需明确给出候选（`--pw`/`--words`）才启用。
3. **hex 感知**：所有值以 dataHex 存储，搜敏感词用 hex 编码形态（如 `pin_code`→`70696e5f636f6465`）。
4. **结论附验证链**：熵→词→seed 比对 / keystore MAC / 地址派生 MATCH / 阳性对照。
5. 详细方法论、陷阱清单、复现命令 → 见 `docs/FORENSICS_PLAYBOOK.md`。
6. **后端对接（单 worker 队列 / 资源限制 / JSON schema / Go 示例）→ 见 `docs/WAR_INTEL_BACKEND.md`。**
