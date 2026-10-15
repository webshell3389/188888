# iOS 钥匙串取证：分析与证实方法论（SOP）

> 版本: 2026-09-30 | 适用: pe_worker_v4 导出 JSON（keychain + sandbox）
> 用途: 固化「分析流水线 + 证实思路 + 工具链 + 经验教训」，后续新证据**无需从头开始**。
> 纪律: **单证据包口径**（逐包独立验证，不做跨包串联）；提取/解密与"爆破"分离（默认不爆破）。
> 说明: 本手册为通用方法论版，不含任何具体案例结果。

---

## 一、证据包格式解剖

```
{
  "device_uuid": "XXXXXXXX-....",          // 设备唯一标识
  "keychain": {
    "timestamp": "...", "source": "pe_worker_v4",
    "securityd_pid": N, "diagnostics": {...},
    "metadataKeys": {...},
    "tables": {                             // 原始表
      "genp": {"count": N, "items": [ 条目 ]},
      "inet": {...}, "keys": {...}, "cert": {...}
    },
    "wallets": {                            // 工具的分组副本（部分导出才有）
      "trustwallet": {"count": N, "items": [...]},
      "phantom": {...}, "bitpie": {...}, ...
    }
  },
  "sandbox": {                              // App 容器文件（base64）
    "trust_wallet": { "Documents/keystore/UTC--...": "<base64 JSON>" }
  }
}
```

**条目（item）结构**：
```json
{"rowid":N, "_table":"genp|inet|keys|cert", "version":8, "protectionClass":N,
 "_blobSizes":{...}, "service":"...|null", "account":"...|null",
 "accessGroup":"<TeamID>.<bundle-id>", "label":"...|null",
 "_l3via":"sfkeys",            // ← 注意：可能插在 label 与 dataHex 之间！
 "dataHex":"<hex>",            // ← 值统一以 hex 存储（需解码）
 "layer3Error":"aks_unwrap kr=..." }   // 部分条目解封失败（无 dataHex）
```

**三条铁律（踩过的坑）**：
1. **值都在 dataHex 里**：字面搜索会漏 → 搜索敏感词时用 **hex 编码形态**（如 `pin_code`→`70696e5f636f6465`）。
2. **字段间可能有额外字段**（`_l3via`/`layer3Error`）→ 正则要容错（label 与 dataHex 之间留 `.{0,300}?`）。
3. **BOM**：PowerShell 写文件带 BOM；Python 读用 `utf-8-sig`，Node 需 `.replace(/^\uFEFF/,'')`。

---

## 二、分析流水线（10 步 SOP）

### Step 0 包画像（30 秒）
- 工具：`smart_forensics.py`（自动）
- 产出：设备 UUID / 时间 / 表规模 / 沙盒 / 品牌指纹（30+ 品牌库）
- 命令：`python smart_forensics.py <file|dir> [--pattern "2026092*.json"] [--out smart_out]`

### Step 1 全条目解码
- 遍历 `keychain.tables.*` + `keychain.wallets.*` 全部条目
- dataHex 智能解码：JSON / bplist / ASCII / BIN（可打印率 ≥85% 判 ASCII）

### Step 2 密钥材料识别（高价值优先）
| 类型 | 判据 | 处理 |
|---|---|---|
| 明文助记词 | 全部词 ∈ BIP39 词表且 12/15/18/21/24 词 | BIP39 校验 + 地址交叉核对 |
| 熵（JSON） | JSON 含 `entropy` 字典（16/24/32B） | **直接推导助记词**（Phantom 系） |
| 熵（裸 hex） | 账户/服务名含 entropy/mnemonic/seedphrase 且值=16/24/32B hex | **直接推导助记词**（Bitpie 系） |
| 扩展私钥 | `zprv/xprv/yprv/tprv/...` base58check | 解码 + 与助记词派生值比对 |
| ed25519 | `ed25519:<base58>` | base58 解码 → seed+pubkey |
| hex64/hex128 | 32B/64B hex 串 | 作为 BIP39 seed 交叉核对候选 |
| keystore v3 | JSON 含 `crypto.cipher/kdf/mac` | 走 Step 3 解密 |
| 保险库 | `encrypted-scrypt-tweetnacl` / MetaMask `cipher` | 标注为加密（需 passcode/密码） |

### Step 3 keystore 自动解密（密码在钥匙串）
- 沙盒定位：`sandbox.<app>.Documents/keystore/UTC--*`（base64 → JSON）
- **密码匹配逻辑**：钥匙串内 `account == "<app>UTC--<文件名>"` 或 `== "<文件名>"` 的条目，其 dataHex 解码（ASCII、8~128 字符）即密码
- 解密：`scrypt/PBKDF2` → `keccak256(dk[16:32] + ct) == mac` 校验 → `AES-128-CTR` 解密
- 常见参数：Trust N=16384,p=4,r=8；Bybit N=4096,p=4,r=8（以包内 kdfparams 为准）
- 成功即输出明文（助记词），随后走 Step 5 验证

### Step 4 明文密码全量提取（不爆破）
- 网站口令：`accessGroup` 含 `cfnetwork` + 值可打印（label 含 `@` 或 Password/Backup/Wallet）
- App PIN：账户/服务名含 `pin_code|pincode|pin_key|PINCODEKEY`；JSON 值含 `"pin_code"`
- 应用内凭据：JSON 值含 `password|passwd|passphrase` 字段
- 工具：`extract_passwords.py` → `plaintext_passwords.{txt,csv,md}`

### Step 5 证实（密码学校验链）—— 核心
| 场景 | 校验方法 |
|---|---|
| 助记词（有缓存 seed） | `PBKDF2(mnemonic,"mnemonic",2048,64) == 缓存 seed`（64B 全等） |
| 助记词（有地址线索） | 派生 `m/44'/60'/0'/0/0` → 与条目地址/名称比对（MATCH） |
| 助记词（有 hex seed） | `PBKDF2(mnemonic) == 包内 hex128/hex64` 条目 |
| keystore | `keccak256(dk[16:32]+ct) == mac` + BIP39 校验 + 地址对账 |
| 扩展私钥 | 解码私钥 == 助记词同路径派生私钥 |
| BIP39 词表校验 | checksum 重算（熵→词→校验位） |
| 余额查询 | 阳性对照：创世地址 `1A1zP1eP…`（约 26.99 BCH）验证查询管道 |

### Step 6 派生与资产核查（单包口径）
- 每包独立派生（BTC m/84'、ETH m/44'/60'、SOL 双路径、LTC/DOGE/BCH/TRON/TON/ADA/ATOM…）
- 余额查询源：mempool.space / publicnode / bsc-dataseed / Solana RPC / trongrid / toncenter / xrplcluster / blockcypher / koios / **Electrum SSL 直连（BCH 兜底）**
- 输出：逐包余额清单（单包口径）

### Step 7 口令字典（挂起能力，不默认执行）
- 画像提取：人名/生日/家属名/手机号/邮箱/兴趣（来自证据）
- 规则：基词 × 后缀(`!@#$/年份/786`) × 大小写 × l33t + 日期格式 + **包内 hex 串**
- 爆破器为独立脚本（本工具包不含），分级执行（字典→PIN 长度递增），用户明确指令后才启用

### Step 8 报告（单包口径）
- 每包：专项证据 `.md` + `smart_out/*_smart.json`（机器可读）
- 汇总：总报告 + 逐包验证比对

### Step 9 归档
- 证据/脚本/报告同目录；所有结论附**复现命令**

---

## 三、工具说明（本工具包内）

| 脚本 | 用途 | 用法 |
|---|---|---|
| `smart_forensics.py` | **主工具**：包画像/条目解析/密钥识别/keystore 自动解密/验证/报告 | `python smart_forensics.py <file\|dir> [--out smart_out]` |
| `tw_keystore_tool.py` | keystore 独立工具（纯 Python 原语库：keccak/AES/scrypt/BIP32/地址，被主工具复用） | `python tw_keystore_tool.py <export.json>` |
| `extract_passwords.py` | 明文密码全量提取 | `python extract_passwords.py` |
| `recover_all.py` | **全合一恢复**：9 类钱包助记词 + 口令候选引擎（内置常见 PIN） | `python recover_all.py <证据.json> --pw 候选… --out out` |
| `bip39_english.txt` | BIP39 词表（主工具依赖） | — |

> 三个脚本均为**纯标准库**实现，无第三方依赖；在 Windows PowerShell 与 Linux 下均可运行。

---

## 四、陷阱与规则（经验教训）

1. **搜索必须 hex 感知**：`pin_code` 等值以 hex 存储；字面搜索为空 ≠ 不存在。
2. **正则要容错 `_l3via`**：label 与 dataHex 之间可能有额外字段（曾导致整包漏提）。
3. **`wallets` 分组只是副本**：完整数据以 `tables` 为准；分组可能为空。
4. **假阳性要排除**：`MCCryptoDictionary`=Amazon、`trustkit`=TLS pinning、`chainlogin`=TikTok、`ledger`=Deezer、`jupiter`≠`money.jupiter`（前者 Solana DEX，后者印度网银）。
5. **不跨包串联**：同主/集群推断需另立课题并明确证据链依赖。
6. **解密失败是正常设计**：Tonkeeper（passcode）、MetaMask（钱包密码）、MEXC（Secure Enclave）——数据中无密钥时不要强求。
7. **BCH 兜底通道**：公共 API 全挂时用 Electrum SSL 直连（`bch.imaginary.cash:50002`，自算 scripthash）。
8. **余额查询要阳性对照**：防止"全 0"是脚本错误（创世地址验证管道）。

---

## 五、钱包解密方案库（源码级，2026-10-03）

| 钱包 | 方案 | 关键参数 | 备注 |
|---|---|---|---|
| Trust Wallet | keystore v3 | scrypt → AES-128-CTR + keccak MAC | 密码常可在钥匙串内直接找到 |
| Bybit Web3 | keystore v3 | 同 Trust（**钥匙串内嵌 keystore JSON**，非沙盒） | 密码常为包内网站口令（复用） |
| imToken | keystore v3 | PBKDF2-SHA256 c=262144 → AES-128-CTR + MAC | 明文即助记词 |
| MetaMask (mobile) | quick-crypto | **PBKDF2-HMAC-SHA512**（iters=keyMetadata，默认 5000，32B）→ **AES-256-CBC**(PKCS7)；salt 用 **base64 字符串字节** | 浏览器版是 SHA256+AES-GCM，勿混淆 |
| Tonkeeper V1 | encrypted-scrypt-tweetnacl | scrypt(N=16384,r=8,p=1,**dkLen=32**) → secretbox；**nonce=salt[:24]**；salt/ct 为 hex；box-only | 口令常为 **4 位 PIN** |
| Tonkeeper V2 | `v2mnemonics_*` | Argon2id(libsodium Interactive/Moderate) + AES-256-GCM（版本字节 2） | 需 argon2 库 |
| Phantom | 钥匙串熵 | `vault.seed` 的 entropy → 助记词；`cache.seed` 64B 核对 | 无需口令 |
| Uniswap | 明文 + zprv | 钥匙串明文助记词；zprv 私钥 == m/44'/60'/0'/0/0 派生 | 无需口令 |
| Bitpie | 熵 | seedPhraseEntropy(hex) → 助记词；userHDseed(64B) 核对 | 无需口令 |
| Exodus | 明文 | 钥匙串 JSON 内 mnemonic | 无需口令 |
| Coin98 | 明文备份 | WALLET_SECURE_BACKUP（mnemonic+privateKey，TRON 地址核对） | 无需口令 |
| Solflare | 自研加密 | biometric_storage 插件仅存钥匙串（不加密），密文为 app 自研 | 非口令派生，需逆向 |
| Bitget (BitKeep) | SE 密钥引用 | pin/mnemonic_key_uuid + kPKey 包裹 | 非纯口令 |
| HashKey | 信封 | AES-256-GCM + dek.v1（再包裹） | 需逆向 |

> 判据：**密文里有没有 KDF 参数（salt/iterations/N/r/p）** —— 有 = 口令型（候选扫描有意义）；
> 没有 = 钥匙串/硬件密钥型（口令再对也解不开，需逆向或拿到 Keychain 密钥）。

---

## 六、复现速查（新证据来了怎么跑）

```powershell
# 1) 主工具：画像 + 密钥 + keystore 解密 + 验证 + 报告
python tools/smart_forensics.py "<新证据.json>" --out smart_out

# 2) 明文密码提取（在证据目录运行）
python tools/extract_passwords.py

# 3) 若发现 keystore/保险库 → 看 smart 报告里的 [keystore]/[保险库] 行
#    需要时单独跑：python tools/tw_keystore_tool.py "<新证据.json>"

# 4) 新助记词 → 按 Step 5 校验链验证后再做资产核查（单包口径）
```
