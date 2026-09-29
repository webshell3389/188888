#!/usr/bin/env bash
# smoke9001.sh —— 授权闭环冒烟回归（bash/curl 版，对齐 tools/smoke9001.ps1）
#
# 用法:
#   bash tools/smoke9001.sh                 # 默认 http://127.0.0.1:9100
#   bash tools/smoke9001.sh http://127.0.0.1:9101
set -uo pipefail

BASE="${1:-http://127.0.0.1:9100}"
CJ="$(mktemp -t c2mock.XXXXXX)"
trap 'rm -f "$CJ"' EXIT
PASS=0; FAIL=0

# probe METHOD PATH [BODY] [EXPECTED]
probe() {
  local m="$1" p="$2" body="${3:-}" exp="${4:-200}" code
  if [ -n "$body" ]; then
    code=$(curl -s -o /dev/null -w '%{http_code}' -b "$CJ" -c "$CJ" -X "$m" \
      -H 'Content-Type: application/json' --data "$body" "$BASE$p" 2>/dev/null || echo 000)
  else
    code=$(curl -s -o /dev/null -w '%{http_code}' -b "$CJ" -c "$CJ" -X "$m" "$BASE$p" 2>/dev/null || echo 000)
  fi
  if [ "$code" = "$exp" ]; then PASS=$((PASS+1)); mark="OK "; else FAIL=$((FAIL+1)); mark="!! "; fi
  printf '%s%-7s %-42s -> %s (exp %s)\n' "$mark" "$m" "$p" "$code" "$exp"
}

echo "== 交付链 =="
probe GET "/"
probe GET "/boot.html?c=1"
probe POST "/api/ip-sync/sync" '{"channelCode":"1","deviceVersion":"IOS 18.6.2","domain":"lab"}'
for f in /B10/group.html /B10/rce_loader.js /B10/rce_worker_18.6.js /B10/rce_module_18.6.js \
         "/log.html?id=0&text=smoke" /pe_worker.js /sbx0_main_18.4.js /sbx1_main.js /boot.js; do
  probe GET "$f"
done

echo "== C2 簇 =="
probe POST /beacon '{"uuid":"S","status":"idle"}'
probe POST /result '{"command_id":"s","status":"ok"}'
probe POST /event '{}'
probe POST /a '{}'
probe POST /u '{}'
probe POST /nb '{}'
probe POST /ph 'PNG'
probe GET /res/1/2 "" 404
probe POST /heartbeat 'heartbeat:pe_done'

echo "== CORS 预检 =="
probe OPTIONS /api/ip-sync/sync "" 204

echo "== 管理面（授权闭环）=="
probe GET /admin
probe GET /__dash "" 401
probe GET /__q/devices "" 401
probe POST /__auth/login '{"password":"lab888"}'
probe GET /__dash/api
probe GET /__q/devices
probe GET /__q/stats
probe GET /__q/commands
probe GET /__q/wallets
probe GET /__q/wallets_rules
probe GET /panel/api/overview
probe GET /panel/api/devices
probe GET /panel/api/apps
probe GET /panel/api/war
probe GET /panel/api/notes
probe GET /panel/api/commands
probe GET /panel/api/access
probe GET /panel/api/visits
probe GET /panel/api/progress
probe GET /console
probe GET /api/stats
probe GET "/api/list?type=visitor"
probe GET "/api/list?type=managed"
probe GET "/api/activity?limit=5"
probe GET /api/tasks
probe GET /api/data/memos
probe GET /api/data/wallets
probe GET /api/data/photos
probe GET /__control
probe POST /__control '{"type":"ls","params":{"path":"/var/mobile"},"once":true}'
probe POST /beacon '{"uuid":"S","status":"idle"}'
probe POST /panel/api/commands '{"type":"memo_scan","params":{},"once":true}'
probe POST /api/dispatch '{"uuid":"SMOKE-0001","module":"basic_info"}'
probe POST /__auth/logout '{}'
probe GET /__dash/api "" 401

echo
echo "== 汇总: PASS=$PASS FAIL=$FAIL =="
[ "$FAIL" -eq 0 ]
