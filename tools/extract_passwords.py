# extract_passwords.py — 全库明文密码提取（网站口令 + App PIN + 应用内口令）
# 输出: plaintext_passwords.md / plaintext_passwords.csv
import json, os, re, csv, sys

D = os.path.dirname(os.path.abspath(__file__))
ROOTS = [
    ("war", D),
    ("war_new", os.path.join(os.path.dirname(D), "war_new")),
    ("new", os.path.dirname(D)),
]

def decode_hex_smart(hexs):
    if not hexs: return None
    try:
        b = bytes.fromhex(hexs)
    except Exception:
        return None
    printable = sum(1 for x in b if 32 <= x <= 126)
    if b and printable / len(b) >= 0.85:
        return b.decode("utf-8", "replace")
    return None

rows = []
seen = set()

for tag, root in ROOTS:
    if not os.path.isdir(root): continue
    for fn in sorted(os.listdir(root)):
        if not fn.lower().endswith(".json"): continue
        if not re.match(r"^\d{8}-\d{6}-", fn): continue
        path = os.path.join(root, fn)
        try:
            raw = open(path, encoding="utf-8", errors="replace").read()
        except Exception:
            continue
        dev = re.search(r'"device_uuid":"([^"]+)"', raw)
        dev = dev.group(1) if dev else "?"
        # 条目遍历（粗粒度正则：rowid + service + account + accessGroup + label + dataHex）
        pat = re.compile(
            r'\{"rowid":(\d+),"_table":"([^"]+)".{0,4000}?"service":("([^"]*)"|null),"account":("([^"]*)"|null),"accessGroup":("([^"]*)"|null),.{0,400}?"label":("([^"]*)"|null),.{0,300}?"dataHex":"([0-9a-fA-F]*)"',
            re.S)
        for m in pat.finditer(raw):
            rowid = m.group(1); table = m.group(2)
            svc = m.group(4); acct = m.group(6); ag = m.group(8); label = m.group(10)
            hexs = m.group(11)
            val = decode_hex_smart(hexs)
            recs = []
            # 1) 网站口令: cfnetwork + label 含账号 或 Password/Backup 类
            if "cfnetwork" in (ag or "") and val and not val.startswith("1503"):
                lbl2 = label or ""
                if (("@" in lbl2) or re.search(r"(?i)password|backup|wallet", lbl2)) and ("Passwords?not?saved" not in lbl2):
                    recs.append(("website", label, acct, val))
            # 2) pin/pincode 类（账户名或服务名含 pin）
            if re.search(r"(?i)pin[_\-]?code|pincode|pin_key|pinCodeType|PINCODEKEY", f"{svc} {acct}"):
                if val:
                    recs.append(("app-pin", label or svc, acct, val))
            # 3) JSON 值内含 pin_code 字段
            if val and val.lstrip().startswith("{") and '"pin_code"' in val:
                try:
                    j = json.loads(val)
                    if j.get("pin_code"):
                        recs.append(("app-pin-json", label or svc, acct, str(j["pin_code"])))
                except Exception:
                    pass
            # 4) JSON 值内含 password 字段（非空）
            if val and val.lstrip().startswith("{") and re.search(r'"(password|passwd|passphrase)"\s*:', val):
                try:
                    j = json.loads(val)
                    for k in ("password", "passwd", "passphrase"):
                        if j.get(k):
                            recs.append(("app-password", label or svc, acct, str(j[k])))
                except Exception:
                    pass
            for kind, lbl, a, v in recs:
                key = (fn, kind, lbl, a, v)
                if key in seen: continue
                seen.add(key)
                rows.append({"file": fn, "root": tag, "device": dev, "table": table, "rowid": rowid,
                             "kind": kind, "bundle": ag or "", "site_label": lbl, "account": a, "password": v})

# 输出
csv_path = os.path.join(D, "plaintext_passwords.csv")
with open(csv_path, "w", newline="", encoding="utf-8-sig") as fh:
    w = csv.DictWriter(fh, fieldnames=["file", "root", "device", "table", "rowid", "kind", "bundle", "site_label", "account", "password"])
    w.writeheader()
    for r in rows:
        w.writerow(r)

by_file = {}
for r in rows:
    by_file.setdefault(r["file"], []).append(r)

md = []
md.append("# 全库明文密码提取报告（plaintext passwords）")
md.append("")
md.append(f"> 证据包: {len(by_file)} 个 | 明文密码条目: {len(rows)} 条（网站口令 + App PIN + 应用内口令）")
md.append("> 仅提取可直接解码的明文；不含爆破/解密尝试。")
md.append("")
for fn in sorted(by_file):
    recs = by_file[fn]
    md.append(f"## {fn}（{recs[0]['device'][:8]}，{len(recs)} 条）")
    md.append("")
    md.append("| 类型 | 站点/来源 | 账号 | 密码 |")
    md.append("|---|---|---|---|")
    for r in recs:
        site = (r["site_label"] or "").replace("|", "/")
        acct = (r["account"] or "").replace("|", "/")
        pw = (r["password"] or "").replace("|", "/")
        md.append(f"| {r['kind']} | {site} | {acct} | `{pw}` |")
    md.append("")
with open(os.path.join(D, "plaintext_passwords.md"), "w", encoding="utf-8") as fh:
    fh.write("\n".join(md))

# TXT: 包名 + 账号 + 密码
txt = []
txt.append("全库明文密码清单（包名 | 站点/来源 | 账号 | 密码）")
txt.append(f"证据包 {len(by_file)} 个 | 条目 {len(rows)} 条")
txt.append("")
for fn in sorted(by_file):
    recs = by_file[fn]
    txt.append(f"### {fn}（{recs[0]['device']}）")
    for r in recs:
        txt.append(f"{r['bundle']} | {r['site_label']} | {r['account']} | {r['password']}")
    txt.append("")
with open(os.path.join(D, "plaintext_passwords.txt"), "w", encoding="utf-8") as fh:
    fh.write(chr(10).join(txt))
print(f"提取完成: {len(rows)} 条 -> plaintext_passwords.md / .csv / .txt")
print(f"覆盖证据包: {len(by_file)}")
kinds = {}
for r in rows:
    kinds[r["kind"]] = kinds.get(r["kind"], 0) + 1
print("类型分布:", kinds)
