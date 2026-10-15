# -*- coding: utf-8 -*-
"""chainquery/server.py — HTTP API + Web 前端服务

启动: python server.py [--host 127.0.0.1] [--port 8787]
浏览器打开: http://127.0.0.1:8787

API:
  GET  /api/chains                支持链列表
  POST /api/derive   {mnemonics}  仅派生地址（快）
  POST /api/query    {mnemonics:[], addresses:[], chains:[], tokens:true, prices:false}
                                  创建查询任务 → 返回 {job: id}
  GET  /api/job?id=xxx            任务状态/结果
"""
import argparse, json, os, threading, time, uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse, parse_qs

import engine

HERE = os.path.dirname(os.path.abspath(__file__))
JOBS = {}
LOCK = threading.Lock()
JOB_TTL = 3600


class Handler(BaseHTTPRequestHandler):
    server_version = "chainquery/1.0"

    def log_message(self, fmt, *args):
        pass  # 静默

    def _send(self, code, body, ctype="application/json; charset=utf-8"):
        data = body if isinstance(body, (bytes, bytearray)) else json.dumps(body, ensure_ascii=False).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()
        self.wfile.write(data)

    # ---------------- GET ----------------
    def do_GET(self):
        u = urlparse(self.path)
        p = u.path
        if p in ("/", "/index.html"):
            fp = os.path.join(HERE, "static", "index.html")
            try:
                self._send(200, open(fp, "rb").read(), "text/html; charset=utf-8")
            except Exception:
                self._send(500, {"error": "index.html missing"})
        elif p == "/api/chains":
            self._send(200, {"chains": engine.CHAIN_LIST, "evm": list(engine.RPC.keys()),
                             "stables": [f"{s}-{c}" for s, c, _, _ in engine.STABLES]})
        elif p == "/api/job":
            qs = parse_qs(u.query)
            jid = (qs.get("id") or [""])[0]
            with LOCK:
                job = JOBS.get(jid)
            if job:
                self._send(200, job)
            else:
                self._send(404, {"error": "job not found"})
        else:
            self._send(404, {"error": "not found"})

    # ---------------- POST ----------------
    def do_POST(self):
        try:
            ln = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(ln).decode("utf-8") or "{}")
        except Exception:
            self._send(400, {"error": "bad json"})
            return
        if self.path == "/api/derive":
            out = []
            for mn in (body.get("mnemonics") or []):
                try:
                    out.append({"mnemonic": mn, "addresses": engine.derive_all(mn.strip())})
                except Exception as e:
                    out.append({"mnemonic": mn, "error": str(e)[:120]})
            self._send(200, {"results": out})
        elif self.path == "/api/query":
            jid = uuid.uuid4().hex[:12]
            with LOCK:
                JOBS[jid] = {"id": jid, "status": "running", "total": 0, "done": 0,
                             "started": time.time(), "results": []}
            threading.Thread(target=run_job, args=(jid, body), daemon=True).start()
            self._send(200, {"job": jid})
        else:
            self._send(404, {"error": "not found"})


def run_job(jid, body):
    mns = [x for x in (body.get("mnemonics") or []) if x and x.strip()]
    addrs = [x for x in (body.get("addresses") or []) if x and x.strip()]
    chains = body.get("chains") or None
    with_tokens = bool(body.get("tokens", True))
    with_prices = bool(body.get("prices", False))

    prices = engine.get_prices() if with_prices else {}
    tasks = [("mnemonic", m) for m in mns] + [("address", a) for a in addrs]
    with LOCK:
        JOBS[jid]["total"] = len(tasks)
        JOBS[jid]["prices"] = prices

    for i, (kind, val) in enumerate(tasks):
        try:
            if kind == "mnemonic":
                r = engine.query_mnemonic(val, chains=chains, with_tokens=with_tokens, prices=prices)
            else:
                r = engine.query_address(val, chains=chains, with_tokens=with_tokens, prices=prices)
        except Exception as e:
            r = {"label": val[:48], "error": str(e)[:200], "rows": [], "tokens": [], "total_usd": 0.0}
        with LOCK:
            JOBS[jid]["results"].append(r)
            JOBS[jid]["done"] = i + 1

    with LOCK:
        JOBS[jid]["status"] = "done"
        JOBS[jid]["elapsed"] = round(time.time() - JOBS[jid]["started"], 1)


def cleanup():
    while True:
        time.sleep(300)
        now = time.time()
        with LOCK:
            for k in [k for k, v in JOBS.items() if v.get("status") == "done" and now - v["started"] > JOB_TTL]:
                JOBS.pop(k, None)


def main():
    ap = argparse.ArgumentParser(description="chainquery 多链余额/代币查询服务")
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--port", type=int, default=8787)
    args = ap.parse_args()
    threading.Thread(target=cleanup, daemon=True).start()
    srv = ThreadingHTTPServer((args.host, args.port), Handler)
    print(f"chainquery 已启动: http://{args.host}:{args.port}")
    print("Ctrl+C 停止")
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
