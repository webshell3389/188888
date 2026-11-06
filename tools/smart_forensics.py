#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""smart_forensics.py — iOS 钥匙串取证导出「智能识别 · 读取 · 解析」工具

功能:
  1) 包识别     : 设备 UUID / 时间 / 规模 / 沙盒 / 钱包品牌指纹（自动）
  2) 全条目解析 : 遍历 genp/inet/keys/cert + wallets 分组，解码 dataHex，智能类型识别
                  (JSON / bplist / ASCII / 二进制)，识别助记词 / 私钥 / 令牌 / 邮箱 / 口令
  3) keystore   : 沙盒 Documents/keystore/* 自动提取 + 自动解密
                  (scrypt/PBKDF2 + AES-CTR + keccak256 MAC；密码自动取自钥匙串)
  4) 保险库识别 : scrypt-tweetnacl (Tonkeeper 系) / MetaMask vault / flutter_secure_storage
  5) 助记词验证 : BIP39 checksum +（上下文含 0x 地址时）派生地址交叉核对
  6) 报告输出   : 控制台 + <name>_smart.json + <name>_smart.md

用法:
  python smart_forensics.py <导出JSON | 目录> [--out DIR] [--decrypt] [--max-items N]
"""
import argparse, base64, hashlib, json, os, re, sys, unicodedata

# ---- 复用 tw_keystore_tool 的纯 Python 原语（keccak/AES/scrypt/BIP32/地址） ----
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import tw_keystore_tool as tw

# ------------------------------------------------------------------ 品牌指纹库
BRANDS = {
    # 自托管钱包
    "Trust Wallet": r"sixdays\.trust|trustwallet",
    "Phantom": r"app\.phantom|74UR4AUZ34",
    "MetaMask": r"metamask",
    "Tonkeeper": r"jbig\.tonkeeper|tonkeeper",
    "Bitpie": r"bitpie",
    "Uniswap": r"uniswap",
    "SafePal": r"safepal",
    "imToken": r"imtoken",
    "TokenPocket": r"tokenpocket",
    "OneKey": r"onekey",
    "Exodus": r"exodus",
    "Solflare": r"solflare",
    "Rainbow": r"rainbow\.me",
    "Zerion": r"zerion",
    "Coinbase Wallet": r"bakkenbaeck|coinbase\.wallet",
    "Brave Wallet": r"brave\.ios|Brave Safe Storage",
    "dAppWallet": r"dappwallet|wallet\.mk",
    "Maps.me n2": r"n2wallet|n2-wallet|mapsme",
    # 交易所 / CEX
    "Binance": r"czzhao\.binance|com\.binance|binanceAuthenticator",
    "OKX": r"okex|okx",
    "Coinbase": r"vilcsak\.bitcoin2|coinbase",
    "Bitget": r"bitget",
    "Nexo": r"nexobank",
    "Bybit": r"bybit",
    "KuCoin": r"kucoin",
    "MEXC": r"mexcglobal",
    "BitMart": r"bitmart",
    "CoinEx": r"coinex",
    "Gate.io": r"gate\.io|gateio",
    "eToro": r"etoro",
    "Robinhood": r"robinhood",
    "Webull": r"webull",
    "Stake": r"stake\.mobile",
    "Cash App (Block)": r"squareup\.cash",
    # 区块链应用
    "Sweat Economy": r"sweateconomy",
    "Pi Network": r"pi\.wallet|pi\.browser|socialchain",
    "Jupiter": r"jup\.jupiter|money\.jupiter",
    "DexScreener": r"dexscreener",
    "WalletConnect SDK": r"walletconnect",
    "CoinMarketCap": r"coinmarketcap",
    "Ledger": r"com\.ledger",
    "Trezor": r"trezor",
}
BRAND_RE = {k: re.compile(v, re.I) for k, v in BRANDS.items()}

WORDLIST = None
def load_wordlist():
    global WORDLIST
    if WORDLIST is None:
        for cand in (os.path.join(os.path.dirname(os.path.abspath(__file__)), "bip39_english.txt"),):
            if os.path.exists(cand):
                WORDLIST = open(cand, encoding="utf-8").read().split()
                break
        if WORDLIST is None:
            WORDLIST = []
    return WORDLIST

# ------------------------------------------------------------------ 基础工具
def decode_hex_smart(hexs):
    """dataHex -> (bytes, kind, text)"""
    if not hexs:
        return b"", "empty", ""
    try:
        b = bytes.fromhex(hexs)
    except Exception:
        return b"", "badhex", ""
    if b.startswith(b"bplist00"):
        return b, "bplist", printable_strings(b, 4)
    if b[:1] in (b"{", b"[") or b[:4] in (b"true", b"null"):
        try:
            j = json.loads(b.decode("utf-8"))
            return b, "json", json.dumps(j, ensure_ascii=False)
        except Exception:
            pass
    printable = sum(1 for x in b if 32 <= x <= 126)
    if b and printable / len(b) >= 0.9:
        return b, "ascii", b.decode("utf-8", "replace")
    return b, "binary", ""

def printable_strings(b, minlen=4):
    out, cur = [], []
    for x in b:
        if 32 <= x <= 126:
            cur.append(chr(x))
        else:
            if len(cur) >= minlen: out.append("".join(cur))
            cur = []
    if len(cur) >= minlen: out.append("".join(cur))
    return " | ".join(out[:30])

SECRET_PATTERNS = [
    ("jwt", re.compile(r"^eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]*$")),
    ("hex128", re.compile(r"^[0-9a-fA-F]{128}$")),
    ("hex64", re.compile(r"^[0-9a-fA-F]{64}$")),
    ("hex32", re.compile(r"^[0-9a-fA-F]{32}$")),
    ("email", re.compile(r"[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}")),
    ("eth_addr", re.compile(r"^0x[0-9a-fA-F]{40}$")),
    ("uuid", re.compile(r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")),
    ("b58_ext_key", re.compile(r"^(zprv|xprv|yprv|tprv|zpub|xpub|ypub|tpub|dgub|dprv|Ltub|Ltpv)[1-9A-HJ-NP-Za-km-z]{50,}$")),
    ("ed25519", re.compile(r"^ed25519:[1-9A-HJ-NP-Za-km-z]+$")),
    ("wif", re.compile(r"^[5KL][1-9A-HJ-NP-Za-km-z]{50}$")),
    ("near_account", re.compile(r"^[0-9a-f]{64}$")),
]

# 常见 PIN / 弱口令（Tonkeeper V1 实测 4 位 PIN 最多）
BUILTIN_PINS = [
    "8888", "0000", "1111", "2222", "3333", "4444", "5555", "6666", "7777", "9999",
    "1234", "4321", "1122", "1212", "2121", "2580", "0852", "1998", "1999", "2000",
    "2024", "2025", "2026", "1004", "6668", "8880",
    "888888", "000000", "111111", "222222", "333333", "444444", "555555", "666666",
    "777777", "999999", "123456", "654321", "112233", "121212", "123123",
    "88888888", "00000000", "12345678", "11111111", "password", "Password", "qwerty",
    "abc123", "123456789", "1234567890", "admin", "root",
]


def looks_like_mnemonic(text):
    wl = set(load_wordlist())
    if not wl: return False
    words = text.split()
    if len(words) not in (12, 15, 18, 21, 24): return False
    return all(w in wl for w in words)

def mnemonic_from_entropy(ent: bytes):
    wl = load_wordlist()
    h = hashlib.sha256(ent).digest()
    bits = "".join(format(b, "08b") for b in ent) + format(h[0], "08b")[:len(ent) * 8 // 32]
    return " ".join(wl[int(bits[i:i + 11], 2)] for i in range(0, len(bits), 11))

def bip39_checksum_ok(mn):
    wl = load_wordlist()
    if not wl: return None
    idx = {w: i for i, w in enumerate(wl)}
    try:
        bits = "".join(format(idx[w], "011b") for w in mn.split())
    except KeyError:
        return False
    total = len(bits); ent_bits = total * 32 // 33
    ent = int(bits[:ent_bits], 2).to_bytes(ent_bits // 8, "big")
    cs = int(bits[ent_bits:], 2)
    expect = hashlib.sha256(ent).digest()[0] >> (8 - (total - ent_bits))
    return cs == expect

def try_address_crosscheck(mn, context_text):
    """上下文含 0x 地址时，派生 m/44'/60'/0'/0/0 交叉核对"""
    m = re.search(r"0x[0-9a-fA-F]{40}", context_text or "")
    if not m: return None
    target = m.group(0).lower()
    try:
        seed = tw.mnemonic_to_seed(mn)
        for path in ("m/44'/60'/0'/0/0", "m/44'/60'/0'/0"):
            a = tw.eth_address(tw.derive_path(seed, tw.parse_path(path)))[0]
            if a.lower() == target:
                return f"MATCH {path} -> {a}"
        derived = tw.eth_address(tw.derive_path(seed, tw.parse_path("m/44'/60'/0'/0/0")))[0]
        return f"NO-MATCH (derived m/44'/60'/0'/0/0 = {derived})"
    except Exception as e:
        return f"derive-error {e}"

# ------------------------------------------------------------------ 读取导出
def normalize_export(obj):
    """统一导出形态。

    pe_worker_v4 的 dump 是扁平的（tables / wallets / metadataKeys 在顶层），
    而早期导出是 {"keychain": {...}} 或 {"big": {...}} 包裹的。这里把扁平格式
    包成 {"keychain": ...}，老格式原样返回，保证下游 iter_items/profile 一套逻辑通吃。
    """
    if not isinstance(obj, dict):
        return obj
    if isinstance(obj.get("keychain"), dict) or isinstance(obj.get("big"), dict):
        return obj
    if any(isinstance(obj.get(k), (dict, list)) for k in ("tables", "wallets", "sandbox", "metadataKeys")):
        return {"keychain": obj}
    return obj


def _find_json_object(text, key):
    """从截断 JSON 中按大括号配对提取 key 对应的对象文本。"""
    m = re.search(r'"%s"\s*:\s*\{' % re.escape(key), text)
    if not m:
        return None
    i = m.end() - 1
    depth = 0
    instr = esc = False
    for j in range(i, len(text)):
        c = text[j]
        if instr:
            if esc:
                esc = False
            elif c == "\\":
                esc = True
            elif c == '"':
                instr = False
        else:
            if c == '"':
                instr = True
            elif c == "{":
                depth += 1
            elif c == "}":
                depth -= 1
                if depth == 0:
                    return text[i:j + 1]
    return None


def _read_json_string(s, i):
    j = i + 1
    out = []
    while j < len(s):
        c = s[j]
        if c == "\\":
            out.append(s[j:j + 2])
            j += 2
            continue
        if c == '"':
            return "".join(out), j + 1, True
        out.append(c)
        j += 1
    return "".join(out), j, False


def _skip_sep(s, i):
    while i < len(s) and s[i] in " \t\r\n,":
        i += 1
    return i


def _salvage_sandbox(text):
    """从截断 JSON 抢救 sandbox：只收完整 base64 文件条目。"""
    m = re.search(r'"sandbox"\s*:\s*\{', text)
    if not m:
        return {}
    s = text
    i = m.end() - 1 + 1
    out = {}
    while True:
        i = _skip_sep(s, i)
        if i >= len(s) or s[i] == "}":
            break
        if s[i] != '"':
            break
        app, i, ok = _read_json_string(s, i)
        if not ok:
            break
        i = _skip_sep(s, i)
        if i >= len(s) or s[i] != ":":
            break
        i = _skip_sep(s, i + 1)
        if i >= len(s) or s[i] != "{":
            break
        i += 1
        files = {}
        complete = True
        while True:
            i = _skip_sep(s, i)
            if i >= len(s):
                complete = False
                break
            if s[i] == "}":
                i += 1
                break
            if s[i] != '"':
                complete = False
                break
            fn, i, ok = _read_json_string(s, i)
            if not ok:
                complete = False
                break
            i = _skip_sep(s, i)
            if i >= len(s) or s[i] != ":":
                complete = False
                break
            i = _skip_sep(s, i + 1)
            if i >= len(s) or s[i] != '"':
                complete = False
                break
            val, i, ok = _read_json_string(s, i)
            if not ok:
                complete = False
                break
            files[fn] = val
        if files:
            out[app] = files
        if not complete:
            break
    return out


def salvage_export(raw):
    """抢救 8MB 截断包：keychain 完整保留 + sandbox 完整文件条目。"""
    kc_txt = _find_json_object(raw, "keychain")
    if not kc_txt:
        return None
    try:
        kc = json.loads(kc_txt)
    except Exception:
        return None
    m = re.search(r'"device_uuid"\s*:\s*"([^"]+)"', raw)
    dev = m.group(1) if m else "?"
    sb = _salvage_sandbox(raw)
    return {"device_uuid": dev, "keychain": kc, "sandbox": sb,
            "_salvaged": True, "_salvage_note": "JSON 截断，已抢救 keychain + 完整 sandbox 条目"}


def load_export(path):
    raw = open(path, encoding="utf-8", errors="replace").read()
    raw = raw.lstrip("\ufeff")
    try:
        return normalize_export(json.loads(raw)), raw
    except json.JSONDecodeError:
        # 1) 8MB 截断包：抢救 keychain/sandbox
        obj = salvage_export(raw)
        if obj is not None:
            return normalize_export(obj), raw
        # 2) 修复截断导出（老格式片段）
        try:
            return normalize_export(json.loads(tw.REPAIR_PREFIX + raw)), raw
        except Exception:
            return None, raw

def iter_items(obj):
    """遍历所有条目: yield (section, table_name, item)"""
    if not isinstance(obj, dict): return
    for top in ("keychain", "big"):
        t = obj.get(top)
        if not isinstance(t, dict): continue
        for sect in ("wallets", "tables"):
            sv = t.get(sect)
            if not isinstance(sv, dict): continue
            for name, sub in sv.items():
                if isinstance(sub, dict) and isinstance(sub.get("items"), list):
                    for it in sub["items"]:
                        yield (f"{top}.{sect}", name, it)

def profile(obj):
    kc = (obj or {}).get("keychain") or {}
    dev = (obj or {}).get("device_uuid") or kc.get("device_uuid") or "?"
    meta = {"device_uuid": dev, "timestamp": kc.get("timestamp"), "source": kc.get("source")}
    tables = {}
    for sect, name, _ in iter_items(obj):
        tables[name] = tables.get(name, 0) + 1
    meta["tables"] = tables
    # 沙盒
    sb = (obj or {}).get("sandbox") or {}
    meta["sandbox_apps"] = list(sb.keys())
    ks_files = []
    for app, files in sb.items():
        if isinstance(files, dict):
            for fp in files:
                if "/keystore/UTC" in fp:
                    ks_files.append((app, fp))
    meta["keystore_files"] = ks_files
    return meta

def brand_fingerprint(obj, raw):
    found = {}
    for name, rx in BRAND_RE.items():
        n = len(rx.findall(raw))
        if n: found[name] = n
    return found

def _json_secret_fields(j):
    """递归找 JSON 里的 mnemonic/seedPhrase/privateKey 字段。"""
    out = []
    MN_KEYS = ("mnemonic", "seedphrase", "seed_phrase", "recoveryphrase", "recovery_phrase",
               "backupphrase", "backup_phrase", "phrase", "secretphrase", "secret_phrase")
    PK_KEYS = ("privatekey", "private_key", "secretkey", "secret_key")

    def walk(o, depth=0):
        if depth > 3:
            return
        if isinstance(o, dict):
            for k, v in o.items():
                kl = str(k).lower().replace("-", "_")
                if isinstance(v, str):
                    if any(x in kl for x in MN_KEYS) and looks_like_mnemonic(v):
                        out.append(("mnemonic(json)", v.strip()))
                    elif any(x in kl for x in PK_KEYS) and re.fullmatch(r"[0-9a-fA-F]{64}", v):
                        out.append(("privatekey(json)", v))
                elif isinstance(v, (dict, list)):
                    walk(v, depth + 1)
        elif isinstance(o, list):
            for x in o:
                walk(x, depth + 1)

    walk(j)
    return out


# ------------------------------------------------------------------ 条目解析
def analyze_items(obj, raw, max_items=100000):
    result = {"secrets": [], "emails": set(), "passwords": [], "vaults": [], "keystores": [], "interesting": []}
    n = 0
    for sect, tname, it in iter_items(obj):
        n += 1
        if n > max_items: break
        svc = str(it.get("service") or "")
        acct = str(it.get("account") or "")
        ag = str(it.get("accessGroup") or "")
        hexs = it.get("dataHex") or ""
        ctx = f"{svc} {acct} {ag}"
        b, kind, text = decode_hex_smart(hexs)
        rec = {"sect": sect, "table": tname, "rowid": it.get("rowid"), "svc": svc, "acct": acct, "ag": ag, "kind": kind}
        # 品牌相关?
        hit_brands = [bn for bn, rx in BRAND_RE.items() if rx.search(ctx)]
        if hit_brands:
            rec["brands"] = hit_brands
        # 助记词
        if text and looks_like_mnemonic(text):
            chk = bip39_checksum_ok(text)
            xc = try_address_crosscheck(text, ctx)
            result["secrets"].append({"type": "mnemonic", "value": text, "bip39": chk, "crosscheck": xc, **rec})
            continue
        # 智能: JSON 内含 entropy -> 直接推导助记词（Phantom 系）
        if kind == "json" and '"entropy"' in text:
            try:
                j = json.loads(b.decode("utf-8"))
                ent = j.get("entropy")
                if isinstance(ent, dict) and len(ent) in (16, 24, 32):
                    ent_bytes = bytes(int(ent[str(i)]) for i in range(len(ent)))
                    mn = mnemonic_from_entropy(ent_bytes)
                    result["secrets"].append({"type": "mnemonic(entropy-derived)", "value": mn,
                                              "bip39": bip39_checksum_ok(mn), "crosscheck": None, **rec})
                    continue
            except Exception:
                pass
        # 智能: 任意 JSON 里的 mnemonic/seedPhrase/privateKey 字段（Exodus 等）
        if kind == "json":
            try:
                j = json.loads(b.decode("utf-8"))
            except Exception:
                j = None
            found_mn = False
            for stype, val in _json_secret_fields(j):
                if stype.startswith("mnemonic"):
                    result["secrets"].append({"type": stype, "value": val,
                                              "bip39": bip39_checksum_ok(val),
                                              "crosscheck": try_address_crosscheck(val, ctx), **rec})
                    found_mn = True
                else:
                    result["secrets"].append({"type": stype, "value": val, **rec})
            if found_mn:
                continue
        # 智能: 账户/服务名含 entropy/mnemonic/seedphrase 且值为裸 hex（16/24/32B）-> 推导助记词（Bitpie 系）
        if kind == "ascii" and re.search(r"(?i)entropy|mnemonic|seedphrase", ctx):
            t = text.strip()
            if re.fullmatch(r"(?:[0-9a-fA-F]{2}){16}|(?:[0-9a-fA-F]{2}){24}|(?:[0-9a-fA-F]{2}){32}", t):
                try:
                    mn = mnemonic_from_entropy(bytes.fromhex(t))
                    result["secrets"].append({"type": "mnemonic(entropy-hex-derived)", "value": mn,
                                              "bip39": bip39_checksum_ok(mn), "crosscheck": None, **rec})
                    continue
                except Exception:
                    pass
        # 其他密钥形态
        if text:
            for sname, rx in SECRET_PATTERNS:
                if rx.match(text.strip()):
                    result["secrets"].append({"type": sname, "value": text.strip()[:200], **rec})
                    break
            for em in re.findall(r"[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}", text):
                result["emails"].add(em)
        # 保险库
        if text and ('"encrypted-scrypt-tweetnacl"' in text or "ciphertext" in text and "cipher" in text):
            result["vaults"].append({"kind": "encrypted-vault", "table": tname, "rowid": it.get("rowid"), "svc": svc, "acct": acct, "ag": ag})
        # 网站口令（cfnetwork label 含账号）
        if "com.apple.cfnetwork" in ag and "@" in acct and kind == "ascii":
            result["passwords"].append({"site_acct": acct, "pw": text.strip(), "label": it.get("label")})
        if hit_brands and (kind in ("json", "ascii") or "mnemonic" in acct.lower() or "seed" in acct.lower()):
            result["interesting"].append(rec)
    result["emails"] = sorted(result["emails"])
    return result, n

# ------------------------------------------------------------------ keystore 解密
def extract_keystores(obj):
    out = []
    seen = set()

    def add(app, path, ks, blen=0):
        mac = ((ks.get("crypto") or {}).get("mac") or "").lower()
        if mac and mac in seen:
            return
        if mac:
            seen.add(mac)
        out.append({"app": app, "path": path, "ks": ks, "b64len": blen})

    # 1) 沙盒 Documents/keystore/UTC--*
    sb = (obj or {}).get("sandbox") or {}
    for app, files in sb.items():
        if not isinstance(files, dict):
            continue
        for fp, b64 in files.items():
            if "/keystore/UTC" not in fp:
                continue
            try:
                ks = json.loads(tw.b64d(b64).decode("utf-8"))
            except Exception:
                continue
            add(app, fp, ks, len(b64))
    # 2) 钥匙串内嵌 keystore JSON（Bybit Web3 等）
    for sect, tname, it in iter_items(obj):
        _, kind, text = decode_hex_smart(it.get("dataHex") or "")
        if kind != "json" or not text or not text.lstrip().startswith("{"):
            continue
        try:
            ks = json.loads(text)
        except Exception:
            continue
        if isinstance(ks, dict) and isinstance(ks.get("crypto"), dict) and "kdfparams" in ks["crypto"]:
            add("keychain", "keychain rowid=%s" % it.get("rowid"), ks)
    return out

def keystore_passwords(obj):
    """钥匙串内 <app>UTC--<file> 形式的密码候选 + 全部明文口令"""
    by_acct, plain = {}, []
    for sect, tname, it in iter_items(obj):
        acct = str(it.get("account") or "")
        hexs = it.get("dataHex") or ""
        if "UTC--" in acct:
            try:
                pw = bytes.fromhex(hexs).decode("ascii")
                if 8 <= len(pw) <= 128 and all(32 <= ord(c) < 127 for c in pw):
                    by_acct[acct] = pw
            except Exception:
                pass
        if "com.apple.cfnetwork" in str(it.get("accessGroup") or "") and "@" in acct:
            try:
                pw = bytes.fromhex(hexs).decode("ascii")
                if 4 <= len(pw) < 64:
                    plain.append(pw)
            except Exception:
                pass
    return by_acct, list(dict.fromkeys(plain))

def decrypt_keystore_entry(entry, pw_by_acct, plain_pws):
    fp = entry["path"]; ks = entry["ks"]
    fname = fp.split("/")[-1]
    cands = []
    for key in (fname, "trustwallet" + fname):
        if key in pw_by_acct: cands.append(pw_by_acct[key])
    cands += [v for k, v in pw_by_acct.items() if v not in cands]
    cands += plain_pws[:200]
    cands += BUILTIN_PINS
    seen = set()
    cands = [c for c in cands if c and not (c in seen or seen.add(c))]
    cr = ks.get("crypto") or {}
    kp = cr.get("kdfparams") or {}
    if not cr or "salt" not in kp: return {"path": fp, "status": "no-crypto"}
    salt = bytes.fromhex(kp["salt"]); iv = bytes.fromhex(cr["cipherparams"]["iv"])
    ct = bytes.fromhex(cr["ciphertext"]); mac = bytes.fromhex(cr["mac"])
    for pw in cands:
        try:
            if cr.get("kdf") == "scrypt":
                dk = tw.scrypt(pw.encode(), salt, int(kp["n"]), int(kp["r"]), int(kp["p"]), int(kp["dklen"]))
            else:
                dk = hashlib.pbkdf2_hmac("sha256", pw.encode(), salt, int(kp.get("c", 262144)), int(kp["dklen"]))
        except Exception:
            continue
        if tw.keccak256(dk[16:32] + ct) == mac:
            pt = tw.aes_ctr(dk[:16], iv, ct).decode("utf-8", "replace").strip()
            mn = None
            try:
                j = json.loads(pt)
                if isinstance(j, dict):
                    mn = j.get("mnemonic") or j.get("seedPhrase")
            except Exception:
                pass
            if not mn and looks_like_mnemonic(pt):
                mn = pt
            return {"path": fp, "status": "DECRYPTED", "password": pw, "plaintext": pt,
                    "mnemonic": mn, "bip39": bip39_checksum_ok(mn) if mn else None}
    return {"path": fp, "status": "encrypted (no password matched)", "tried": len(cands)}

# ------------------------------------------------------------------ 保险库解密（Tonkeeper V1）
def collect_candidates(obj, explicit=None, deep_pin=False):
    _, plain = keystore_passwords(obj)
    cands = list(explicit or []) + list(plain) + list(BUILTIN_PINS)
    if deep_pin:
        cands += ["%04d" % i for i in range(10000)]
    seen = set()
    return [c for c in cands if c and not (c in seen or seen.add(c))]


def decrypt_tonkeeper_vaults(obj, explicit=None, deep_pin=False):
    """Tonkeeper V1: scrypt(N,r,p) + secretbox(nonce=salt[:24])；V2 仅标注。

    兼容两种存放形态：
      - service = mnemonics_vault_<uuid>
      - service = app / account = wallets_chunk_<n>（Tonkeeper 分块保险库）
    一个保险库可能含多组助记词，全部返回。
    """
    try:
        import nacl.bindings as nacl_b
    except Exception:
        nacl_b = None
    cands = collect_candidates(obj, explicit, deep_pin)
    out = []
    seen_vault = set()
    seen_v2 = set()
    for sect, tname, it in iter_items(obj):
        svc = str(it.get("service") or "")
        if svc.startswith("v2mnemonics_"):
            if svc in seen_v2:
                continue
            seen_v2.add(svc)
            out.append({"wallet": "Tonkeeper V2 (%s)" % svc[:30],
                        "status": "V2 格式（Argon2id+AES-GCM，需另行处理）",
                        "mnemonic": None, "password": None})
            continue
        acct = str(it.get("account") or "")
        try:
            acct_dec = bytes.fromhex(acct).decode("utf-8", "replace")
        except Exception:
            acct_dec = acct
        try:
            text = bytes.fromhex(it.get("dataHex") or "").decode("utf-8", "replace")
        except Exception:
            continue
        if not text or ("encrypted-scrypt-tweetnacl" not in text
                        and not svc.startswith("mnemonics_vault_")
                        and "wallets_chunk" not in acct_dec):
            continue
        try:
            d = json.loads(text)
        except Exception:
            continue
        if not isinstance(d, dict) or "salt" not in d or "ct" not in d:
            continue
        vkey = (d.get("salt"), d.get("ct"))
        if vkey in seen_vault:
            continue
        seen_vault.add(vkey)
        if svc.startswith("mnemonics_vault_"):
            label = "Tonkeeper (%s)" % svc[:24]
        elif acct_dec:
            label = "Tonkeeper (%s)" % acct_dec[:24]
        else:
            label = "Tonkeeper (vault)"
        if nacl_b is None:
            out.append({"wallet": label, "status": "需 pynacl（pip install pynacl）",
                        "mnemonic": None, "password": None})
            continue
        try:
            salt = bytes.fromhex(d["salt"])
            ct = bytes.fromhex(d["ct"])
            N = int(d.get("N", 16384)); r = int(d.get("r", 8)); p = int(d.get("p", 1))
        except Exception:
            out.append({"wallet": label, "status": "V1 参数异常", "mnemonic": None, "password": None})
            continue
        got = False
        for i, pw in enumerate(cands):
            if deep_pin and i and i % 500 == 0:
                print("      [Tonkeeper PIN] %d/%d …" % (i, len(cands)), flush=True)
            try:
                key = tw.scrypt(pw.encode(), salt, N, r, p, 32)
                pt = nacl_b.crypto_secretbox_open(ct, salt[:24], key)
                if not pt:
                    continue
                plain = pt.decode("utf-8", "replace")
                mns = []
                try:
                    j = json.loads(plain)
                    for v in j.values():
                        if isinstance(v, dict) and v.get("mnemonic"):
                            mns.append(v["mnemonic"])
                except Exception:
                    pass
                if not mns and looks_like_mnemonic(plain):
                    mns = [plain.strip()]
                if not mns:
                    out.append({"wallet": label, "status": "DECRYPTED（未识别助记词）",
                                "mnemonic": None, "password": pw})
                for mn in mns:
                    out.append({"wallet": label, "status": "DECRYPTED", "mnemonic": mn,
                                "bip39": bip39_checksum_ok(mn),
                                "verify": "scrypt+secretbox（nonce=salt[:24]）", "password": pw})
                got = True
                break
            except Exception:
                continue
        if not got:
            out.append({"wallet": label, "status": "V1 保险库加密（候选未命中）",
                        "mnemonic": None, "password": None, "tried": len(cands)})
    return out


# ------------------------------------------------------------------ 主流程
def process_file(path, outdir, do_decrypt=True, explicit_pw=None, deep_pin=False):
    obj, raw = load_export(path)
    name = os.path.splitext(os.path.basename(path))[0]
    if obj is None:
        return {"file": path, "error": "parse failed"}
    meta = profile(obj)
    brands = brand_fingerprint(obj, raw)
    items, n = analyze_items(obj, raw)
    ks_entries = extract_keystores(obj)
    ks_results = []
    vault_results = []
    if do_decrypt:
        by_acct, plain = keystore_passwords(obj)
        for e in ks_entries:
            ks_results.append(decrypt_keystore_entry(e, by_acct, plain))
        vault_results = decrypt_tonkeeper_vaults(obj, explicit_pw, deep_pin)
    # 智能交叉核对: 熵推导助记词 vs 包内 BIP39 seed 条目（64B=hex128 或 32B=hex64）
    hex64s = {s["value"].lower() for s in items["secrets"] if s["type"] in ("hex64", "hex128")}
    for s in items["secrets"]:
        if s["type"].startswith("mnemonic") and s.get("crosscheck") is None and hex64s:
            try:
                seed = tw.mnemonic_to_seed(s["value"])
                if seed.hex().lower() in hex64s:
                    s["crosscheck"] = "MATCH 包内 BIP39 seed (hex64)"
            except Exception:
                pass
    rep = {
        "file": os.path.basename(path),
        "meta": meta,
        "brands": brands,
        "item_count": n,
        "secrets": items["secrets"],
        "emails": items["emails"],
        "passwords_count": len(items["passwords"]),
        "vaults": items["vaults"],
        "vault_decrypted": vault_results,
        "keystores": ks_results if ks_results else [{"path": e["path"], "status": "not-attempted"} for e in ks_entries],
    }
    if (obj or {}).get("_salvaged"):
        rep["meta"]["salvaged"] = True
        rep["meta"]["salvage_note"] = (obj or {}).get("_salvage_note")
    # 控制台摘要（默认降噪：只展示高价值密钥/保险库/keystore/邮箱/品牌；uuid/hex32/hex64/jwt 仅计数）
    print(f"\n===== {rep['file']} =====")
    print(f"  设备: {meta['device_uuid']}  时间: {meta.get('timestamp')}")
    if meta.get("salvaged"):
        print(f"  [抢救] {meta.get('salvage_note')}")
    print(f"  品牌: {', '.join(f'{k}({v})' for k, v in sorted(brands.items())) or '-'}")
    print(f"  条目: {n}  沙盒: {meta['sandbox_apps'] or '-'}")
    seen_vals = set()
    counts = {}
    high = {"b58_ext_key", "ed25519", "wif"}
    for s in rep["secrets"]:
        counts[s["type"]] = counts.get(s["type"], 0) + 1
        if (s["type"].startswith("mnemonic") or s["type"] in high) and s["value"] not in seen_vals:
            seen_vals.add(s["value"])
            extra = f" | bip39={s['bip39']} | {s.get('crosscheck')}" if s["type"].startswith("mnemonic") else ""
            print(f"  [密钥] {s['type']}: {str(s['value'])[:100]}{extra}")
    low_counts = {k: v for k, v in counts.items() if k not in high}
    if low_counts:
        print("  [计数] " + ", ".join(f"{k}={v}" for k, v in sorted(low_counts.items())))
    for k in rep["keystores"]:
        print(f"  [keystore] {k['path'].split('/')[-1]} -> {k['status']}" + (f" | pw={k.get('password')}" if k.get("status") == "DECRYPTED" else ""))
        if k.get("status") == "DECRYPTED":
            if k.get("mnemonic"):
                print(f"             mnemonic: {k['mnemonic']} | bip39={k.get('bip39')}")
            else:
                print(f"             plaintext: {k['plaintext'][:120]}")
    for v in vault_results:
        line = f"  [保险库解密] {v['wallet']} -> {v['status']}"
        if v.get("status") == "DECRYPTED":
            line += f" | PIN={v.get('password')} | bip39={v.get('bip39')}"
            line += f"\n             mnemonic: {v.get('mnemonic')}"
        elif v.get("tried"):
            line += f" | tried={v['tried']}"
        print(line)
    seen_vault = set()
    for v in rep["vaults"]:
        key = (v["table"], v["rowid"])
        if key in seen_vault: continue
        seen_vault.add(key)
        print(f"  [保险库] {v['svc'] or v['acct']} ({v['table']}:{v['rowid']}) 加密")
    if rep["emails"]:
        print(f"  [邮箱] {', '.join(rep['emails'][:12])}")
    if outdir:
        os.makedirs(outdir, exist_ok=True)
        with open(os.path.join(outdir, f"{name}_smart.json"), "w", encoding="utf-8") as fh:
            json.dump(rep, fh, ensure_ascii=False, indent=2)
    return rep

def main():
    ap = argparse.ArgumentParser(description="iOS 钥匙串导出智能识别/读取/解析")
    ap.add_argument("target", help="导出 JSON 文件或目录")
    ap.add_argument("--out", default=None, help="报告输出目录")
    ap.add_argument("--pattern", default="*.json", help="目录模式下的文件匹配（如 2026092*.json）")
    ap.add_argument("--no-decrypt", action="store_true")
    ap.add_argument("--pw", nargs="*", default=[], help="追加保险库口令候选")
    ap.add_argument("--deep-pin", action="store_true",
                    help="Tonkeeper V1 追加全量 0000-9999（慢；命中即停）")
    args = ap.parse_args()

    import fnmatch
    files = []
    if os.path.isdir(args.target):
        for f in sorted(os.listdir(args.target)):
            if f.lower().endswith(".json") and fnmatch.fnmatch(f, args.pattern):
                files.append(os.path.join(args.target, f))
    else:
        files = [args.target]
    for f in files:
        try:
            process_file(f, args.out, do_decrypt=not args.no_decrypt,
                         explicit_pw=args.pw, deep_pin=args.deep_pin)
        except Exception as e:
            print(f"  !! {f}: {e}")
    print("\nDONE")


# ------------------------------------------------------------------ 统一解密模块
# 解密能力已收拢到 war_decrypt.py；这里只做薄封装，保持原函数名/输出不变。
import war_decrypt as wd


def load_export(path):
    return wd.load_export(path)


def extract_keystores(obj):
    return wd.extract_keystores(obj)


def keystore_passwords(obj):
    return wd.keystore_passwords(obj)


def decrypt_keystore_entry(entry, pw_by_acct, plain_pws):
    return wd.decrypt_keystore_entry(entry, pw_by_acct, plain_pws)


def decrypt_tonkeeper_vaults(obj, explicit=None, deep_pin=False):
    return wd.recover_tonkeeper_vaults(obj, collect_candidates(obj, explicit, deep_pin))

if __name__ == "__main__":
    main()
