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
probe GET /admin/
probe GET /__dash "" 401
probe GET /__q/commands "" 401
probe GET /panel/api/devices "" 401
probe POST /__auth/login '{"password":"lab888"}'
# ── 保留契约（Vue 控制台唯一数据源）──
probe GET /__dash/api
probe GET "/__dash/api?log_tail=10&acc_tail=5"
probe GET /__q/commands
probe GET /__q/device_command_targets
probe GET "/__q/device_commands?target=127.0.0.1"
probe GET /panel/api/devices
probe GET /panel/api/progress
probe GET /panel/api/apps
probe GET /panel/api/war
probe GET /panel/api/notes
probe GET /panel/api/visits
probe GET /panel/api/keychain/devices
probe GET /panel/api/results
probe GET /console "" 302
probe GET /api/stats
probe GET /api/data/photos
probe GET /__control
probe POST /__control '{"type":"ls","params":{"path":"/var/mobile"},"once":true}'
probe POST /beacon '{"uuid":"S","status":"idle"}'
# ── 已下线端点应 404（前端全量迁移 Vue 后收敛）──
probe GET /panel/api/overview "" 404
probe GET /panel/api/access "" 404
probe GET /panel/api/commands "" 404
probe GET /__q/devices "" 404
probe GET /__q/stats "" 404
probe GET /__q/wallets "" 404
probe GET "/api/list?type=managed" "" 404
probe GET "/api/activity?limit=5" "" 404
probe GET /api/data/memos "" 404
probe POST /api/dispatch '{"uuid":"SMOKE-0001","module":"basic_info"}' 404
probe POST /__auth/logout '{}'
probe GET /__dash/api "" 401

echo
echo "== 汇总: PASS=$PASS FAIL=$FAIL =="
[ "$FAIL" -eq 0 ]
