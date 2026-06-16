#!/usr/bin/env bash
#
# 启动 rfa trace 可视化服务（后端 API + 前端页面一体）
#
# 用法：
#   scripts/serve.sh                    # 默认端口 7777，读取当前目录 .rfa/sessions
#   scripts/serve.sh --port 8080        # 自定义端口
#   scripts/serve.sh --dir /path/to/sessions  # 自定义会话目录
#
# 前端页面：
#   http://127.0.0.1:7777/             # 原始 trace 列表视图
#   http://127.0.0.1:7777/flow.html    # 执行流可视化（推荐）
#
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO"

# 确保二进制是最新的
echo "▸ 编译 rfa ..."
go build -o "$REPO/bin/rfa" ./cmd/rfa

PORT=7777
DIR=""
HOST="127.0.0.1"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --port)  PORT="$2"; shift 2 ;;
    --dir)   DIR="$2";  shift 2 ;;
    --host)  HOST="$2"; shift 2 ;;
    -h|--help)
      echo "用法: scripts/serve.sh [--port 7777] [--dir <sessions-dir>] [--host 127.0.0.1]"
      echo ""
      echo "启动后打开:"
      echo "  http://<host>:<port>/           原始 trace 视图"
      echo "  http://<host>:<port>/flow.html  执行流可视化（推荐）"
      exit 0
      ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
done

ARGS=(trace --port "$PORT" --host "$HOST")
if [[ -n "$DIR" ]]; then
  ARGS+=(--dir "$DIR")
fi

echo ""
echo "┌─────────────────────────────────────────────────────┐"
echo "│  rfa trace 可视化服务                               │"
echo "│                                                     │"
echo "│  Trace 列表:  http://$HOST:$PORT/              │"
echo "│  执行流视图:  http://$HOST:$PORT/flow.html     │"
echo "│                                                     │"
echo "│  Ctrl+C 停止                                        │"
echo "└─────────────────────────────────────────────────────┘"
echo ""

exec "$REPO/bin/rfa" "${ARGS[@]}"
