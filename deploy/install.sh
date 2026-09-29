#!/usr/bin/env bash
# install.sh —— 安装到 /opt/mockc2 并注册 systemd 服务（Linux，MySQL 版）
#
# 前置条件：
#   1) 已准备 MySQL/MariaDB 库（建议独立实例或本机 127.0.0.1:3306）
#      并创建库与账号：CREATE DATABASE mockc2 CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
#      CREATE USER 'mockc2'@'%' IDENTIFIED BY '<password>';
#      GRANT SELECT,INSERT,UPDATE,DELETE,CREATE,ALTER,INDEX,REFERENCES ON mockc2.* TO 'mockc2'@'%';
#      （或直接使用 deploy/docker-compose.yml 一键编排）
#   2) 在项目根执行：make linux
#
# 用法：
#   sudo ./deploy/install.sh
#   # 可选环境变量: DEST=/opt/mockc2 BIN=dist/mockc2-linux-arm64
#   # 可选预置 DB 参数: DB_HOST=... DB_PORT=... DB_USER=... DB_PASSWORD=... DB_NAME=...
set -euo pipefail

SRC="$(cd "$(dirname "$0")/.." && pwd)"
DEST="${DEST:-/opt/mockc2}"

BINFILE="${BIN:-}"
if [ -z "$BINFILE" ]; then
  if [ -f "$SRC/mockc2" ]; then
    BINFILE="mockc2"          # 发布包布局（二进制已在根）
  else
    case "$(uname -m)" in
      x86_64|amd64) BINFILE="dist/mockc2-linux-amd64" ;;
      aarch64|arm64) BINFILE="dist/mockc2-linux-arm64" ;;
      *) echo "[!] 未知架构 $(uname -m)，请用 BIN=... 指定" >&2; exit 1 ;;
    esac
  fi
fi

[ -f "$SRC/$BINFILE" ] || { echo "[!] 缺少二进制 $SRC/$BINFILE（先 make linux）" >&2; exit 1; }
[ "$(id -u)" = "0" ] || { echo "[!] 需要 root 权限（sudo ./deploy/install.sh）" >&2; exit 1; }

# 服务用户（无登录、无 home）
if ! id -u mockc2 >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin mockc2 || \
    adduser -S -D -H -s /sbin/nologin mockc2
fi

mkdir -p "$DEST"
install -m 0755 "$SRC/$BINFILE" "$DEST/mockc2"
rm -rf "$DEST/www"
cp -r "$SRC/www" "$DEST/www"
[ -f "$SRC/README.md" ] && cp "$SRC/README.md" "$DEST/README.md"
[ -f "$SRC/mockc2_settings.example.json" ] && cp "$SRC/mockc2_settings.example.json" "$DEST/"

# 运行态目录（capture 留存 / certs 自签证书；数据库在 MySQL，不放本地）
mkdir -p "$DEST/capture" "$DEST/certs"
chown -R mockc2:mockc2 "$DEST"

# ── 数据库环境文件（0600，含口令；不覆盖已有）──
ENVFILE="$DEST/mockc2.env"
if [ ! -f "$ENVFILE" ]; then
  SECRET="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  cat > "$ENVFILE" <<EOF
# MockC2 运行环境（systemd EnvironmentFile；优先级高于 mockc2_settings.json）
# 修改后 systemctl restart mockc2
MOCKC2_ADMIN_PASSWORD=${ADMIN_PASSWORD:-CHANGE-ME-strong-password}
MOCKC2_SESSION_SECRET=${SESSION_SECRET:-$SECRET}
MOCKC2_OPS_TOKEN=
MOCKC2_DB_HOST=${DB_HOST:-127.0.0.1}
MOCKC2_DB_PORT=${DB_PORT:-3306}
MOCKC2_DB_USER=${DB_USER:-mockc2}
MOCKC2_DB_PASSWORD=${DB_PASSWORD:-CHANGE-ME-db-password}
MOCKC2_DB_NAME=${DB_NAME:-mockc2}
MOCKC2_DB_MAX_OPEN_CONNS=32
MOCKC2_DB_MAX_IDLE_CONNS=8
EOF
  chmod 0600 "$ENVFILE"
  chown mockc2:mockc2 "$ENVFILE"
  echo "[i] 已生成 $ENVFILE（请修改 MOCKC2_ADMIN_PASSWORD / MOCKC2_DB_PASSWORD）"
fi

# 数据库连通性预检（失败仅告警，不阻断安装）
DBHOST="$(grep -E '^MOCKC2_DB_HOST=' "$ENVFILE" | cut -d= -f2 || true)"
DBPORT="$(grep -E '^MOCKC2_DB_PORT=' "$ENVFILE" | cut -d= -f2 || true)"
if command -v timeout >/dev/null 2>&1 && command -v bash >/dev/null 2>&1; then
  if ! timeout 3 bash -c "echo > /dev/tcp/${DBHOST:-127.0.0.1}/${DBPORT:-3306}" 2>/dev/null; then
    echo "[!] 警告：MySQL ${DBHOST:-127.0.0.1}:${DBPORT:-3306} 不可达，服务将启动失败"
    echo "    请先准备 MySQL（见本脚本头部注释）或修改 $ENVFILE"
  fi
fi

install -m 0644 "$SRC/deploy/mockc2.service" /etc/systemd/system/mockc2.service
systemctl daemon-reload
systemctl enable mockc2 >/dev/null 2>&1 || true
systemctl restart mockc2

sleep 1
systemctl --no-pager --full status mockc2 | head -n 14 || true
echo
echo "[i] 已安装到 $DEST"
echo "    管理台   http://<host>:9100/admin   （默认口令见 $ENVFILE，请立即修改）"
echo "    交付入口 http://<host>:9100/boot.html?c=1"
echo "    运行指标 http://<host>:9100/__metrics （登录后）"
echo "    诊断     pprof 本地 127.0.0.1:6060/debug/pprof/（--pprof-addr 调整）"
echo "    配置覆盖 $ENVFILE 或 $DEST/mockc2_settings.json"
