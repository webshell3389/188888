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
import argparse, base64, hashlib, json, os, re, sys, time

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
    if deep_pin:
        cands += [f"{i:04d}" for i in range(10000)]
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
        if "wallet" not in blob:
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
    vaults = {}
    for sect, tname, it in iter_items(obj):
        svc = it.get("service") or ""
        if svc.startswith("mnemonics_vault_"):
            txt = bytes.fromhex(it.get("dataHex") or "").decode("utf-8", "replace")
            try:
                d = json.loads(txt)
                if "salt" in d and "ct" in d:
                    vaults[svc] = d
            except Exception:
                pass
    v2 = [it.get("service") for sect, tname, it in iter_items(obj)
          if (it.get("service") or "").startswith("v2mnemonics_")]
    out = []
    for svc, d in sorted(vaults.items()):
        if not HAVE_NACL:
            out.append({"wallet": f"Tonkeeper ({svc[:24]})", "status": "需 pynacl", "mnemonic": None,
                        "verify": "", "password": None})
            continue
        salt = bytes.fromhex(d["salt"]); ct = bytes.fromhex(d["ct"])
        nonce = salt[:24]
        N = int(d.get("N", 16384)); r = int(d.get("r", 8)); p = int(d.get("p", 1))
        got = False
        for i, pw in enumerate(cands):
            _progress(i, len(cands), "Tonkeeper")
            try:
                key = hashlib.scrypt(pw.encode(), salt=salt, n=N, r=r, p=p, dklen=32,
                                     maxmem=512 * 1024 * 1024)
            except Exception:
                continue
            try:
                pt = nacl_b.crypto_secretbox_open(ct, nonce, key)
                if not pt:
                    continue
                mn = None
                try:
                    j = json.loads(pt.decode("utf-8", "replace"))
                    vals = [v.get("mnemonic") for v in j.values() if isinstance(v, dict)]
                    mn = vals[0] if vals else None
                except Exception:
                    mn = pt.decode("utf-8", "replace")[:200]
                out.append({"wallet": f"Tonkeeper ({svc[:24]})", "status": "DECRYPTED",
                            "mnemonic": mn, "bip39": bip39_ok(mn) if mn else None,
                            "verify": "scrypt+secretbox（nonce=salt[:24]）", "password": pw})
                got = True
                break
            except Exception:
                continue
        if not got:
            out.append({"wallet": f"Tonkeeper ({svc[:24]})", "status": "V1 保险库加密（候选未命中）",
                        "mnemonic": None, "verify": "", "password": None})
    for svc in v2:
        out.append({"wallet": f"Tonkeeper V2 ({str(svc)[:30]})", "status": "V2 格式（Argon2id+AES-GCM，需另行处理）",
                    "mnemonic": None, "verify": "", "password": None})
    return out or None

# ------------------------------------------------------------------ 主流程
RECOVERERS = [
    ("Phantom", lambda obj, c: recover_phantom(obj)),
    ("Uniswap", lambda obj, c: recover_uniswap(obj)),
    ("Bitpie", lambda obj, c: recover_bitpie(obj)),
    ("Exodus", lambda obj, c: recover_exodus(obj)),
    ("Coin98", lambda obj, c: recover_coin98(obj)),
    ("明文助记词钱包", lambda obj, c: recover_plaintext_wallets(obj)),
    ("Trust/Bybit keystore", recover_keystores),
    ("imToken", recover_imtoken),
    ("MetaMask", recover_metamask),
    ("Tonkeeper", recover_tonkeeper),
]

def run_all(obj, cands):
    """跑全部恢复器 + 去重（同钱包同助记词）"""
    results = []
    for label, fn in RECOVERERS:
        try:
            rs = fn(obj, cands)
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

def main():
    ap = argparse.ArgumentParser(description="全合一钱包助记词恢复（方案库版）")
    ap.add_argument("export", help="证据导出 JSON")
    ap.add_argument("--pw", nargs="*", default=[], help="显式口令候选")
    ap.add_argument("--words", default=None, help="口令词表文件（每行一个）")
    ap.add_argument("--deep-pin", action="store_true", help="追加全量 0000-9999 四位 PIN（慢）")
    ap.add_argument("--out", default=None, help="报告输出目录")
    args = ap.parse_args()

    raw = open(args.export, encoding="utf-8", errors="replace").read().lstrip("\ufeff")
    obj = json.loads(raw)
    dev = obj.get("device_uuid", "?")

    cands = collect_candidates(obj, args.pw, args.words, args.deep_pin)
    print("=" * 92)
    print(f"全合一恢复 | 包: {os.path.basename(args.export)} | 设备: {dev}")
    print(f"候选口令: {len(cands)} 个（显式 {len(args.pw)} / 词表 {'有' if args.words else '无'} / deep-pin {'开' if args.deep_pin else '关'}）")
    print("=" * 92)

    t0 = time.time()
    results = run_all(obj, cands)
    printed = set()
    for r in results:
        printed.add(r["wallet"])
        print(f"\n[{r['wallet']}] {r['status']}")
        if r.get("mnemonic"):
            print(f"    助记词: {r['mnemonic']}")
            extra = f"BIP39: {'OK' if r.get('bip39') else 'FAIL'}"
            if r.get("verify"):
                extra += f" | {r['verify']}"
            if r.get("password"):
                extra += f" | 口令: {r['password']!r}"
            print(f"    {extra}")
        else:
            if r.get("verify"):
                print(f"    {r['verify']}")
    for label, _ in RECOVERERS:
        if label not in printed:
            print(f"\n[{label}] 未检出")

    ok = [r for r in results if r.get("mnemonic")]
    uniq = {}
    for r in ok:
        uniq.setdefault(r["mnemonic"], []).append(r["wallet"])
    print("\n" + "=" * 92)
    print(f"汇总: 恢复 {len(ok)} 个钱包 / 唯一助记词 {len(uniq)} 组 | 用时 {time.time()-t0:.1f}s")
    for i, (mn, ws) in enumerate(uniq.items(), 1):
        print(f"  {i}. [{'/'.join(sorted(set(ws)))}] {mn}")

    if args.out:
        os.makedirs(args.out, exist_ok=True)
        name = os.path.splitext(os.path.basename(args.export))[0]
        with open(os.path.join(args.out, f"{name}_allin1.json"), "w", encoding="utf-8") as fh:
            json.dump({"file": os.path.basename(args.export), "device": dev,
                       "candidates": len(cands), "results": results,
                       "unique_mnemonics": list(uniq)}, fh, ensure_ascii=False, indent=2)
        md = [f"# 全合一恢复报告 — {os.path.basename(args.export)}", "",
              f"> 设备: {dev} | 候选口令 {len(cands)} 个 | 恢复 {len(ok)} 钱包 / {len(uniq)} 组助记词", ""]
        for r in results:
            md.append(f"## {r['wallet']} — {r['status']}")
            if r.get("mnemonic"):
                md.append(f"- 助记词: `{r['mnemonic']}`")
                md.append(f"- BIP39: {'OK' if r.get('bip39') else 'FAIL'}" + (f" | {r.get('verify','')}" if r.get('verify') else ""))
                if r.get("password"):
                    md.append(f"- 口令: `{r['password']}`")
            else:
                if r.get("verify"):
                    md.append(f"- {r['verify']}")
            md.append("")
        with open(os.path.join(args.out, f"{name}_allin1.md"), "w", encoding="utf-8") as fh:
            fh.write("\n".join(md))
        print(f"\n报告已保存: {name}_allin1.json / {name}_allin1.md")

if __name__ == "__main__":
    main()
