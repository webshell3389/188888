#!/usr/bin/env bash
# run.sh —— Linux/macOS 启动脚本（等价 run.bat）
# 用法: ./run.sh [--no-https --http-port 9100 --workers 4 ...]
set -euo pipefail
cd "$(dirname "$0")"

BIN="${MOCKC2_BIN:-}"
if [ -z "$BIN" ]; then
  if [ -x "./mockc2" ]; then
    BIN="./mockc2"                      # 发布包布局（二进制在根）
  else
    case "$(uname -m)" in
      x86_64|amd64) BIN="dist/mockc2-linux-amd64" ;;
      aarch64|arm64) BIN="dist/mockc2-linux-arm64" ;;
      *) BIN="dist/mockc2-linux-amd64" ;;
    esac
  fi
fi
[ -x "$BIN" ] || { echo "[!] 找不到可执行文件: $BIN（先 make linux，或用 MOCKC2_BIN 指定）" >&2; exit 1; }
exec "$BIN" "$@"
