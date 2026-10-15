# -*- coding: utf-8 -*-
"""chainquery/engine.py — 多链地址派生 + 余额/代币/稳定币查询引擎

特点（吸取实战教训）:
  - 每条链多 RPC 回退 + 429 退避重试 + 超时保护
  - 稳定币（USDT/USDC）直连合约 balanceOf，不依赖浏览器 API 列表
  - EVM 代币走 Blockscout；SOL 走全 SPL 账户；TRON 走全 TRC-20 扫描
  - 单链失败不影响整体（错误按链记录）
依赖: cryptography（ed25519）；其余纯标准库
"""
import hashlib, hmac, json, os, re, socket, ssl, sys, time, urllib.request
import concurrent.futures
from urllib.error import HTTPError

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import tw_keystore_tool as tw

try:
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
    from cryptography.hazmat.primitives import serialization
    HAVE_ED = True
except Exception:
    HAVE_ED = False

UA = {"User-Agent": "chainquery-lab/1.0"}
TIMEOUT = 20
# TronGrid API Key（环境变量 TRONGRID_API_KEY 可覆盖）
TRONGRID_KEY = os.environ.get("TRONGRID_API_KEY", "69785618-d0bc-436d-9753-7c134cb8f8ea")
TRON_HEADERS = {"TRON-PRO-API-KEY": TRONGRID_KEY} if TRONGRID_KEY else {}

# ---------------------------------------------------------------- HTTP
def _http_get(url, tries=3, headers=None):
    h = dict(UA)
    if headers:
        h.update(headers)
    last = None
    for i in range(tries):
        try:
            req = urllib.request.Request(url, headers=h)
            with urllib.request.urlopen(req, timeout=TIMEOUT) as r:
                return r.read().decode("utf-8", "replace")
        except HTTPError as e:
            last = e
            time.sleep((1.5 * (i + 1)) * (3 if e.code == 429 else 1))
        except Exception as e:
            last = e
            time.sleep(0.8 * (i + 1))
    raise last

def _trongrid_get(url):
    """TronGrid GET：优先带 API Key；Key 无效(401/403)时自动回退匿名模式"""
    if TRON_HEADERS:
        try:
            return _http_get(url, tries=2, headers=TRON_HEADERS)
        except HTTPError as e:
            if e.code not in (401, 403):
                raise
    return _http_get(url, tries=3)

def _trongrid_post(url, payload):
    """TronGrid POST：同上回退逻辑"""
    if TRON_HEADERS:
        try:
            return _http_post(url, payload, tries=1, headers=TRON_HEADERS)
        except HTTPError as e:
            if e.code not in (401, 403):
                raise
    return _http_post(url, payload, tries=2)

def _http_post(url, payload, tries=3, headers=None):
    data = json.dumps(payload).encode()
    h = {**UA, "Content-Type": "application/json"}
    if headers:
        h.update(headers)
    last = None
    for i in range(tries):
        try:
            req = urllib.request.Request(url, data=data, headers=h)
            with urllib.request.urlopen(req, timeout=TIMEOUT) as r:
                return r.read().decode("utf-8", "replace")
        except HTTPError as e:
            last = e
            time.sleep((1.5 * (i + 1)) * (3 if e.code == 429 else 1))
        except Exception as e:
            last = e
            time.sleep(0.8 * (i + 1))
    raise last

# ---------------------------------------------------------------- 派生
B58A = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

def b58enc(b):
    n = int.from_bytes(b, "big"); s = ""
    while n:
        n, r = divmod(n, 58); s = B58A[r] + s
    return "1" * (len(b) - len(b.lstrip(b"\x00"))) + s

def b58dec(s):
    n = 0
    for c in s:
        n = n * 58 + B58A.index(c)
    b = n.to_bytes((n.bit_length() + 7) // 8, "big")
    return b"\x00" * (len(s) - len(s.lstrip("1"))) + b

def slip10_ed(seed, path):
    I = hmac.new(b"ed25519 seed", seed, hashlib.sha512).digest()
    k, c = I[:32], I[32:]
    for idx in path:
        I = hmac.new(c, b"\x00" + k + (idx | 0x80000000).to_bytes(4, "big"), hashlib.sha512).digest()
        k, c = I[:32], I[32:]
    return k

def ed_pub(k):
    return Ed25519PrivateKey.from_private_bytes(k).public_key().public_bytes(
        serialization.Encoding.Raw, serialization.PublicFormat.Raw)

def H(p): return [0x80000000 + x for x in p]

def derive_secp(mn, path):
    return tw.derive_path(tw.mnemonic_to_seed(mn), tw.parse_path(path))

def p2pkh(priv, ver):
    return tw.b58check(bytes([ver]) + tw.hash160(tw.pub_comp(tw.pubkey(priv))))

def derive_all(mnemonic):
    """标准派生路径 → 全链地址字典"""
    out = {}
    out["BTC"] = tw.p2wpkh(derive_secp(mnemonic, "m/84'/0'/0'/0/0"), "bc")
    out["ETH"] = tw.eth_address(derive_secp(mnemonic, "m/44'/60'/0'/0/0"))[0]
    out["LTC"] = tw.p2wpkh(derive_secp(mnemonic, "m/84'/2'/0'/0/0"), "ltc")
    out["DOGE"] = p2pkh(derive_secp(mnemonic, "m/44'/3'/0'/0/0"), 0x1e)
    out["BCH"] = tw.b58check(bytes([0x00]) + tw.hash160(tw.pub_comp(tw.pubkey(derive_secp(mnemonic, "m/44'/145'/0'/0/0")))))
    out["TRON"] = tw.tron_address(derive_secp(mnemonic, "m/44'/195'/0'/0/0"))
    if HAVE_ED:
        seed = tw.mnemonic_to_seed(mnemonic)
        out["SOL"] = b58enc(ed_pub(slip10_ed(seed, H([44, 501, 0, 0]))))
        out["SOL_legacy"] = b58enc(ed_pub(slip10_ed(seed, H([44, 501, 0]))))
    return out

# ---------------------------------------------------------------- 链配置
RPC = {
    "ETH": ["https://cloudflare-eth.com", "https://eth.llamarpc.com", "https://ethereum-rpc.publicnode.com",
            "https://rpc.ankr.com/eth", "https://eth.drpc.org", "https://1rpc.io/eth"],
    "BSC": ["https://bsc-dataseed.binance.org", "https://bsc-dataseed1.defibit.io",
            "https://bsc-rpc.publicnode.com", "https://binance.llamarpc.com", "https://rpc.ankr.com/bsc"],
    "Base": ["https://mainnet.base.org", "https://base-rpc.publicnode.com", "https://base.llamarpc.com",
             "https://base.drpc.org"],
    "Polygon": ["https://polygon-bor-rpc.publicnode.com", "https://polygon.llamarpc.com",
                "https://polygon-rpc.com", "https://rpc.ankr.com/polygon", "https://polygon.drpc.org"],
    "Arbitrum": ["https://arb1.arbitrum.io/rpc", "https://arbitrum-one-rpc.publicnode.com",
                 "https://arbitrum.llamarpc.com", "https://rpc.ankr.com/arbitrum"],
    "Optimism": ["https://mainnet.optimism.io", "https://optimism-rpc.publicnode.com",
                 "https://optimism.llamarpc.com", "https://rpc.ankr.com/optimism"],
    "AVAX": ["https://api.avax.network/ext/bc/C/rpc", "https://avalanche-c-chain-rpc.publicnode.com",
             "https://avalanche.drpc.org"],
    "Linea": ["https://rpc.linea.build", "https://linea-rpc.publicnode.com"],
    "Scroll": ["https://rpc.scroll.io", "https://scroll-rpc.publicnode.com"],
    "zkSync": ["https://mainnet.era.zksync.io", "https://zksync-era-rpc.publicnode.com"],
    "Fantom": ["https://rpc.ftm.tools", "https://fantom-rpc.publicnode.com"],
    "Celo": ["https://forno.celo.org", "https://celo-rpc.publicnode.com"],
    "Cronos": ["https://evm.cronos.org"],
    "Gnosis": ["https://rpc.gnosischain.com", "https://gnosis-rpc.publicnode.com"],
}
EVM_CHAINS = list(RPC.keys())
EVM_SYM = {"ETH": "ETH", "BSC": "BNB", "Base": "ETH", "Polygon": "POL",
           "Arbitrum": "ETH", "Optimism": "ETH", "AVAX": "AVAX",
           "Linea": "ETH", "Scroll": "ETH", "zkSync": "ETH",
           "Fantom": "FTM", "Celo": "CELO", "Cronos": "CRO", "Gnosis": "xDAI"}
PRICE_SYM = {"ETH": "ETHUSDT", "BSC": "BNBUSDT", "Base": "ETHUSDT", "Polygon": "POLUSDT",
             "Arbitrum": "ETHUSDT", "Optimism": "ETHUSDT", "AVAX": "AVAXUSDT",
             "Linea": "ETHUSDT", "Scroll": "ETHUSDT", "zkSync": "ETHUSDT",
             "Fantom": "FTMUSDT", "Celo": "CELOUSDT", "Cronos": "CROUSDT",
             "BTC": "BTCUSDT", "LTC": "LTCUSDT", "DOGE": "DOGEUSDT", "TRON": "TRXUSDT", "SOL": "SOLUSDT"}
CHAIN_LIST = ["BTC", "ETH", "BSC", "Base", "Polygon", "Arbitrum", "Optimism", "AVAX",
              "Linea", "Scroll", "zkSync", "Fantom", "Celo", "Cronos", "Gnosis",
              "LTC", "DOGE", "BCH", "TRON", "SOL", "SOL(旧路径)"]

STABLES = [
    ("USDT", "ETH", "0xdAC17F958D2ee523a2206206994597C13D831ec7", 6),
    ("USDT", "BSC", "0x55d398326f99059fF775485246999027B3197955", 18),
    ("USDT", "Polygon", "0xc2132D05D31c914a87C6611C10748AEb04B58e8F", 6),
    ("USDT", "Arbitrum", "0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9", 6),
    ("USDT", "Base", "0xfde4C96c8593536E31F229EA8f37b2ADa2699bb2", 6),
    ("USDT", "Optimism", "0x94b008aA00579c1307B0EF2c499aD98a8ce58e58", 6),
    ("USDT", "Linea", "0xA219439258ca9da29E9Cc4cE5596924745e12B93", 6),
    ("USDC", "ETH", "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", 6),
    ("USDC", "BSC", "0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d", 18),
    ("USDC", "Base", "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", 6),
    ("USDC", "Polygon", "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359", 6),
    ("USDC", "Arbitrum", "0xaf88d065e77c8cC2239327C5EDb3A432268e5831", 6),
    ("USDC", "Optimism", "0x0b2C639c533813f4Aa9D7837CAf62653d097Ff85", 6),
    ("USDC", "Linea", "0x176211869cA2b568f2A7D4EE941E073a821EE1ff", 6),
]
BLOCKSCOUT = {
    "ETH": "https://eth.blockscout.com",
    "Base": "https://base.blockscout.com",
    "Polygon": "https://polygon.blockscout.com",
    "Arbitrum": "https://arbitrum.blockscout.com",
    "Optimism": "https://optimism.blockscout.com",
    "Linea": "https://linea.blockscout.com",
    "Scroll": "https://scroll.blockscout.com",
    "zkSync": "https://zksync.blockscout.com",
}

# ---------------------------------------------------------------- 原生币查询
def q_btc(a):
    for base in ("https://mempool.space", "https://blockstream.info"):
        try:
            j = json.loads(_http_get(f"{base}/api/address/{a}", tries=2))
            bal = j["chain_stats"]["funded_txo_sum"] - j["chain_stats"]["spent_txo_sum"]
            return bal / 1e8, f"{bal/1e8:.8f} BTC"
        except Exception:
            continue
    return None, "查询失败"

def q_ltc(a):
    for base in ("https://litecoinspace.org", "https://blockstream.info"):
        try:
            if "litecoinspace" in base:
                j = json.loads(_http_get(f"{base}/api/address/{a}", tries=2))
                bal = j["chain_stats"]["funded_txo_sum"] - j["chain_stats"]["spent_txo_sum"]
                return bal / 1e8, f"{bal/1e8:.8f} LTC"
            j = json.loads(_http_get(f"https://api.blockcypher.com/v1/ltc/main/addrs/{a}/balance", tries=2))
            bal = j.get("final_balance", 0)
            return bal / 1e8, f"{bal/1e8:.8f} LTC"
        except Exception:
            continue
    return None, "查询失败"

def q_doge(a):
    for src in (f"https://api.blockcypher.com/v1/doge/main/addrs/{a}/balance",
                f"https://dogechain.info/api/v1/address/balance/{a}"):
        try:
            j = json.loads(_http_get(src, tries=2))
            if "final_balance" in j:
                return j["final_balance"] / 1e8, f"{j['final_balance']/1e8:.8f} DOGE"
            if "balance" in j:
                return float(j["balance"]), f"{float(j['balance']):.8f} DOGE"
        except Exception:
            continue
    return None, "查询失败"

def q_bch(legacy_addr):
    try:
        payload = b58dec(legacy_addr)[:-4]
        h160 = payload[1:21]
    except Exception:
        return None, "地址解析失败"
    script = bytes([0x76, 0xA9, 0x14]) + h160 + bytes([0x88, 0xAC])
    sh = hashlib.sha256(script).digest()[::-1].hex()
    for server in ("bch.imaginary.cash", "electroncash.dk"):
        try:
            ctx = ssl.create_default_context(); ctx.check_hostname = False; ctx.verify_mode = ssl.CERT_NONE
            raw = socket.create_connection((server, 50002), timeout=20)
            s = ctx.wrap_socket(raw, server_hostname=server)
            s.sendall((json.dumps({"id": 1, "method": "blockchain.scripthash.get_balance", "params": [sh]}) + "\n").encode())
            buf = b""; s.settimeout(20)
            while b"\n" not in buf:
                chunk = s.recv(4096)
                if not chunk:
                    break
                buf += chunk
            s.close()
            j = json.loads(buf.decode().strip().splitlines()[0])
            if "result" in j:
                conf = j["result"].get("confirmed", 0)
                return conf / 1e8, f"{conf/1e8:.8f} BCH"
        except Exception:
            continue
    return None, "查询失败"

def q_trx(a):
    try:
        j = json.loads(_trongrid_get(f"https://api.trongrid.io/v1/accounts/{a}"))
        if j.get("data"):
            sun = j["data"][0].get("balance", 0)
            return sun / 1e6, f"{sun/1e6:.6f} TRX"
        return 0.0, "0 TRX（未激活）"
    except Exception:
        return None, "查询失败"

def q_sol(a):
    try:
        j = json.loads(_http_post("https://api.mainnet-beta.solana.com",
                                  {"jsonrpc": "2.0", "id": 1, "method": "getBalance", "params": [a]}))
        lam = j["result"]["value"]
        return lam / 1e9, f"{lam/1e9:.9f} SOL"
    except Exception:
        return None, "查询失败"

def q_evm(a, chain):
    sym = EVM_SYM[chain]
    for rpc in RPC[chain]:
        try:
            j = json.loads(_http_post(rpc, {"jsonrpc": "2.0", "id": 1, "method": "eth_getBalance", "params": [a, "latest"]}, tries=2))
            wei = int(j["result"], 16)
            return wei / 1e18, f"{wei/1e18:.8f} {sym}"
        except Exception:
            continue
    return None, "查询失败"

# ---------------------------------------------------------------- 代币查询
def tokens_blockscout(eth_addr, chain):
    out = []
    base = BLOCKSCOUT.get(chain)
    if not base:
        return out
    try:
        j = json.loads(_http_get(f"{base}/api/v2/addresses/{eth_addr}/token-balances", tries=2))
        for t in j:
            tok = t.get("token", {})
            dec = int(tok.get("decimals") or 18)
            val = int(t.get("value", 0)) / (10 ** dec)
            if val < 1e-9:
                continue
            xr = tok.get("exchange_rate")
            price = float(xr) if xr not in (None, "", "null") else None
            out.append({"chain": chain, "symbol": tok.get("symbol") or "?", "amount": val,
                        "usd": val * price if price else None,
                        "contract": tok.get("address_hash"), "source": "blockscout"})
    except Exception:
        pass
    return out

def balance_of(token, rpc_list, addr, dec):
    data = "0x70a08231" + "0" * 24 + addr[2:].lower()
    for rpc in rpc_list:
        try:
            j = json.loads(_http_post(rpc, {"jsonrpc": "2.0", "id": 1, "method": "eth_call",
                                            "params": [{"to": token, "data": data}, "latest"]}, tries=2))
            return int(j["result"], 16) / (10 ** dec)
        except Exception:
            continue
    return None

def tokens_stables(eth_addr, chains):
    out = []
    for sym, chain, contract, dec in STABLES:
        if chains and chain not in chains:
            continue
        b = balance_of(contract, RPC[chain], eth_addr, dec)
        if b and b > 0:
            out.append({"chain": chain, "symbol": sym, "amount": b, "usd": b if sym in ("USDT", "USDC") else None,
                        "contract": contract, "source": "balanceOf"})
    return out

def tokens_spl(sol_addr):
    out = []
    try:
        j = json.loads(_http_post("https://api.mainnet-beta.solana.com", {
            "jsonrpc": "2.0", "id": 1, "method": "getTokenAccountsByOwner",
            "params": [sol_addr, {"programId": "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"}, {"encoding": "jsonParsed"}]}, tries=2))
        for a in j.get("result", {}).get("value", []):
            info = a["account"]["data"]["parsed"]["info"]
            amt = info["tokenAmount"]
            if float(amt["uiAmountString"]) > 0:
                out.append({"chain": "SOL", "symbol": None, "amount": float(amt["uiAmountString"]),
                            "usd": None, "contract": info["mint"], "source": "SPL"})
    except Exception:
        pass
    return out

_TRC20_META_CACHE = {}

def _trc20_meta(contract):
    """TRC-20 合约元数据（symbol/decimals），带缓存"""
    if contract in _TRC20_META_CACHE:
        return _TRC20_META_CACHE[contract]
    meta = {"symbol": None, "decimals": None}
    for fn, key in (("symbol()", "symbol"), ("decimals()", "decimals")):
        try:
            j = json.loads(_trongrid_post("https://api.trongrid.io/wallet/triggerconstantcontract",
                                          {"owner_address": contract, "contract_address": contract,
                                           "function_selector": fn, "parameter": "", "visible": True}))
            cr = j.get("constant_result") or []
            if cr:
                raw = bytes.fromhex(cr[0])
                if key == "decimals":
                    meta["decimals"] = int(cr[0], 16)
                elif len(raw) >= 64:
                    ln = int.from_bytes(raw[32:64], "big")
                    s = raw[64:64 + ln].decode("utf-8", "replace").strip()
                    if s and s.isprintable():
                        meta["symbol"] = s
        except Exception:
            pass
        time.sleep(0.2)
    _TRC20_META_CACHE[contract] = meta
    return meta

def tokens_trc20(trx_addr):
    out = []
    try:
        j = json.loads(_trongrid_get(f"https://api.trongrid.io/v1/accounts/{trx_addr}"))
        if j.get("data"):
            for item in (j["data"][0].get("trc20") or []):
                for k, v in item.items():
                    amt = int(v)
                    if amt > 0:
                        meta = _trc20_meta(k)
                        dec = meta.get("decimals")
                        adj = amt / (10 ** dec) if dec else amt
                        out.append({"chain": "TRON", "symbol": meta.get("symbol"), "amount": adj,
                                    "usd": None, "contract": k, "source": "TRC-20"})
    except Exception:
        pass
    return out

def get_prices():
    prices = {}
    for name, pair in [("ETH", "ETHUSDT"), ("BNB", "BNBUSDT"), ("LTC", "LTCUSDT"),
                       ("SOL", "SOLUSDT"), ("POL", "POLUSDT"), ("DOGE", "DOGEUSDT"),
                       ("TRX", "TRXUSDT"), ("AVAX", "AVAXUSDT"), ("BTC", "BTCUSDT")]:
        try:
            j = json.loads(_http_get(f"https://api.binance.com/api/v3/ticker/price?symbol={pair}", tries=2))
            prices[name] = float(j["price"])
        except Exception:
            continue
    return prices

# ---------------------------------------------------------------- 地址类型识别
def detect_address(addr):
    a = addr.strip()
    if re.fullmatch(r"0x[0-9a-fA-F]{40}", a):
        return {"ETH": a, "_kind": "evm"}
    if a.startswith("bc1"):
        return {"BTC": a}
    if a.startswith("ltc1"):
        return {"LTC": a}
    if a.startswith("bitcoincash:"):
        return {"BCH": a.split(":")[1]}
    if a.startswith("T") and len(a) == 34:
        return {"TRON": a}
    if a.startswith("D") and len(a) == 34:
        return {"DOGE": a}
    if re.fullmatch(r"[13][1-9A-HJ-NP-Za-km-z]{25,34}", a):
        return {"BTC": a}
    if 32 <= len(a) <= 44 and re.fullmatch(r"[1-9A-HJ-NP-Za-km-z]+", a):
        return {"SOL": a}
    return {"_unknown": a}

# ---------------------------------------------------------------- 主查询
def _query_common(label, addrs, chains=None, with_tokens=True, prices=None):
    chains = chains or CHAIN_LIST
    prices = prices or {}
    res = {"label": label, "addresses": addrs, "rows": [], "tokens": [], "total_usd": 0.0, "errors": {}}
    eth = addrs.get("ETH")

    def row_of(chain, addr, fn):
        try:
            bal, disp = fn(addr)
        except Exception as e:
            bal, disp = None, f"错误: {str(e)[:60]}"
        usd = None
        if bal is not None and bal > 0:
            pkey = {"SOL(旧路径)": "SOL", "TRON": "TRX"}.get(chain, EVM_SYM.get(chain, chain))
            p = 1.0 if pkey == "xDAI" else prices.get(pkey)
            if p:
                usd = bal * p
        return {"chain": chain, "address": addr, "balance": bal, "display": disp, "usd": usd}

    jobs = []
    if addrs.get("BTC"):
        jobs.append(("BTC", addrs["BTC"], q_btc))
    if addrs.get("LTC"):
        jobs.append(("LTC", addrs["LTC"], q_ltc))
    if addrs.get("DOGE"):
        jobs.append(("DOGE", addrs["DOGE"], q_doge))
    if addrs.get("BCH"):
        jobs.append(("BCH", addrs["BCH"], q_bch))
    if addrs.get("TRON"):
        jobs.append(("TRON", addrs["TRON"], q_trx))
    if addrs.get("SOL"):
        jobs.append(("SOL", addrs["SOL"], q_sol))
    if addrs.get("SOL_legacy"):
        jobs.append(("SOL(旧路径)", addrs["SOL_legacy"], q_sol))
    if eth:
        for ch in EVM_CHAINS:
            if ch in chains:
                jobs.append((ch, eth, (lambda a, c=ch: q_evm(a, c))))

    # 并行：各链原生币同时查（原来逐链串行是慢的主因）
    with concurrent.futures.ThreadPoolExecutor(max_workers=min(16, max(1, len(jobs)))) as ex:
        rows = list(ex.map(lambda j: row_of(*j), jobs))
    order = {c: i for i, c in enumerate(CHAIN_LIST)}
    rows.sort(key=lambda r: order.get(r["chain"], 99))
    res["rows"] = rows
    for r in rows:
        if r["balance"] is None:
            res["errors"][r["chain"]] = r["display"]
        if r.get("usd"):
            res["total_usd"] += r["usd"]

    if with_tokens:
        toks = []
        with concurrent.futures.ThreadPoolExecutor(max_workers=16) as ex:
            futs = []
            if eth:
                for ch in ("ETH", "Base", "Polygon", "Arbitrum", "Optimism", "Linea", "Scroll", "zkSync"):
                    if ch in chains:
                        futs.append(ex.submit(tokens_blockscout, eth, ch))
                futs.append(ex.submit(tokens_stables, eth, chains))
            if addrs.get("SOL"):
                futs.append(ex.submit(tokens_spl, addrs["SOL"]))
            if addrs.get("SOL_legacy"):
                futs.append(ex.submit(tokens_spl, addrs["SOL_legacy"]))
            if addrs.get("TRON"):
                futs.append(ex.submit(tokens_trc20, addrs["TRON"]))
            for f in concurrent.futures.as_completed(futs):
                try:
                    toks += f.result()
                except Exception:
                    pass
        seen = set(); dedup = []
        for t in toks:
            k = (t["chain"], t.get("contract"))
            if k in seen:
                continue
            seen.add(k)
            dedup.append(t)
        res["tokens"] = dedup
        for t in dedup:
            if t.get("usd"):
                res["total_usd"] += t["usd"]

    res["total_usd"] = round(res["total_usd"], 2)
    return res

def query_mnemonic(mn, chains=None, with_tokens=True, prices=None):
    addrs = derive_all(mn.strip())
    return _query_common(f"助记词 {mn.strip()[:28]}…", addrs, chains, with_tokens, prices)

def query_address(addr, chains=None, with_tokens=True, prices=None):
    detected = detect_address(addr)
    return _query_common(addr.strip(), detected, chains, with_tokens, prices)
