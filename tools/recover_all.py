#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""recover_all.py — 全合一钱包助记词恢复工具（方案库版）

覆盖 9 类钱包（按源码级/标准方案实现）：
  明文类（无需口令）:
    1) Phantom     — 钥匙串 vault seed 熵 → 助记词（+seed 缓存核对）
    2) Uniswap     — 钥匙串明文助记词 + zprv 同源核对
    3) Bitpie      — seedPhraseEntropy 熵 → 助记词（+userHDseed 核对）
    4) Exodus      — 钥匙串明文助记词
    5) Coin98      — WALLET_SECURE_BACKUP 明文（助记词+私钥，TRON 地址核对）
  口令类（自动从包内+内置+词表收集候选口令）:
    6) Trust Wallet — keystore v3（scrypt/PBKDF2 + AES-CTR + keccak MAC）
    7) imToken      — keystore v3（PBKDF2-SHA256 262144 + AES-128-CTR）
    8) MetaMask     — quick-crypto（PBKDF2-SHA512 + AES-256-CBC, PKCS7）★源码级
    9) Tonkeeper    — V1 保险库（scrypt N=16384/r=8/p=1/dkLen=32 + tweetnacl secretbox,
                       nonce=salt[:24]）★源码级; V2(v2mnemonics_) 检测并标注

用法:
  python recover_all.py <导出JSON> [--pw P1 P2 ...] [--words 词表.txt]
                        [--deep-pin] [--out DIR]

依赖: 同目录 tw_keystore_tool.py + bip39_english.txt；pynacl（Tonkeeper 需要）
"""
import argparse, base64, csv, fnmatch, hashlib, json, os, re, sys, time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import tw_keystore_tool as tw

D = os.path.dirname(os.path.abspath(__file__))

try:
    import nacl.bindings as nacl_b
    HAVE_NACL = True
except Exception:
    HAVE_NACL = False

# ------------------------------------------------------------------ 基础
def load_wordlist():
    p = os.path.join(D, "bip39_english.txt")
    return open(p, encoding="utf-8").read().split()

WL = load_wordlist()
WIDX = {w: i for i, w in enumerate(WL)}

def bip39_ok(mn):
    try:
        words = mn.split()
        if len(words) not in (12, 15, 18, 21, 24):
            return False
        bits = "".join(format(WIDX[w], "011b") for w in words)
        total = len(bits)
        ent_bits = total * 32 // 33
        ent = int(bits[:ent_bits], 2).to_bytes(ent_bits // 8, "big")
        cs = int(bits[ent_bits:], 2)
        expect = hashlib.sha256(ent).digest()[0] >> (8 - (total - ent_bits))
        return cs == expect
    except Exception:
        return False

def mnemonic_from_entropy(ent: bytes):
    h = hashlib.sha256(ent).digest()
    bits = "".join(format(b, "08b") for b in ent) + format(h[0], "08b")[:len(ent) * 8 // 32]
    return " ".join(WL[int(bits[i:i + 11], 2)] for i in range(0, len(bits), 11))

def decode_hex_text(hexs):
    if not hexs:
        return None
    try:
        b = bytes.fromhex(hexs)
    except Exception:
        return None
    printable = sum(1 for x in b if 32 <= x <= 126)
    if b and printable / len(b) >= 0.85:
        return b.decode("utf-8", "replace")
    return None

def decode_hex_bytes(hexs):
    try:
        return bytes.fromhex(hexs or "")
    except Exception:
        return b""

B58A = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
def b58dec(s):
    n = 0
    for c in s:
        n = n * 58 + B58A.index(c)
    b = n.to_bytes((n.bit_length() + 7) // 8, "big")
    return b"\x00" * (len(s) - len(s.lstrip("1"))) + b

def iter_items(obj):
    for top in ("keychain", "big"):
        t = obj.get(top)
        if not isinstance(t, dict):
            continue
        for sect in ("wallets", "tables"):
            sv = t.get(sect)
            if not isinstance(sv, dict):
                continue
            for name, sub in sv.items():
                if isinstance(sub, dict) and isinstance(sub.get("items"), list):
                    for it in sub["items"]:
                        yield (f"{top}.{sect}", name, it)

def items_of(wallets, name):
    g = (wallets or {}).get(name) or {}
    return g.get("items") or []

def all_items(obj):
    """全条目迭代（wallets 分组 + tables），按 (rowid,account,service) 去重"""
    seen = set()
    for sect, tname, it in iter_items(obj):
        key = (it.get("rowid"), str(it.get("account")), str(it.get("service")))
        if key in seen:
            continue
        seen.add(key)
        yield it

# ------------------------------------------------------------------ 口令候选
BUILTIN = [
    # 4 位 PIN（Tonkeeper 实测为 4 位）
    "8888", "0000", "1111", "2222", "3333", "4444", "5555", "6666", "7777", "9999",
    "1234", "4321", "1122", "1212", "2121", "2580", "0852", "1998", "1999", "2000",
    "2024", "2025", "2026", "1004", "6668", "8880",
    # 6 位
    "888888", "000000", "111111", "222222", "333333", "444444", "555555", "666666",
    "777777", "999999", "123456", "654321", "112233", "121212", "123123",
    # 8 位 / 常见
    "88888888", "00000000", "12345678", "11111111", "password", "Password", "qwerty",
    "abc123", "123456789", "1234567890", "admin", "root",
]

def collect_candidates(obj, explicit, words_file, deep_pin):
    cands = []
    for p in (explicit or []):
        cands.append(p)
    # 包内明文口令（网站口令 / keystore 密码 / JSON 内 password/pin 字段）
    for sect, tname, it in iter_items(obj):
        ag = it.get("accessGroup") or ""
        acct = it.get("account") or ""
        val = decode_hex_text(it.get("dataHex"))
        if not val:
            continue
        if "com.apple.cfnetwork" in ag and "@" in acct and 4 <= len(val) < 64:
            cands.append(val)
        if "UTC--" in acct and 8 <= len(val) <= 128:
            cands.append(val)
        if val.startswith("{"):
            try:
                j = json.loads(val)
                for k in ("password", "passwd", "passphrase", "pin_code", "pinCode"):
                    v = j.get(k)
                    if isinstance(v, str) and v:
                        cands.append(v)
            except Exception:
                pass
    cands += BUILTIN
    if words_file and os.path.exists(words_file):
        with open(words_file, encoding="utf-8-sig", errors="replace") as fh:
            for ln in fh:
                ln = ln.strip()
                if ln:
                    cands.append(ln)
    # 注意：deep-pin 不在这里加，避免把所有 keystore 都拖进 10000 次扫描；
    # 只在 Tonkeeper V1 上使用（见 process_one / run_all）。
    # 数字变体: n + "." / "!"
    extra = []
    for c in cands:
        if re.fullmatch(r"\d{4,8}", c):
            extra.append(c + ".")
            extra.append(c + "!")
    cands += extra
    # 去重保序
    seen = set()
    out = []
    for c in cands:
        if c and c not in seen:
            seen.add(c)
            out.append(c)
    return out

# ------------------------------------------------------------------ 明文类钱包
def recover_phantom(obj):
    out = []
    seed_cache = None
    for it in all_items(obj):
        t = decode_hex_text(it.get("dataHex"))
        if not t:
            continue
        acct = it.get("account") or ""
        try:
            acct_dec = bytes.fromhex(acct).decode("utf-8", "replace")
        except Exception:
            acct_dec = acct
        if "vault.seed" in acct_dec and '"entropy"' in t:
            j = json.loads(t)
            ent = j.get("entropy")
            if isinstance(ent, dict) and len(ent) in (16, 24, 32):
                ent_bytes = bytes(int(ent[str(i)]) for i in range(len(ent)))
                mn = mnemonic_from_entropy(ent_bytes)
                out.append({"wallet": "Phantom", "status": "ENTROPY->MNEMONIC",
                            "mnemonic": mn, "bip39": bip39_ok(mn),
                            "verify": "entropy 推导", "password": None})
        if "cache.seed" in acct_dec and '"seed"' in t:
            try:
                j = json.loads(t)
                b64 = j.get("seed", {}).get("bytes")
                if b64:
                    seed_cache = tw.b64d(b64)
            except Exception:
                pass
    for r in out:
        if seed_cache and r.get("mnemonic"):
            try:
                calc = tw.mnemonic_to_seed(r["mnemonic"])
                if len(seed_cache) == 64:
                    r["verify"] += f" | seed 缓存核对: {'MATCH' if calc == seed_cache else 'NO-MATCH'}"
            except Exception:
                pass
    return out or None

def recover_uniswap(obj):
    out = []
    for it in all_items(obj):
        t = decode_hex_text(it.get("dataHex"))
        if not t:
            continue
        acct = it.get("account") or ""
        if acct.startswith("com.uniswap.mobile.mnemonic."):
            out.append({"wallet": "Uniswap", "status": "PLAINTEXT", "mnemonic": t.strip(),
                        "bip39": bip39_ok(t.strip()), "verify": "钥匙串明文", "password": None})
        if acct.startswith("com.uniswap.mobile.privateKey.") and t.strip().startswith("zprv"):
            out.append({"wallet": "Uniswap", "status": "EXTKEY(zprv)", "mnemonic": None,
                        "zprv": t.strip(), "verify": "", "password": None})
    for r in out:
        if r.get("zprv"):
            try:
                raw = b58dec(r["zprv"])
                if len(raw) == 82:
                    priv_z = raw[:-4][46:78].hex()
                    mn = next((x["mnemonic"] for x in out if x.get("mnemonic")), None)
                    if mn:
                        seed = tw.mnemonic_to_seed(mn)
                        ph = tw.derive_path(seed, tw.parse_path("m/44'/60'/0'/0/0")).to_bytes(32, "big").hex()
                        r["verify"] = f"zprv 同源: {'MATCH' if ph == priv_z else 'NO-MATCH'}"
                    else:
                        r["verify"] = "zprv 已解码"
            except Exception as e:
                r["verify"] = f"zprv 解码失败: {e}"
    return out or None

def recover_bitpie(obj):
    out = []
    entropy = None
    hdseed = None
    for it in all_items(obj):
        blob = (str(it.get("accessGroup") or "") + str(it.get("service") or "")).lower()
        if "bitpie" not in blob:
            continue
        t = decode_hex_text(it.get("dataHex"))
        acct = it.get("account") or ""
        if acct == "seedPhraseEntropy" and t:
            ts = t.strip()
            if re.fullmatch(r"(?:[0-9a-fA-F]{2}){16}|(?:[0-9a-fA-F]{2}){24}|(?:[0-9a-fA-F]{2}){32}", ts):
                entropy = bytes.fromhex(ts)
        if acct == "userHDseed":
            rb = decode_hex_bytes(it.get("dataHex"))
            try:
                ts = rb.decode("ascii").strip()
                if re.fullmatch(r"[0-9a-fA-F]{128}", ts):
                    hdseed = bytes.fromhex(ts)
            except Exception:
                pass
    if entropy:
        mn = mnemonic_from_entropy(entropy)
        r = {"wallet": "Bitpie", "status": "ENTROPY->MNEMONIC", "mnemonic": mn,
             "bip39": bip39_ok(mn), "verify": "entropy 推导", "password": None}
        if hdseed and len(hdseed) == 64:
            calc = tw.mnemonic_to_seed(mn)
            r["verify"] += f" | userHDseed 核对: {'MATCH' if calc == hdseed else 'NO-MATCH'}"
        out.append(r)
    return out or None

def recover_exodus(obj):
    out = []
    seen = set()
    for it in all_items(obj):
        blob = (str(it.get("accessGroup") or "") + str(it.get("service") or "")).lower()
        if "exodus" not in blob:
            continue
        t = decode_hex_text(it.get("dataHex"))
        if not t or '"mnemonic"' not in t:
            continue
        try:
            j = json.loads(t)
        except Exception:
            continue
        mn = j.get("mnemonic")
        if mn and mn not in seen:
            seen.add(mn)
            out.append({"wallet": "Exodus", "status": "PLAINTEXT", "mnemonic": mn.strip(),
                        "bip39": bip39_ok(mn.strip()), "verify": "钥匙串明文", "password": None})
    return out or None

def recover_plaintext_wallets(obj):
    """通用明文助记词扫描（覆盖 Bitcoin.com Wallet 等：值本身是 BIP39 助记词且上下文含 wallet）"""
    out = []
    seen = set()
    for it in all_items(obj):
        v = decode_hex_text(it.get("dataHex"))
        if not v:
            continue
        words = v.strip().split()
        if len(words) not in (12, 15, 18, 21, 24):
            continue
        if not all(w in WIDX for w in words):
            continue
        blob = (str(it.get("accessGroup") or "") + " " + str(it.get("service") or "") + " " + str(it.get("account") or "")).lower()
        # 放宽：除了字面 "wallet"，也认已知钱包 App 的 keychain 标识
        WALLET_CTX = ("wallet", "sweateconomy", "3k666jg497.swc", "nearseed",
                      "alohabrowser", "trustwallet", "phantom", "metamask",
                      "okex", "binance", "coinbase", "exodus", "tonkeeper")
        if not any(k in blob for k in WALLET_CTX):
            continue
        mn = v.strip()
        if mn in seen:
            continue
        seen.add(mn)
        if "bitcoin.mwallet" in blob or "bitcoincom" in blob:
            app = "Bitcoin.com Wallet"
        else:
            app = str(it.get("service") or it.get("accessGroup") or "wallet")[:32]
        out.append({"wallet": f"{app}（明文）", "status": "PLAINTEXT", "mnemonic": mn,
                    "bip39": bip39_ok(mn), "verify": f"钥匙串明文（{str(it.get('account'))[:44]}）", "password": None})
    return out or None

def recover_coin98(obj):
    out = []
    for it in all_items(obj):
        if (it.get("account") or "") != "WALLET_SECURE_BACKUP":
            continue
        blob = (str(it.get("accessGroup") or "") + str(it.get("service") or "")).lower()
        if "coin98" not in blob:
            continue
        t = decode_hex_text(it.get("dataHex"))
        if not t:
            continue
        try:
            arr = json.loads(t)
        except Exception:
            continue
        for e in arr:
            mn = e.get("mnemonic")
            pk = e.get("privateKey")
            addr = e.get("address")
            chain = e.get("chain")
            verify = "明文备份"
            if pk and addr and chain == "tron":
                try:
                    calc = tw.tron_address(int(pk, 16))
                    verify += f" | TRON 地址核对: {'MATCH' if calc == addr else 'NO-MATCH'}"
                except Exception:
                    pass
            out.append({"wallet": f"Coin98 ({e.get('name','')})", "status": "PLAINTEXT",
                        "mnemonic": mn, "bip39": bip39_ok(mn or ""), "verify": verify,
                        "address": addr, "privateKey": pk, "chain": chain, "password": None})
    return out or None

# ------------------------------------------------------------------ 口令类钱包
def _progress(i, total, tag):
    if total > 100 and i % 200 == 0:
        print(f"      [{tag}] {i}/{total} …", flush=True)

def recover_keystores(obj, cands):
    """keystore 通用解密：沙盒 UTC-- 文件 + 钥匙串内嵌 keystore JSON（Trust / Bybit Web3 / 其他）"""
    jobs = []  # (label, ks, source)
    sb = obj.get("sandbox") or {}
    for app, files in sb.items():
        if not isinstance(files, dict):
            continue
        for fp, b64 in files.items():
            if "/keystore/UTC" in fp or "UTC--" in fp.split("/")[-1]:
                try:
                    ks = json.loads(tw.b64d(b64).decode("utf-8"))
                except Exception:
                    continue
                label = {"trust_wallet": "Trust Wallet", "bybit": "Bybit Web3"}.get(app, f"{app} keystore")
                jobs.append((label, ks, f"sandbox {app}/{fp.split('/')[-1]}"))
    # 钥匙串内嵌 keystore（Bybit Web3 等以此方式存储）
    for sect, tname, it in iter_items(obj):
        v = decode_hex_text(it.get("dataHex"))
        if not v or not v.lstrip().startswith("{"):
            continue
        try:
            ks = json.loads(v)
        except Exception:
            continue
        if isinstance(ks, dict) and isinstance(ks.get("crypto"), dict) and "kdfparams" in ks["crypto"]:
            ag = str(it.get("accessGroup") or "")
            svc = str(it.get("service") or "")
            acct = str(it.get("account") or "")
            blob = (ag + " " + svc + " " + acct).lower()
            label = "Bybit Web3" if "bybit" in blob else f"keystore ({svc[:24] or acct[:24] or 'keychain'})"
            jobs.append((label, ks, f"keychain rowid={it.get('rowid')}"))
    # 去重（按 mac）
    seen = set()
    uniq = []
    for label, ks, src in jobs:
        m = (ks.get("crypto") or {}).get("mac")
        if m in seen:
            continue
        seen.add(m)
        uniq.append((label, ks, src))
    if not uniq:
        return None
    out = []
    for label, ks, src in uniq:
        cr = ks.get("crypto") or {}
        kp = cr.get("kdfparams") or {}
        if not cr:
            continue
        salt = bytes.fromhex(kp["salt"]); iv = bytes.fromhex(cr["cipherparams"]["iv"])
        ct = bytes.fromhex(cr["ciphertext"]); mac = bytes.fromhex(cr["mac"])
        got = False
        for i, pw in enumerate(cands):
            _progress(i, len(cands), label)
            try:
                if cr.get("kdf") == "scrypt":
                    dk = tw.scrypt(pw.encode(), salt, int(kp["n"]), int(kp["r"]), int(kp["p"]), int(kp["dklen"]))
                else:
                    dk = hashlib.pbkdf2_hmac("sha256", pw.encode(), salt, int(kp.get("c", 262144)), int(kp["dklen"]))
            except Exception:
                continue
            if tw.keccak256(dk[16:32] + ct) == mac:
                pt = tw.aes_ctr(dk[:16], iv, ct).decode("utf-8", "replace").strip()
                out.append({"wallet": label, "status": "DECRYPTED", "mnemonic": pt,
                            "bip39": bip39_ok(pt), "verify": "keystore MAC ok", "password": pw,
                            "source": src})
                got = True
                break
        if not got:
            out.append({"wallet": label, "status": "keystore 加密（候选未命中）",
                        "mnemonic": None, "verify": "", "password": None, "source": src})
    return out or None

def recover_imtoken(obj, cands):
    out = []
    sb = obj.get("sandbox") or {}
    im_sb = sb.get("imtoken") or {}
    files = [(fp, b64) for fp, b64 in im_sb.items() if fp.endswith(".json")]
    if not files:
        return None
    for fp, b64 in files:
        try:
            ks = json.loads(tw.b64d(b64).decode("utf-8"))
        except Exception:
            continue
        cr = ks.get("crypto") or {}
        kp = cr.get("kdfparams") or {}
        if not cr:
            continue
        salt = bytes.fromhex(kp["salt"]); iv = bytes.fromhex(cr["cipherparams"]["iv"])
        ct = bytes.fromhex(cr["ciphertext"]); mac = bytes.fromhex(cr["mac"])
        c = int(kp.get("c", 262144))
        got = False
        for i, pw in enumerate(cands):
            _progress(i, len(cands), "imToken")
            dk = hashlib.pbkdf2_hmac("sha256", pw.encode(), salt, c, int(kp["dklen"]))
            if tw.keccak256(dk[16:32] + ct) == mac:
                pt = tw.aes_ctr(dk[:16], iv, ct).decode("utf-8", "replace").strip()
                mn = pt
                try:
                    j = json.loads(pt)
                    mn = j.get("mnemonic") or j.get("seedPhrase") or pt
                except Exception:
                    pass
                out.append({"wallet": "imToken", "status": "DECRYPTED", "mnemonic": mn,
                            "bip39": bip39_ok(mn), "verify": "keystore MAC ok", "password": pw,
                            "source": os.path.basename(fp)})
                got = True
                break
        if not got:
            out.append({"wallet": "imToken", "status": "keystore 加密（候选未命中）",
                        "mnemonic": None, "verify": "", "password": None, "source": os.path.basename(fp)})
    return out or None

def recover_metamask(obj, cands):
    vault_txt = None
    for it in all_items(obj):
        if (it.get("account") or "") == "VAULT_BACKUP":
            blob = (str(it.get("accessGroup") or "") + str(it.get("service") or "")).lower()
            if "metamask" not in blob:
                continue
            vault_txt = decode_hex_text(it.get("dataHex"))
    if not vault_txt:
        return None
    try:
        mm = json.loads(vault_txt)
    except Exception:
        return None
    got = False
    out = []
    # 新版 mobile quick-crypto / original: PBKDF2-SHA512 + AES-256-CBC
    if "cipher" in mm and "salt" in mm:
        cipher = base64.b64decode(mm["cipher"])
        iv = bytes.fromhex(mm["iv"])
        salt_str = mm["salt"]
        iters = int(mm.get("keyMetadata", {}).get("params", {}).get("iterations", 5000))
        from cryptography.hazmat.primitives.kdf.pbkdf2 import PBKDF2HMAC
        from cryptography.hazmat.primitives import hashes
        from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
        from cryptography.hazmat.primitives.padding import PKCS7
        for i, pw in enumerate(cands):
            _progress(i, len(cands), "MetaMask")
            try:
                dk = PBKDF2HMAC(algorithm=hashes.SHA512(), length=32, salt=salt_str.encode(),
                                iterations=iters).derive(pw.encode())
                dec = Cipher(algorithms.AES(dk), modes.CBC(iv)).decryptor()
                raw = dec.update(cipher) + dec.finalize()
                unp = PKCS7(128).unpadder()
                pt = (unp.update(raw) + unp.finalize()).decode("utf-8", "replace")
                if not pt.lstrip().startswith(("[", "{")):
                    continue
                j = json.loads(pt)
                mn = None
                pks = []
                for kr in j if isinstance(j, list) else [j]:
                    d = kr.get("data", {}) if isinstance(kr, dict) else {}
                    if isinstance(d.get("mnemonic"), list):
                        try:
                            mn = bytes(d["mnemonic"]).decode("utf-8", "replace")
                        except Exception:
                            pass
                    if isinstance(d.get("privateKey"), str):
                        pks.append(d["privateKey"])
                out.append({"wallet": "MetaMask", "status": "DECRYPTED", "mnemonic": mn,
                            "bip39": bip39_ok(mn) if mn else None,
                            "verify": f"quick-crypto 解出（keyrings={len(j) if isinstance(j, list) else 1}）",
                            "password": pw, "privateKeys": pks[:5]})
                got = True
                break
            except Exception:
                continue
    if not got:
        out.append({"wallet": "MetaMask", "status": "VAULT_BACKUP 加密（候选未命中）",
                    "mnemonic": None, "verify": "", "password": None})
    return out

def recover_tonkeeper(obj, cands):
    """Tonkeeper V1：兼容 mnemonics_vault_* 与 wallets_chunk_*（服务名 app）两种形态；
    一个保险库内可能含多组助记词，全部返回。"""
    vaults = {}
    v2 = []
    for sect, tname, it in iter_items(obj):
        svc = str(it.get("service") or "")
        acct = str(it.get("account") or "")
        try:
            acct_dec = bytes.fromhex(acct).decode("utf-8", "replace")
        except Exception:
            acct_dec = acct
        if svc.startswith("v2mnemonics_"):
            v2.append(svc)
            continue
        try:
            txt = bytes.fromhex(it.get("dataHex") or "").decode("utf-8", "replace")
        except Exception:
            continue
        if not txt or ("encrypted-scrypt-tweetnacl" not in txt
                       and not svc.startswith("mnemonics_vault_")
                       and "wallets_chunk" not in acct_dec):
            continue
        try:
            d = json.loads(txt)
        except Exception:
            continue
        if not isinstance(d, dict) or "salt" not in d or "ct" not in d:
            continue
        if svc.startswith("mnemonics_vault_"):
            label = "Tonkeeper (%s)" % svc[:24]
        elif acct_dec:
            label = "Tonkeeper (%s)" % acct_dec[:24]
        else:
            label = "Tonkeeper (vault)"
        vaults.setdefault((d["salt"], d["ct"]), (label, d))

    out = []
    for (salt_hex, ct_hex), (label, d) in vaults.items():
        if not HAVE_NACL:
            out.append({"wallet": label, "status": "需 pynacl", "mnemonic": None,
                        "verify": "", "password": None})
            continue
        salt = bytes.fromhex(salt_hex); ct = bytes.fromhex(ct_hex)
        N = int(d.get("N", 16384)); r = int(d.get("r", 8)); p = int(d.get("p", 1))
        got = False
        for i, pw in enumerate(cands):
            _progress(i, len(cands), "Tonkeeper")
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
                if not mns:
                    mns = [plain[:200]]
                for mn in mns:
                    out.append({"wallet": label, "status": "DECRYPTED",
                                "mnemonic": mn, "bip39": bip39_ok(mn),
                                "verify": "scrypt+secretbox（nonce=salt[:24]）", "password": pw})
                got = True
                break
            except Exception:
                continue
        if not got:
            out.append({"wallet": label, "status": "V1 保险库加密（候选未命中）",
                        "mnemonic": None, "verify": "", "password": None})
    for svc in sorted(set(v2)):
        out.append({"wallet": "Tonkeeper V2 (%s)" % str(svc)[:30],
                    "status": "V2 格式（Argon2id+AES-GCM，需另行处理）",
                    "mnemonic": None, "verify": "", "password": None})
    return out or None


def recover_json_fields(obj):
    """扫任意条目 JSON 里的 mnemonic/seedPhrase/privateKey 字段（Exodus 等）。"""
    MN_KEYS = ("mnemonic", "seedphrase", "seed_phrase", "recoveryphrase",
               "recovery_phrase", "backupphrase", "backup_phrase", "phrase")
    PK_KEYS = ("privatekey", "private_key", "secretkey", "secret_key")
    out = []
    seen = set()

    def walk(o, depth=0):
        found = []
        if depth > 3:
            return found
        if isinstance(o, dict):
            for k, v in o.items():
                kl = str(k).lower().replace("-", "_")
                if isinstance(v, str):
                    if any(x in kl for x in MN_KEYS):
                        words = v.strip().split()
                        if len(words) in (12, 15, 18, 21, 24) and all(w in WIDX for w in words):
                            found.append(("mnemonic", v.strip()))
                    elif any(x in kl for x in PK_KEYS) and re.fullmatch(r"[0-9a-fA-F]{64}", v):
                        found.append(("privateKey", v))
                elif isinstance(v, (dict, list)):
                    found += walk(v, depth + 1)
        elif isinstance(o, list):
            for x in o:
                found += walk(x, depth + 1)
        return found

    for sect, tname, it in iter_items(obj):
        txt = decode_hex_text(it.get("dataHex"))
        if not txt or not txt.lstrip().startswith("{"):
            continue
        try:
            j = json.loads(txt)
        except Exception:
            continue
        for kind, val in walk(j):
            key = (kind, val)
            if key in seen:
                continue
            seen.add(key)
            base = str(it.get("service") or it.get("account") or "wallet")[:32]
            if kind == "mnemonic":
                out.append({"wallet": "%s（JSON字段）" % base, "status": "PLAINTEXT",
                            "mnemonic": val, "bip39": bip39_ok(val),
                            "verify": "JSON 字段", "password": None})
            else:
                out.append({"wallet": "%s（JSON字段）" % base, "status": "PRIVATE_KEY",
                            "mnemonic": None, "privateKey": val,
                            "verify": "JSON 字段", "password": None})
    return out or None


# ------------------------------------------------------------------ 主流程
RECOVERERS = [
    ("Phantom", lambda obj, c: recover_phantom(obj)),
    ("Uniswap", lambda obj, c: recover_uniswap(obj)),
    ("Bitpie", lambda obj, c: recover_bitpie(obj)),
    ("Exodus", lambda obj, c: recover_exodus(obj)),
    ("Coin98", lambda obj, c: recover_coin98(obj)),
    ("明文助记词钱包", lambda obj, c: recover_plaintext_wallets(obj)),
    ("JSON字段", lambda obj, c: recover_json_fields(obj)),
    ("Trust/Bybit keystore", recover_keystores),
    ("imToken", recover_imtoken),
    ("MetaMask", recover_metamask),
    ("Tonkeeper", recover_tonkeeper),
]


# ------------------------------------------------------------------ 统一解密模块
import war_decrypt as wd


def _load_export(path):
    obj, _ = wd.load_export(path)
    return obj


def recover_json_fields(obj):
    return wd.recover_json_fields(obj)


RECOVERERS = [(label, (lambda obj, c: wd.recover_tonkeeper_vaults(obj, c))
               if label == "Tonkeeper" else fn) for label, fn in RECOVERERS]

def run_all(obj, cands, tonkeeper_cands=None):
    """跑全部恢复器 + 去重（同钱包同助记词）。

    tonkeeper_cands: 仅用于 Tonkeeper V1 的候选（可包含 --deep-pin 的 0000-9999），
    避免其他 keystore 也被 10000 个 PIN 拖慢。
    """
    results = []
    for label, fn in RECOVERERS:
        try:
            use = tonkeeper_cands if (tonkeeper_cands and label == "Tonkeeper") else cands
            rs = fn(obj, use)
        except Exception as e:
            rs = [{"wallet": label, "status": f"error: {e}", "mnemonic": None}]
        if rs:
            results.extend(rs)
    dedup = []
    seen = set()
    for r in results:
        k = (r.get("wallet"), r.get("mnemonic"), r.get("status"))
        if k in seen:
            continue
        seen.add(k)
        dedup.append(r)
    return dedup

def _load_export(path):
    raw = open(path, encoding="utf-8", errors="replace").read().lstrip("\ufeff")
    try:
        obj = json.loads(raw)
    except json.JSONDecodeError:
        try:
            import smart_forensics as _sf
            obj = _sf.salvage_export(raw)
        except Exception:
            obj = None
        if obj is None:
            raise
    if isinstance(obj, dict) and not isinstance(obj.get("keychain"), dict) and not isinstance(obj.get("big"), dict):
        if any(isinstance(obj.get(k), (dict, list)) for k in ("tables", "wallets", "sandbox", "metadataKeys")):
            obj = {"keychain": obj}
    return obj


def process_one(path, args, verbose=True):
    obj = _load_export(path)
    dev = obj.get("device_uuid") or (obj.get("keychain") or {}).get("device_uuid") or "?"
    cands = collect_candidates(obj, args.pw, args.words, False)
    ton_cands = None
    if args.deep_pin:
        ton_cands = list(cands) + ["%04d" % i for i in range(10000)]
        seen = set()
        ton_cands = [c for c in ton_cands if c and not (c in seen or seen.add(c))]
    t0 = time.time()
    results = run_all(obj, cands, ton_cands)
    # 去重
    ded, seen = [], set()
    for r in results:
        k = (r.get("wallet"), r.get("mnemonic"), r.get("status"))
        if k in seen:
            continue
        seen.add(k); ded.append(r)
    results = ded
    ok = [r for r in results if r.get("mnemonic")]
    uniq = {}
    for r in ok:
        uniq.setdefault(r["mnemonic"], []).append(r["wallet"])

    if verbose:
        print("=" * 92)
        print("全合一恢复 | 包: %s | 设备: %s" % (os.path.basename(path), dev))
        print("候选口令: %d 个（显式 %d / 词表 %s / deep-pin %s）" %
              (len(cands), len(args.pw), "有" if args.words else "无",
               "开" if args.deep_pin else "关"))
        print("=" * 92)
        printed = set()
        for r in results:
            printed.add(r["wallet"])
            print("\n[%s] %s" % (r["wallet"], r["status"]))
            if r.get("mnemonic"):
                print("    助记词: %s" % r["mnemonic"])
                extra = "BIP39: %s" % ("OK" if r.get("bip39") else "FAIL")
                if r.get("verify"):
                    extra += " | %s" % r["verify"]
                if r.get("password"):
                    extra += " | 口令: %r" % r["password"]
                print("    " + extra)
            elif r.get("verify"):
                print("    " + r["verify"])
        for label, _ in RECOVERERS:
            if not any(label in p or p.startswith(label) for p in printed):
                print("\n[%s] 未检出" % label)
        print("\n" + "=" * 92)
        print("汇总: 恢复 %d 个钱包 / 唯一助记词 %d 组 | 用时 %.1fs" %
              (len(ok), len(uniq), time.time() - t0))
        for i, (mn, ws) in enumerate(uniq.items(), 1):
            print("  %d. [%s] %s" % (i, "/".join(sorted(set(ws))), mn))

    payload = {"file": os.path.basename(path), "device": dev,
               "candidates": len(ton_cands or cands),
               "results": results, "unique_mnemonics": list(uniq),
               "elapsed_sec": round(time.time() - t0, 1)}
    if args.out:
        os.makedirs(args.out, exist_ok=True)
        name = os.path.splitext(os.path.basename(path))[0]
        with open(os.path.join(args.out, name + "_allin1.json"), "w", encoding="utf-8") as fh:
            json.dump(payload, fh, ensure_ascii=False, indent=2)
        md = ["# 全合一恢复报告 — %s" % os.path.basename(path), "",
              "> 设备: %s | 候选口令 %d 个 | 恢复 %d 钱包 / %d 组助记词"
              % (dev, len(cands), len(ok), len(uniq)), ""]
        for r in results:
            md.append("## %s — %s" % (r["wallet"], r["status"]))
            if r.get("mnemonic"):
                md.append("- 助记词: `%s`" % r["mnemonic"])
                md.append("- BIP39: %s" % ("OK" if r.get("bip39") else "FAIL")
                          + (" | %s" % r.get("verify", "") if r.get("verify") else ""))
                if r.get("password"):
                    md.append("- 口令: `%s`" % r["password"])
            elif r.get("verify"):
                md.append("- %s" % r["verify"])
            md.append("")
        with open(os.path.join(args.out, name + "_allin1.md"), "w", encoding="utf-8") as fh:
            fh.write("\n".join(md))
    return payload


def main():
    ap = argparse.ArgumentParser(description="全合一钱包助记词恢复（方案库版）")
    ap.add_argument("export", help="证据导出 JSON 或目录")
    ap.add_argument("--pw", nargs="*", default=[], help="显式口令候选")
    ap.add_argument("--words", default=None, help="口令词表文件（每行一个）")
    ap.add_argument("--deep-pin", action="store_true", help="追加全量 0000-9999 四位 PIN（慢）")
    ap.add_argument("--out", default=None, help="报告输出目录")
    ap.add_argument("--pattern", default="*.json", help="目录模式下的文件匹配")
    ap.add_argument("--resume", action="store_true", help="目录模式下跳过已有报告")
    ap.add_argument("--limit", type=int, default=0, help="目录模式下最多处理多少个")
    args = ap.parse_args()

    if os.path.isdir(args.export):
        files = [os.path.join(args.export, f) for f in sorted(os.listdir(args.export))
                 if f.lower().endswith(".json") and fnmatch.fnmatch(f, args.pattern)]
        if args.limit:
            files = files[:args.limit]
        print("[*] 目录模式: %d 个包" % len(files), flush=True)
        payloads = []
        for i, f in enumerate(files, 1):
            name = os.path.splitext(os.path.basename(f))[0]
            if args.resume and args.out and os.path.exists(
                    os.path.join(args.out, name + "_allin1.json")):
                try:
                    payloads.append(json.load(open(os.path.join(args.out, name + "_allin1.json"),
                                                   encoding="utf-8")))
                    continue
                except Exception:
                    pass
            try:
                p = process_one(f, args, verbose=False)
                payloads.append(p)
                print("[%d/%d] %s -> %d 钱包" % (i, len(files), os.path.basename(f),
                                                sum(1 for r in p["results"] if r.get("mnemonic"))),
                      flush=True)
            except Exception as e:
                print("[!] %s: %r" % (os.path.basename(f), e), flush=True)
        # 汇总
        uniq = {}
        for p in payloads:
            for r in p.get("results", []):
                mn = r.get("mnemonic")
                if not mn:
                    continue
                u = uniq.setdefault(mn, {"wallets": set(), "devices": set(),
                                         "passwords": set(), "verify": set(), "bip39": r.get("bip39")})
                u["wallets"].add(r["wallet"].split(" (")[0])
                u["devices"].add(p.get("device"))
                if r.get("password"):
                    u["passwords"].add(r["password"])
                if r.get("verify"):
                    u["verify"].add(r["verify"])
        if args.out:
            os.makedirs(args.out, exist_ok=True)
            with open(os.path.join(args.out, "summary.json"), "w", encoding="utf-8") as fh:
                json.dump({"packages": len(payloads), "unique_mnemonics": len(uniq),
                           "wallets": [{"mnemonic": mn, "wallets": sorted(v["wallets"]),
                                        "devices": sorted(str(x) for x in v["devices"]),
                                        "passwords": sorted(v["passwords"]),
                                        "verify": sorted(v["verify"]), "bip39": v["bip39"]}
                                       for mn, v in uniq.items()]}, fh, ensure_ascii=False, indent=2)
            with open(os.path.join(args.out, "wallets.csv"), "w", encoding="utf-8", newline="") as fh:
                w = csv.writer(fh)
                w.writerow(["id", "wallets", "devices", "passwords", "bip39", "verify", "mnemonic"])
                for i, (mn, v) in enumerate(uniq.items(), 1):
                    w.writerow(["W%03d" % i, "/".join(sorted(v["wallets"])),
                                "/".join(sorted(str(x) for x in v["devices"])),
                                "/".join(sorted(v["passwords"])), v["bip39"],
                                "/".join(sorted(v["verify"])), mn])
            md = ["# 全合一恢复汇总", "",
                  "- 包: **%d**；唯一助记词: **%d**" % (len(payloads), len(uniq)), "",
                  "| # | 钱包 | 设备 | 口令 | 助记词 |", "|---|---|---|---|---|"]
            for i, (mn, v) in enumerate(uniq.items(), 1):
                md.append("| W%03d | %s | %s | %s | `%s` |" %
                          (i, "/".join(sorted(v["wallets"])),
                           "/".join(sorted(str(x) for x in v["devices"]))[:24],
                           "/".join(sorted(v["passwords"])) or "-", mn))
            open(os.path.join(args.out, "SUMMARY.md"), "w", encoding="utf-8").write("\n".join(md) + "\n")
        print("[DONE] 包 %d / 唯一助记词 %d" % (len(payloads), len(uniq)), flush=True)
    else:
        process_one(args.export, args, verbose=True)


if __name__ == "__main__":
    main()
