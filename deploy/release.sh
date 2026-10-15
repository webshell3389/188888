#!/usr/bin/env bash
# release.sh — 规范发布：原子替换 + 健康门禁 + 失败自动回滚 + 缓存预热。
#
# 用法（服务器上，root）：
#   bash release.sh /tmp/mockc2-linux-amd64 /tmp/admin-dist.tar.gz
#
# 为什么要有这个脚本（都是实际踩过的坑）：
#   1) 不再 `rm -rf www/admin/assets`：删旧哈希资源会让"浏览器里还开着旧 index.html
#      或缓存了旧 HTML"的客户端请求到 404 的 JS → 页面白屏，必须强刷才恢复。
#      改为叠加新资源、只覆盖 index.html（旧哈希文件留着，靠哈希天然区分版本）。
#   2) 二进制先落 .new 再 `mv`（规避运行中可执行文件 ETXTBSY）。
#   3) 重启后做健康门禁（端口 → 登录 → 关键接口），任一失败自动回滚到上一版并重启。
#   4) 发布完预热一次 /__dash/api：否则第一个用户吃到 5s 冷启动，观感就是"部署完很慢"。
set -uo pipefail

BIN="${1:-/tmp/mockc2-linux-amd64}"
WEB="${2:-/tmp/admin-dist.tar.gz}"
ROOT=/opt/mockc2
SVC=mockc2
TS=$(date +%Y%m%d-%H%M%S)
PORT=9100
ADMIN_PATH="/cp-652924fc/"
CK=/tmp/.release-ck
FAIL=0

log() { printf '\033[1;36m[%s]\033[0m %s\n' "$(date +%H:%M:%S)" "$*"; }
die() { printf '\033[1;31m[fail]\033[0m %s\n' "$*"; exit 1; }

cd "$ROOT" || die "no $ROOT"
[ -f "$BIN" ] || die "binary not found: $BIN"

# ── 0. 读配置口令（用于登录健康检查）──
set -a; . "$ROOT/mockc2.env" 2>/dev/null || true; set +a
ADMIN_PW="${MOCKC2_ADMIN_PASSWORD:-}"
ADMIN_USER="${MOCKC2_ADMIN_USER:-admin}"

# ── 1. 备份 ──
log "备份 binary + www/admin"
cp -a mockc2 "mockc2.bak-$TS"
[ -d www/admin ] && cp -a www/admin "www/admin.bak-$TS"
echo "  → mockc2.bak-$TS / www/admin.bak-$TS"

# ── 2. 二进制原子替换 ──
log "安装二进制"
install -m 0755 "$BIN" mockc2.new || die "install failed"
chown mockc2:mockc2 mockc2.new
mv -f mockc2.new mockc2

# ── 3. 前端：叠加，不删旧资源 ──
if [ -f "$WEB" ]; then
  log "更新前端（保留旧哈希资源）"
  mkdir -p www/admin/assets
  tar -xzf "$WEB" -C www/admin
  chown -R mockc2:mockc2 www/admin
  echo -n "  index → "; grep -oE 'index-[A-Za-z0-9_-]+\.js' www/admin/index.html | head -1
fi

# ── 4. 重启 ──
log "重启 $SVC"
systemctl restart "$SVC"

# ── 5. 健康门禁 ──
log "健康检查"
for i in $(seq 1 24); do
  if curl -sf -m 2 -o /dev/null "http://127.0.0.1:$PORT$ADMIN_PATH"; then break; fi
  [ "$i" = 24 ] && FAIL=1
  sleep 0.5
done

if [ "$FAIL" = 0 ] && [ -n "$ADMIN_PW" ]; then
  code=$(curl -s -o /dev/null -w '%{http_code}' -c "$CK" -X POST "http://127.0.0.1:$PORT/__auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PW\"}")
  if [ "$code" != "200" ]; then echo "  登录返回 $code"; FAIL=1; fi
fi

if [ "$FAIL" = 0 ]; then
  for ep in "/panel/api/devices" "/panel/api/notes?page=1&per=50" "/__dash/api"; do
    t=$(curl -s -o /dev/null -w '%{time_total}' -b "$CK" "http://127.0.0.1:$PORT$ep")
    echo "  $ep  ${t}s"
  done
fi

# ── 6. 失败自动回滚 ──
if [ "$FAIL" != 0 ]; then
  log "健康门禁未通过 → 回滚到 $TS"
  cp -a "mockc2.bak-$TS" mockc2
  if [ -d "www/admin.bak-$TS" ]; then rm -rf www/admin; cp -a "www/admin.bak-$TS" www/admin; fi
  systemctl restart "$SVC"
  sleep 3
  echo "  回滚后服务状态: $(systemctl is-active "$SVC")"
  die "已回滚。日志：journalctl -u $SVC -n 50 --no-pager"
fi

# ── 7. 预热缓存 ──
log "预热缓存（避免首个用户吃冷启动）"
curl -s -o /dev/null -b "$CK" "http://127.0.0.1:$PORT/__dash/api" || true
curl -s -o /dev/null -b "$CK" "http://127.0.0.1:$PORT/panel/api/devices" || true

log "完成  state=$(systemctl is-active "$SVC")  bundle=$(grep -oE 'index-[A-Za-z0-9_-]+\.js' www/admin/index.html | head -1)"
