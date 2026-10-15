#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Trust Wallet iOS 沙箱导出取证工具：keystore 解密 -> BIP39 助记词 -> 地址派生对账。

环境提示：本工具刻意不 import base64 / pycryptodome（在部分沙箱里它们会把
stdout 替换为假报错），全部使用纯 Python + 标准库实现。
用法:
  python tw_keystore_tool.py EXPORT.json [--wordlist bip39_english.txt] [--out DIR] [--json OUT]
"""
import argparse
import hashlib
import json
import os
import sys
import unicodedata

B64A = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"


def b64d(s):
    out = bytearray()
    buf = 0
    bits = 0
    for ch in s.strip():
        if ch == "=":
            break
        buf = (buf << 6) | B64A.index(ch)
        bits += 6
        if bits >= 8:
            bits -= 8
            out.append((buf >> bits) & 0xFF)
    return bytes(out)


_RC = [0x0000000000000001, 0x0000000000008082, 0x800000000000808A, 0x8000000080008000,
       0x000000000000808B, 0x0000000080000001, 0x8000000080008081, 0x8000000000008009,
       0x000000000000008A, 0x0000000000000088, 0x0000000080008009, 0x000000008000000A,
       0x000000008000808B, 0x800000000000008B, 0x8000000000008089, 0x8000000000008003,
       0x8000000000008002, 0x8000000000000080, 0x000000000000800A, 0x800000008000000A,
       0x8000000080008081, 0x8000000000008080, 0x0000000080000001, 0x8000000080008008]
_ROT = [[0, 36, 3, 41, 18], [1, 44, 10, 45, 2], [62, 6, 43, 15, 61],
        [28, 55, 25, 21, 56], [27, 20, 39, 8, 14]]
_M64 = (1 << 64) - 1


def _rotl(x, n):
    return ((x << n) | (x >> (64 - n))) & _M64


def _keccakf(s):
    b = [0] * 25
    for rnd in _RC:
        C = [s[x] ^ s[x + 5] ^ s[x + 10] ^ s[x + 15] ^ s[x + 20] for x in range(5)]
        D = [C[(x - 1) % 5] ^ _rotl(C[(x + 1) % 5], 1) for x in range(5)]
        for x in range(5):
            for y in range(5):
                s[x + 5 * y] ^= D[x]
        for x in range(5):
            for y in range(5):
                b[y + 5 * ((2 * x + 3 * y) % 5)] = _rotl(s[x + 5 * y], _ROT[x][y])
        for x in range(5):
            for y in range(5):
                s[x + 5 * y] = b[x + 5 * y] ^ ((~b[(x + 1) % 5 + 5 * y]) & b[(x + 2) % 5 + 5 * y])
        s[0] ^= rnd


def keccak256(data):
    rate = 136
    pad = bytearray(data)
    pad.append(0x01)
    while len(pad) % rate != rate - 1:
        pad.append(0x00)
    pad.append(0x80)
    s = [0] * 25
    for off in range(0, len(pad), rate):
        blk = pad[off:off + rate]
        for i in range(0, rate, 8):
            s[i // 8] ^= int.from_bytes(blk[i:i + 8], "little")
        _keccakf(s)
    return b"".join(s[i].to_bytes(8, "little") for i in range(4))


_SBOX = [
    0x63, 0x7c, 0x77, 0x7b, 0xf2, 0x6b, 0x6f, 0xc5, 0x30, 0x01, 0x67, 0x2b, 0xfe, 0xd7, 0xab, 0x76,
    0xca, 0x82, 0xc9, 0x7d, 0xfa, 0x59, 0x47, 0xf0, 0xad, 0xd4, 0xa2, 0xaf, 0x9c, 0xa4, 0x72, 0xc0,
    0xb7, 0xfd, 0x93, 0x26, 0x36, 0x3f, 0xf7, 0xcc, 0x34, 0xa5, 0xe5, 0xf1, 0x71, 0xd8, 0x31, 0x15,
    0x04, 0xc7, 0x23, 0xc3, 0x18, 0x96, 0x05, 0x9a, 0x07, 0x12, 0x80, 0xe2, 0xeb, 0x27, 0xb2, 0x75,
    0x09, 0x83, 0x2c, 0x1a, 0x1b, 0x6e, 0x5a, 0xa0, 0x52, 0x3b, 0xd6, 0xb3, 0x29, 0xe3, 0x2f, 0x84,
    0x53, 0xd1, 0x00, 0xed, 0x20, 0xfc, 0xb1, 0x5b, 0x6a, 0xcb, 0xbe, 0x39, 0x4a, 0x4c, 0x58, 0xcf,
    0xd0, 0xef, 0xaa, 0xfb, 0x43, 0x4d, 0x33, 0x85, 0x45, 0xf9, 0x02, 0x7f, 0x50, 0x3c, 0x9f, 0xa8,
    0x51, 0xa3, 0x40, 0x8f, 0x92, 0x9d, 0x38, 0xf5, 0xbc, 0xb6, 0xda, 0x21, 0x10, 0xff, 0xf3, 0xd2,
    0xcd, 0x0c, 0x13, 0xec, 0x5f, 0x97, 0x44, 0x17, 0xc4, 0xa7, 0x7e, 0x3d, 0x64, 0x5d, 0x19, 0x73,
    0x60, 0x81, 0x4f, 0xdc, 0x22, 0x2a, 0x90, 0x88, 0x46, 0xee, 0xb8, 0x14, 0xde, 0x5e, 0x0b, 0xdb,
    0xe0, 0x32, 0x3a, 0x0a, 0x49, 0x06, 0x24, 0x5c, 0xc2, 0xd3, 0xac, 0x62, 0x91, 0x95, 0xe4, 0x79,
    0xe7, 0xc8, 0x37, 0x6d, 0x8d, 0xd5, 0x4e, 0xa9, 0x6c, 0x56, 0xf4, 0xea, 0x65, 0x7a, 0xae, 0x08,
    0xba, 0x78, 0x25, 0x2e, 0x1c, 0xa6, 0xb4, 0xc6, 0xe8, 0xdd, 0x74, 0x1f, 0x4b, 0xbd, 0x8b, 0x8a,
    0x70, 0x3e, 0xb5, 0x66, 0x48, 0x03, 0xf6, 0x0e, 0x61, 0x35, 0x57, 0xb9, 0x86, 0xc1, 0x1d, 0x9e,
    0xe1, 0xf8, 0x98, 0x11, 0x69, 0xd9, 0x8e, 0x94, 0x9b, 0x1e, 0x87, 0xe9, 0xce, 0x55, 0x28, 0xdf,
    0x8c, 0xa1, 0x89, 0x0d, 0xbf, 0xe6, 0x42, 0x68, 0x41, 0x99, 0x2d, 0x0f, 0xb0, 0x54, 0xbb, 0x16,
]


def _xtime(a):
    a <<= 1
    if a & 0x100:
        a ^= 0x11B
    return a & 0xFF


def _expand_key(key):
    rcon = [0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x1B, 0x36]
    w = [list(key[4 * i:4 * i + 4]) for i in range(4)]
    for i in range(4, 44):
        t = w[i - 1][:]
        if i % 4 == 0:
            t = t[1:] + t[:1]
            t = [_SBOX[b] for b in t]
            t[0] ^= rcon[i // 4 - 1]
        w.append([w[i - 4][j] ^ t[j] for j in range(4)])
    rks = []
    for r in range(11):
        rk = []
        for c in range(4):
            rk.extend(w[4 * r + c])
        rks.append(rk)
    return rks


def _enc_block(key, block):
    rks = _expand_key(key)
    s = [block[i] ^ rks[0][i] for i in range(16)]
    for r in range(1, 11):
        s = [_SBOX[b] for b in s]
        s = [s[0], s[5], s[10], s[15], s[4], s[9], s[14], s[3],
             s[8], s[13], s[2], s[7], s[12], s[1], s[6], s[11]]
        if r != 10:
            for c in range(4):
                a0, a1, a2, a3 = s[4 * c:4 * c + 4]
                s[4 * c + 0] = _xtime(a0) ^ (_xtime(a1) ^ a1) ^ a2 ^ a3
                s[4 * c + 1] = a0 ^ _xtime(a1) ^ (_xtime(a2) ^ a2) ^ a3
                s[4 * c + 2] = a0 ^ a1 ^ _xtime(a2) ^ (_xtime(a3) ^ a3)
                s[4 * c + 3] = (_xtime(a0) ^ a0) ^ a1 ^ a2 ^ _xtime(a3)
        s = [s[i] ^ rks[r][i] for i in range(16)]
    return bytes(s)


def aes_ctr(key, iv, data):
    out = bytearray()
    ctr = int.from_bytes(iv, "big")
    m128 = (1 << 128) - 1
    for off in range(0, len(data), 16):
        ks = _enc_block(key, ctr.to_bytes(16, "big"))
        blk = data[off:off + 16]
        out += bytes(a ^ b for a, b in zip(ks, blk))
        ctr = (ctr + 1) & m128
    return bytes(out)


def scrypt(pw, salt, n, r, p, dklen):
    return hashlib.scrypt(pw, salt=salt, n=n, r=r, p=p, dklen=dklen, maxmem=512 * 1024 * 1024)


_P = 2 ** 256 - 2 ** 32 - 977
_NN = 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141
_GX = 0x79BE667EF9DCBBAC55A06295CE870B07029BFCDB2DCE28D959F2815B16F81798
_GY = 0x483ADA7726A3C4655DA4FBFC0E1108A8FD17B448A68554199C47D08FFB10D4B8


def _inv(a):
    return pow(a, _P - 2, _P)


def _padd(pt1, pt2):
    if pt1 is None:
        return pt2
    if pt2 is None:
        return pt1
    x1, y1 = pt1
    x2, y2 = pt2
    if x1 == x2 and (y1 + y2) % _P == 0:
        return None
    if pt1 == pt2:
        lam = (3 * x1 * x1) * _inv(2 * y1) % _P
    else:
        lam = (y2 - y1) * _inv(x2 - x1) % _P
    x3 = (lam * lam - x1 - x2) % _P
    return (x3, (lam * (x1 - x3) - y1) % _P)


def _pmul(pt, k):
    r = None
    a = pt
    while k:
        if k & 1:
            r = _padd(r, a)
        a = _padd(a, a)
        k >>= 1
    return r


def pubkey(k):
    return _pmul((_GX, _GY), k)


def pub_comp(pt):
    return bytes([2 + (pt[1] & 1)]) + pt[0].to_bytes(32, "big")


def pub_uncomp(pt):
    return b"\x04" + pt[0].to_bytes(32, "big") + pt[1].to_bytes(32, "big")


def hmac_sha512(key, data):
    if len(key) > 128:
        key = hashlib.sha512(key).digest()
    key = key + b"\x00" * (128 - len(key))
    inner = hashlib.sha512(bytes(b ^ 0x36 for b in key) + data).digest()
    return hashlib.sha512(bytes(b ^ 0x5C for b in key) + inner).digest()


def bip32_master(seed):
    i = hmac_sha512(b"Bitcoin seed", seed)
    return int.from_bytes(i[:32], "big"), i[32:]


def ckdpriv(priv, cc, index):
    if index >= 0x80000000:
        data = b"\x00" + priv.to_bytes(32, "big") + index.to_bytes(4, "big")
    else:
        data = pub_comp(pubkey(priv)) + index.to_bytes(4, "big")
    i = hmac_sha512(cc, data)
    return (int.from_bytes(i[:32], "big") + priv) % _NN, i[32:]


def derive_path(seed, path):
    priv, cc = bip32_master(seed)
    for index in path:
        priv, cc = ckdpriv(priv, cc, index)
    return priv


def derive_from_master(priv, cc, path):
    for index in path:
        priv, cc = ckdpriv(priv, cc, index)
    return priv


def parse_path(p):
    toks = str(p).split("/")
    if toks and toks[0] in ("m", "M"):
        toks = toks[1:]
    out = []
    for t in toks:
        t = t.strip()
        if not t:
            continue
        hard = t.endswith("'")
        n = int(t[:-1]) if hard else int(t)
        out.append(n + 0x80000000 if hard else n)
    return out


def pub_normalized(priv):
    pt = pubkey(priv)
    return pub_comp(pt), pub_uncomp(pt)


def stored_pub_matches(comp, uncomp, stored):
    if not stored:
        return False
    s = stored.lower()
    return s == comp.hex().lower() or s == uncomp.hex().lower()


def verify_accounts(active, priv, cc):
    checks = []
    for a in active or []:
        coin = a.get("coin")
        path = a.get("derivationPath") or ""
        addr = a.get("address")
        pk = a.get("publicKey")
        r = {"coin": coin, "path": path, "address": addr, "pub_len": len(pk) if pk else 0, "status": "unknown"}
        try:
            idxs = parse_path(path)
            child = derive_from_master(priv, cc, idxs)
        except Exception:
            r["status"] = "path-error"
            checks.append(r)
            continue
        comp, uncomp = pub_normalized(child)
        r["derived_pub"] = comp.hex()[:66]
        r["stored_pub"] = pk
        matched = stored_pub_matches(comp, uncomp, pk)
        r["pubkey_match"] = matched
        is_eth_path = path.startswith("m/44'/60'")
        if is_eth_path:
            ah = eth_address(child)[0]
            r["derived_eth"] = ah
            norm = str(addr).replace("ronin:", "").lower()
            r["address_match"] = norm == ah.lower()
        if coin == 195 or path.startswith("m/44'/195'"):
            ta = tron_address(child)
            r["derived_tron"] = ta
            r["address_match"] = str(addr) == ta
        if matched:
            r["status"] = "secp-ok"
        elif pk and len(pk) == 64:
            r["status"] = "non-secp(64B raw)"
        elif pk and len(pk) not in (66, 130):
            r["status"] = "non-secp(%dB)" % (len(pk) // 2)
        else:
            r["status"] = "NO-match"
        checks.append(r)
    return checks


def mnemonic_to_seed(mn, passphrase=""):
    mn_nfkd = unicodedata.normalize("NFKD", mn)
    salt = unicodedata.normalize("NFKD", "mnemonic" + passphrase)
    return hashlib.pbkdf2_hmac("sha512", mn_nfkd.encode("utf-8"), salt.encode("utf-8"), 2048, 64)


def eth_address(priv):
    pt = pubkey(priv)
    h = keccak256(pub_uncomp(pt)[1:])
    ah = h[-20:].hex()
    hh = keccak256(ah.encode()).hex()
    out = ""
    for i, c in enumerate(ah):
        out += c if c.isdigit() or int(hh[i], 16) < 8 else c.upper()
    return "0x" + out, pub_uncomp(pt).hex()


_B58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"


def b58check(data):
    checksum = hashlib.sha256(hashlib.sha256(data).digest()).digest()[:4]
    payload = data + checksum
    n = int.from_bytes(payload, "big")
    s = ""
    while n:
        n, r = divmod(n, 58)
        s = _B58[r] + s
    pad = len(payload) - len(payload.lstrip(b"\x00"))
    return "1" * pad + s


def tron_address(priv):
    pt = pubkey(priv)
    raw = b"\x41" + keccak256(pub_uncomp(pt)[1:])[-20:]
    return b58check(raw)


def hash160(b):
    return hashlib.new("ripemd160", hashlib.sha256(b).digest()).digest()


_GEN = [0x3B6A57B2, 0x26508E6D, 0x1EA119FA, 0x3D4233DD, 0x2A1462B3]
_CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"


def _polymod(values):
    chk = 1
    for v in values:
        b = chk >> 25
        chk = (chk & 0x1FFFFFF) << 5 ^ v
        for i in range(5):
            chk ^= _GEN[i] if ((b >> i) & 1) else 0
    return chk


def _bech32_hrp_expand(hrp):
    return [ord(x) >> 5 for x in hrp] + [0] + [ord(x) & 31 for x in hrp]


def _bech32_checksum(hrp, data):
    values = _bech32_hrp_expand(hrp) + data
    pm = _polymod(values + [0, 0, 0, 0, 0, 0]) ^ 1
    return [(pm >> 5 * (5 - i)) & 31 for i in range(6)]


def _convertbits(data, frombits, tobits, pad=True):
    acc = 0
    bits = 0
    ret = []
    maxv = (1 << tobits) - 1
    for value in data:
        acc = (acc << frombits) | value
        bits += frombits
        while bits >= tobits:
            bits -= tobits
            ret.append((acc >> bits) & maxv)
    if pad and bits:
        ret.append((acc << (tobits - bits)) & maxv)
    return ret


def bech32_encode(hrp, data):
    combined = data + _bech32_checksum(hrp, data)
    return hrp + "1" + "".join(_CHARSET[x] for x in combined)


def p2wpkh(priv, hrp):
    prog = hash160(pub_comp(pubkey(priv)))
    return bech32_encode(hrp, [0] + _convertbits(prog, 8, 5))


REPAIR_PREFIX = '{"big": {"keychain": {"genp": {"count": 0, "items": [{"'


def load_export(path):
    raw = open(path, encoding="utf-8").read()
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return json.loads(REPAIR_PREFIX + raw)


def find_keystore_files(obj):
    out = {}
    for k, v in (obj.get("sandbox") or {}).items():
        if not isinstance(v, dict):
            continue
        for fpath, val in v.items():
            if isinstance(val, str) and "/keystore/UTC" in fpath:
                out[fpath] = val
    return out


def password_candidates(obj):
    found = {}
    tables = []
    # 新格式: keychain.wallets.<app>.items 与 keychain.tables.<table>.items
    kc = obj.get("keychain") or {}
    for top in ("wallets", "tables"):
        topv = kc.get(top)
        if isinstance(topv, dict):
            for name, sub in topv.items():
                if isinstance(sub, dict) and isinstance(sub.get("items"), list):
                    tables.append(sub["items"])
    # 旧格式: big.wallets.<app> 与 big.keychain.<app>
    big = obj.get("big") or {}
    for top in ("wallets", "keychain"):
        topv = big.get(top)
        if not isinstance(topv, dict):
            continue
        for app, sub in topv.items():
            if isinstance(sub, dict) and isinstance(sub.get("items"), list):
                tables.append(sub["items"])
    for items in tables:
        for it in items:
            acct = it.get("account")
            dh = it.get("dataHex")
            if not isinstance(acct, str) or not isinstance(dh, str):
                continue
            if not acct.startswith("trustwalletUTC--"):
                continue
            try:
                pw = bytes.fromhex(dh).decode("ascii")
            except Exception:
                continue
            if len(pw) < 8 or not all(32 <= ord(c) < 127 for c in pw):
                continue
            found.setdefault(acct, pw)
    return found


def target_password(account_map, fpath):
    fname = fpath.split("/")[-1]
    full_acct = "trustwallet" + fname
    for cand in (full_acct, fname):
        if cand in account_map:
            return account_map[cand]
    return None


def decrypt_keystore(raw_bytes, pw):
    ks = json.loads(raw_bytes.decode("utf-8"))
    cr = ks["crypto"]
    kp = cr["kdfparams"]
    salt = bytes.fromhex(kp["salt"])
    iv = bytes.fromhex(cr["cipherparams"]["iv"])
    mac = bytes.fromhex(cr["mac"])
    ctext = bytes.fromhex(cr["ciphertext"])
    dk = scrypt(pw, salt=salt, n=int(kp["n"]), r=int(kp["r"]), p=int(kp["p"]), dklen=int(kp["dklen"]))
    mac_ok = keccak256(dk[16:32] + ctext) == mac
    plain = aes_ctr(dk[:16], iv, ctext) if mac_ok else None
    return ks, mac_ok, plain


def bip39_validate(mn, wlist):
    if wlist is None:
        return (True, "wordlist missing")
    words = mn.split()
    wset = set(wlist)
    if not all(w in wset for w in words):
        return (False, "word not in list")
    idx = {w: i for i, w in enumerate(wlist)}
    bits = "".join(format(idx[w], "011b") for w in words)
    total = len(words) * 11
    ent_bits = total * 32 // 33
    ent = int(bits[:ent_bits], 2).to_bytes(ent_bits // 8, "big")
    cs = int(bits[ent_bits:], 2)
    h = hashlib.sha256(ent).digest()
    expect = h[0] >> (8 - (total - ent_bits))
    return (cs == expect, "checksum ok" if cs == expect else "checksum FAIL")


DERIVE_CHECKS = [
    ("coin 60 ETH", "m/44'/60'/0'/0/0", [0x80000000 + 44, 0x80000000 + 60, 0x80000000, 0, 0], "eth"),
    ("coin 0 BTC", "m/84'/0'/0'/0/0", [0x80000000 + 84, 0x80000000, 0x80000000, 0, 0], "bc"),
    ("coin 2 LTC", "m/84'/2'/0'/0/0", [0x80000000 + 84, 0x80000000 + 2, 0x80000000, 0, 0], "ltc"),
]


def active_to_pub(ks):
    m = {}
    for a in ks.get("activeAccounts") or []:
        pk = a.get("publicKey")
        if isinstance(pk, str) and len(pk) >= 66:
            try:
                m[a["coin"]] = bytes.fromhex(pk)
            except Exception:
                pass
    return m


def run_wallet(fpath, encoded, account_map, wordlist, verbose=False):
    r = {"file": fpath, "candidates_tried": 0}
    try:
        raw = b64d(encoded)
    except Exception as e:
        r["error"] = "b64: %s" % e
        return r
    pw = target_password(account_map, fpath)
    pwlist = [pw] if pw else []
    r["target_password"] = pw
    if verbose:
        print("  password(by account): %s" % (pw or "none"))
    pwlist += [v for v in set(account_map.values()) if v != pw]
    for cand in pwlist:
        if not isinstance(cand, bytes):
            cand = cand.encode("utf-8")
        r["candidates_tried"] += 1
        try:
            ks, ok, plain = decrypt_keystore(raw, cand)
        except Exception as e:
            continue
        if not ok:
            continue
        r["mac_match"] = True
        r["password_used"] = cand.decode("utf-8", "replace")
        r["keystore"] = {"id": ks.get("id"), "type": ks.get("type"), "version": ks.get("version"),
                         "cipher": ks["crypto"].get("cipher"), "kdf": ks["crypto"].get("kdf")}
        txt = plain.decode("utf-8", "replace")
        r["plaintext"] = txt
        words = txt.split()
        valid = all(len(w) >= 3 and w.isalnum() for w in words) and len(words) in (12, 15, 18, 21, 24)
        r["looks_like_mnemonic"] = valid
        if valid:
            r["mnemonic"] = " ".join(words)
            okbip, msg = bip39_validate(" ".join(words), wordlist)
            r["bip39"] = {"ok": okbip, "msg": msg}
            seed = mnemonic_to_seed(" ".join(words))
            r["seed_hex"] = seed.hex()
            mast_priv, mast_cc = bip32_master(seed)
            r["account_checks"] = verify_accounts(ks.get("activeAccounts"), mast_priv, mast_cc)
            active_pub = active_to_pub(ks)
            derivations = []
            for label, path_str, path, method in DERIVE_CHECKS:
                priv = derive_path(seed, path)
                if method == "eth":
                    addr, pubhex = eth_address(priv)
                    pubbytes = bytes.fromhex(pubhex)
                else:
                    addr = p2wpkh(priv, method)
                    pubbytes = pub_comp(pubkey(priv))
                coin = 60 if method == "eth" else (0 if method == "bc" else 2)
                stored = active_pub.get(coin)
                match = "n/a"
                if stored is not None:
                    match = (stored == pubbytes) or (stored in (pubbytes, _maybe_uncomp(stored, pubbytes)))
                derivations.append({"label": label, "path": path_str, "address": addr,
                                    "pubkey": pubbytes.hex()[:66], "active_account_match": match})
            r["derivations"] = derivations
        if verbose:
            print("  mac=MATCH pw=%s plain=%r" % (r["password_used"], txt))
        return r
    r["error"] = "no password matched MAC"
    return r


def _maybe_uncomp(a, b):
    if len(a) == 65 and len(b) == 33:
        return a[1:]
    if len(a) == 33 and len(b) == 65:
        return a
    return None


def load_wordlist(path):
    if not path:
        for cand in (os.path.join(os.path.dirname(os.path.abspath(__file__)), "bip39_english.txt"),
                     r"C:\Users\VIP\Desktop\data\bip39_english.txt"):
            if os.path.exists(cand):
                path = cand
                break
    if not path or not os.path.exists(path):
        return None
    return open(path, encoding="utf-8").read().split()


def _query_filter(a, coin, addr):
    if coin is not None and int(a.get("coin") or -1) != coin:
        return False
    if addr:
        hay = " ".join(str(x or "") for x in (a.get("address"), a.get("derived_pub"), a.get("derived_eth", ""), a.get("derived_tron", "")))
        if addr.lower() not in hay.lower():
            return False
    return True


def _status_summary(checks):
    counts = {}
    for c in checks:
        counts[c["status"]] = counts.get(c["status"], 0) + 1
    return counts


def main():
    ap = argparse.ArgumentParser(description="Trust Wallet keystore export forensics tool")
    ap.add_argument("export", help="path to the exported keychain JSON")
    ap.add_argument("--wordlist", default=None, help="BIP39 english wordlist file")
    ap.add_argument("--out", default=None, help="directory for reports (default: stdout only)")
    ap.add_argument("--json", default=None, help="write machine-readable JSON summary to this path")
    ap.add_argument("--coin", type=int, default=None, help="query: only show accounts with this coin id")
    ap.add_argument("--addr", default=None, help="query: only show accounts whose address/pubkey contains this")
    ap.add_argument("--verbose", action="store_true")
    args = ap.parse_args()

    obj = load_export(args.export)
    ks_files = find_keystore_files(obj)
    accounts = password_candidates(obj)
    wl = load_wordlist(args.wordlist)
    if args.out:
        os.makedirs(args.out, exist_ok=True)

    print("== Trust Wallet keystore extraction ==")
    print("export: %s" % args.export)
    print("keystore files: %d" % len(ks_files))
    print("keychain password accounts: %d" % len(accounts))
    if args.coin is not None:
        print("query coin: %d" % args.coin)
    if args.addr:
        print("query addr: %s" % args.addr)
    print("wordlist: %s" % (wl and os.path.basename(args.wordlist or "bip39_english.txt") or "MISSING"))
    print()

    results = []
    for fpath, encoded in ks_files.items():
        print("[*] %s" % fpath)
        r = run_wallet(fpath, encoded, accounts, wl, verbose=args.verbose)
        results.append(r)
        if r.get("mnemonic"):
            print("    MNEMONIC: %s" % r["mnemonic"])
            print("    bip39: %s" % r["bip39"]["msg"])
            for d in r.get("derivations", []):
                print("    %-15s %-20s %s  match=%s" % (d["label"], d["address"], d["pubkey"][:16] + "...", d["active_account_match"]))
            checks = r.get("account_checks") or []
            filt = [c for c in checks if _query_filter(c, args.coin, args.addr)]
            prints = filt if args.coin is not None or args.addr or args.verbose else [c for c in checks if c["status"] not in ("secp-ok",)][:40]
            for c in prints:
                extra = " eth=%s addr_m=%s" % (c.get("derived_eth", ""), c.get("address_match")) if "derived_eth" in c else ""
                if "derived_tron" in c:
                    extra = " tron=%s addr_m=%s" % (c.get("derived_tron"), c.get("address_match"))
                if c["status"] == "secp-ok":
                    tail = ""
                else:
                    tail = " stored=%s" % str(c.get("stored_pub") or "")[:18]
                print("    %-10s %-22s %-45s %-16s %s%s%s" % (c["coin"], c["path"], str(c["address"])[:44], c["status"], (c.get("derived_pub") or "")[:12], tail, extra))
            print("    [%d accounts] status: %s" % (len(checks), _status_summary(checks)))
        else:
            print("    %s" % (r.get("error") or "no result"))
        print()

    if args.json:
        with open(args.json, "w", encoding="utf-8") as fh:
            json.dump(results, fh, ensure_ascii=False, indent=2)
        print("json summary: %s" % args.json)

    if args.out:
        for r in results:
            if not r.get("mnemonic"):
                continue
            fname = os.path.basename(r["file"]).replace(":", "_")
            with open(os.path.join(args.out, fname + ".txt"), "w", encoding="utf-8") as fh:
                fh.write("FILE: %s\n" % r["file"])
                fh.write("PASSWORD: %s\n" % r["password_used"])
                fh.write("MNEMONIC: %s\n" % r["mnemonic"])
                fh.write("BIP39: %s\n\n" % r["bip39"]["msg"])
                for d in r.get("derivations", []):
                    fh.write("%s %s %s match=%s\n" % (d["label"], d["path"], d["address"], d["active_account_match"]))
                checks = r.get("account_checks") or []
                fh.write("\n[full account ledger %d rows]\n" % len(checks))
                for c in checks:
                    row = "%s\t%s\t%s\t%s\t%s" % (c["coin"], c["path"], c.get("derived_eth") or c.get("derived_tron") or c.get("address") or "", c.get("status"), c.get("derived_pub") or "")
                    if c.get("address") is not None:
                        row += "\t%s" % c["address"]
                    fh.write(row + "\n")
        print("per-wallet reports: %s" % args.out)


if __name__ == "__main__":
    main()