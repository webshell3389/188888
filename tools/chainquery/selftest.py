# -*- coding: utf-8 -*-
# selftest.py — chainquery 自测（启动 server.py 后运行）
import json, sys, time, urllib.request

BASE = "http://127.0.0.1:8787"

def get(u):
    req = urllib.request.Request(u, headers={"User-Agent": "selftest"})
    return urllib.request.urlopen(req, timeout=30).read().decode()

def post(u, obj):
    req = urllib.request.Request(u, data=json.dumps(obj).encode(),
                                 headers={"Content-Type": "application/json", "User-Agent": "selftest"})
    return urllib.request.urlopen(req, timeout=30).read().decode()

ok = 0
# 1) 首页
h = get(BASE + "/")
assert "chainquery" in h, "首页异常"
print("[1] GET /            -> OK（HTML 已返回）")
ok += 1
# 2) 链列表
c = json.loads(get(BASE + "/api/chains"))
print(f"[2] GET /api/chains  -> OK（{len(c['chains'])} 条链）")
ok += 1
# 3) 派生
d = json.loads(post(BASE + "/api/derive", {"mnemonics": ["test test test test test test test test test test test junk"]}))
a = d["results"][0]["addresses"]
print(f"[3] POST /api/derive -> OK（BTC={a['BTC'][:14]}… ETH={a['ETH'][:12]}…）")
ok += 1
# 4) 查询（LTC 快测：已知 2.998898 的那组）
MN = "enroll midnight trash awake cannon scissors number sunset pulp tribe enact one"
j = json.loads(post(BASE + "/api/query", {"mnemonics": [MN], "chains": ["LTC"], "tokens": False, "prices": False}))
jid = j["job"]
for _ in range(40):
    time.sleep(1.5)
    job = json.loads(get(f"{BASE}/api/job?id={jid}"))
    if job.get("status") == "done":
        break
r = job["results"][0]
ltc_row = [x for x in r["rows"] if x["chain"] == "LTC"][0]
print(f"[4] POST /api/query  -> OK（LTC = {ltc_row['display']} @ {ltc_row['address']}）")
ok += 1
# 5) 全链+代币快测（bid unveil：已知 SOL 0.003549657 + 3 个 SPL 代币）
MN2 = "bid unveil mix feature item mesh critic glimpse timber excite observe way"
j = json.loads(post(BASE + "/api/query", {"mnemonics": [MN2], "chains": ["SOL"], "tokens": True, "prices": True}))
jid = j["job"]
for _ in range(60):
    time.sleep(1.5)
    job = json.loads(get(f"{BASE}/api/job?id={jid}"))
    if job.get("status") == "done":
        break
r = job["results"][0]
sol_row = [x for x in r["rows"] if x["chain"] == "SOL"][0]
print(f"[5] 全链+代币快测     -> OK（SOL = {sol_row['display']} | SPL 代币 {len(r['tokens'])} 个 | 估值 ${r['total_usd']}）")
ok += 1

print()
print(f"全部 {ok}/5 通过")
