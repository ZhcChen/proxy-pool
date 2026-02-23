#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

compose() {
  if docker compose version >/dev/null 2>&1; then
    docker compose "$@"
    return
  fi
  if command -v docker-compose >/dev/null 2>&1; then
    docker-compose "$@"
    return
  fi
  echo "错误：未找到 docker compose 或 docker-compose，请先安装 Docker Compose。" >&2
  exit 1
}

usage() {
  cat <<'EOF'
用法: ./scripts/dev.sh <command>

命令:
  up       构建并后台启动服务
  down     停止并删除服务容器
  restart  重启服务（down + up）
  logs     跟随查看 proxy-pool 日志
  status   查看 proxy-pool 服务状态
EOF
}

cmd="${1:-}"
case "$cmd" in
  up)
    compose up -d --build
    ;;
  down)
    compose down
    ;;
  restart)
    compose down
    compose up -d --build
    ;;
  logs)
    compose logs -f proxy-pool
    ;;
  status)
    compose ps proxy-pool
    ;;
  *)
    usage
    exit 1
    ;;
esac
