#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

BUILD_SCRIPT="$ROOT_DIR/scripts/build.sh"
LOCAL_PROXY_BINARY="$ROOT_DIR/dist/proxy-pool-linux-amd64"
LOCAL_MIHOMO_BINARY="$ROOT_DIR/dist/mihomo-linux-amd64"
LOCAL_BIN_DIR="$ROOT_DIR/bin"

MIHOMO_REPO="${MIHOMO_REPO:-MetaCubeX/mihomo}"
MIHOMO_OS="linux"
MIHOMO_ARCH="amd64"
MIHOMO_CACHE_DIR="$ROOT_DIR/api/src/server/assets/mihomo/cache"

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

resolve_mihomo_asset() {
  local release_json tag asset_url=""
  local preferred_names first_name first_url

  release_json="$(mktemp)"
  curl -fsSL "https://api.github.com/repos/${MIHOMO_REPO}/releases/latest" -o "$release_json"
  tag="$(jq -r '.tag_name // empty' "$release_json")"
  if [[ -z "$tag" ]]; then
    rm -f "$release_json"
    echo "错误：无法解析 ${MIHOMO_REPO} 最新 release tag" >&2
    exit 1
  fi

  preferred_names=(
    "mihomo-${MIHOMO_OS}-${MIHOMO_ARCH}-compatible-${tag}.gz"
    "mihomo-${MIHOMO_OS}-${MIHOMO_ARCH}-v1-${tag}.gz"
    "mihomo-${MIHOMO_OS}-${MIHOMO_ARCH}-${tag}.gz"
    "mihomo-${MIHOMO_OS}-${MIHOMO_ARCH}-v2-${tag}.gz"
    "mihomo-${MIHOMO_OS}-${MIHOMO_ARCH}-v3-${tag}.gz"
  )

  for name in "${preferred_names[@]}"; do
    asset_url="$(jq -r --arg n "$name" '.assets[]? | select(.name==$n) | .browser_download_url' "$release_json" | head -n1)"
    if [[ -n "$asset_url" ]]; then
      echo "$name|$asset_url"
      rm -f "$release_json"
      return
    fi
  done

  first_name="$(jq -r --arg p "mihomo-${MIHOMO_OS}-${MIHOMO_ARCH}" '
    [.assets[]? | select((.name | startswith($p)) and (.name | endswith(".gz")))] | .[0].name // empty
  ' "$release_json")"
  if [[ -n "$first_name" ]]; then
    first_url="$(jq -r --arg n "$first_name" '.assets[]? | select(.name==$n) | .browser_download_url' "$release_json" | head -n1)"
  fi
  rm -f "$release_json"

  if [[ -z "${first_name:-}" || -z "${first_url:-}" ]]; then
    echo "错误：未找到 ${MIHOMO_OS}/${MIHOMO_ARCH} 的 mihomo .gz 资源" >&2
    exit 1
  fi
  echo "$first_name|$first_url"
}

prepare_local_binaries() {
  if [[ ! -x "$BUILD_SCRIPT" ]]; then
    echo "错误：构建脚本不存在或不可执行: $BUILD_SCRIPT" >&2
    exit 1
  fi
  if ! command -v curl >/dev/null 2>&1 || ! command -v jq >/dev/null 2>&1 || ! command -v gzip >/dev/null 2>&1; then
    echo "错误：dev.sh 需要 curl、jq、gzip" >&2
    exit 1
  fi

  "$BUILD_SCRIPT" linux amd64
  if [[ ! -f "$LOCAL_PROXY_BINARY" ]]; then
    echo "错误：未找到构建产物 $LOCAL_PROXY_BINARY" >&2
    exit 1
  fi
  chmod +x "$LOCAL_PROXY_BINARY"

  mkdir -p "$MIHOMO_CACHE_DIR"
  mkdir -p "$(dirname "$LOCAL_MIHOMO_BINARY")"
  local asset_info asset_name asset_url cache_file
  asset_info="$(resolve_mihomo_asset)"
  asset_name="${asset_info%%|*}"
  asset_url="${asset_info#*|}"
  cache_file="$MIHOMO_CACHE_DIR/$asset_name"

  if [[ ! -s "$cache_file" ]]; then
    echo "下载 mihomo: $asset_name"
    curl -fL "$asset_url" -o "$cache_file"
  fi
  gzip -dc "$cache_file" >"$LOCAL_MIHOMO_BINARY"
  chmod +x "$LOCAL_MIHOMO_BINARY"

  mkdir -p "$LOCAL_BIN_DIR"
  cp "$LOCAL_PROXY_BINARY" "$LOCAL_BIN_DIR/proxy-pool"
  cp "$LOCAL_MIHOMO_BINARY" "$LOCAL_BIN_DIR/mihomo"
  chmod +x "$LOCAL_BIN_DIR/proxy-pool" "$LOCAL_BIN_DIR/mihomo"
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
    prepare_local_binaries
    compose up -d --build
    ;;
  down)
    compose down
    ;;
  restart)
    prepare_local_binaries
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
