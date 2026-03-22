#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_SCRIPT="$ROOT_DIR/scripts/build.sh"

LOCAL_PROXY_BINARY="$ROOT_DIR/dist/proxy-pool-linux-amd64"
LOCAL_MIHOMO_BINARY="$ROOT_DIR/dist/mihomo-linux-amd64"
LOCAL_BIN_DIR="$ROOT_DIR/bin"

REMOTE_ALIAS="ali3"
REMOTE_DIR="/opt/proxy-pool"

MIHOMO_REPO="${MIHOMO_REPO:-MetaCubeX/mihomo}"
MIHOMO_OS="linux"
MIHOMO_ARCH="amd64"
MIHOMO_CACHE_DIR="$ROOT_DIR/api/src/server/assets/mihomo/cache"

MIHOMO_TAG=""
MIHOMO_ASSET_NAME=""
MIHOMO_ASSET_URL=""

C_RESET=""
C_DIM=""
C_INFO=""
C_OK=""
C_WARN=""
C_ERR=""
C_BOLD=""

usage() {
  cat <<'EOF'
用法:
  ./scripts/deploy.sh
  ./scripts/deploy.sh --remote ali3
  ./scripts/deploy.sh --remote your-alias --remote-dir /opt/proxy-pool

说明:
  - 默认远端别名: ali3
  - 默认远端目录: /opt/proxy-pool
  - 会先准备并上传二进制：
    1) proxy-pool -> /opt/proxy-pool/bin/proxy-pool
    2) mihomo     -> /opt/proxy-pool/bin/mihomo
  - docker-compose.yml 若远端已存在则不覆盖
  - 远端自动执行: docker compose up -d --force-recreate
EOF
}

init_colors() {
  if [[ -t 1 ]] && [[ -z "${NO_COLOR:-}" ]] && [[ "${TERM:-}" != "dumb" ]]; then
    C_RESET=$'\033[0m'
    C_DIM=$'\033[2m'
    C_INFO=$'\033[36m'
    C_OK=$'\033[32m'
    C_WARN=$'\033[33m'
    C_ERR=$'\033[31m'
    C_BOLD=$'\033[1m'
  fi
}

log_info() {
  printf "%b[deploy]%b %b%s%b\n" "$C_DIM" "$C_RESET" "$C_INFO" "$*" "$C_RESET"
}

log_ok() {
  printf "%b[deploy]%b %b%s%b\n" "$C_DIM" "$C_RESET" "$C_OK" "$*" "$C_RESET"
}

log_warn() {
  printf "%b[deploy]%b %b%s%b\n" "$C_DIM" "$C_RESET" "$C_WARN" "$*" "$C_RESET"
}

log_error() {
  printf "%b[deploy]%b %b%s%b\n" "$C_DIM" "$C_RESET" "$C_ERR" "$*" "$C_RESET" >&2
}

step() {
  printf "\n%b==> %s%b\n" "$C_BOLD" "$*" "$C_RESET"
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    log_error "错误：未找到命令 $cmd"
    exit 1
  fi
}

resolve_mihomo_asset() {
  local release_json tag asset_url=""
  local preferred_names first_name first_url

  release_json="$(mktemp)"
  curl -fsSL "https://api.github.com/repos/${MIHOMO_REPO}/releases/latest" -o "$release_json"
  tag="$(jq -r '.tag_name // empty' "$release_json")"
  if [[ -z "$tag" ]]; then
    rm -f "$release_json"
    log_error "错误：无法解析 ${MIHOMO_REPO} 最新 release tag"
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
      MIHOMO_TAG="$tag"
      MIHOMO_ASSET_NAME="$name"
      MIHOMO_ASSET_URL="$asset_url"
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
    log_error "错误：未找到 ${MIHOMO_OS}/${MIHOMO_ARCH} 的 mihomo .gz 资源"
    exit 1
  fi

  MIHOMO_TAG="$tag"
  MIHOMO_ASSET_NAME="$first_name"
  MIHOMO_ASSET_URL="$first_url"
}

prepare_mihomo_binary() {
  local cache_file
  resolve_mihomo_asset

  mkdir -p "$MIHOMO_CACHE_DIR"
  mkdir -p "$(dirname "$LOCAL_MIHOMO_BINARY")"

  cache_file="$MIHOMO_CACHE_DIR/$MIHOMO_ASSET_NAME"
  if [[ -s "$cache_file" ]]; then
    log_ok "复用本地 mihomo 缓存: $MIHOMO_ASSET_NAME"
  else
    log_info "下载 mihomo: $MIHOMO_ASSET_NAME"
    curl -fL "$MIHOMO_ASSET_URL" -o "$cache_file"
  fi

  gzip -dc "$cache_file" >"$LOCAL_MIHOMO_BINARY"
  chmod +x "$LOCAL_MIHOMO_BINARY"
  log_ok "mihomo 已准备: $LOCAL_MIHOMO_BINARY (tag=$MIHOMO_TAG)"
}

prepare_proxy_binary() {
  if [[ ! -x "$BUILD_SCRIPT" ]]; then
    log_error "错误：构建脚本不存在或不可执行: $BUILD_SCRIPT"
    exit 1
  fi
  log_info "构建 proxy-pool 二进制（linux/amd64）"
  "$BUILD_SCRIPT" linux amd64
  if [[ ! -f "$LOCAL_PROXY_BINARY" ]]; then
    log_error "错误：未找到构建产物 $LOCAL_PROXY_BINARY"
    exit 1
  fi
  chmod +x "$LOCAL_PROXY_BINARY"
  log_ok "proxy-pool 已构建: $LOCAL_PROXY_BINARY"
}

prepare_local_bin_dir() {
  mkdir -p "$LOCAL_BIN_DIR"
  cp "$LOCAL_PROXY_BINARY" "$LOCAL_BIN_DIR/proxy-pool"
  cp "$LOCAL_MIHOMO_BINARY" "$LOCAL_BIN_DIR/mihomo"
  chmod +x "$LOCAL_BIN_DIR/proxy-pool" "$LOCAL_BIN_DIR/mihomo"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --remote|-r)
      if [[ $# -lt 2 ]]; then
        log_error "错误：--remote 需要一个值"
        exit 1
      fi
      REMOTE_ALIAS="$2"
      shift 2
      ;;
    --remote-dir)
      if [[ $# -lt 2 ]]; then
        log_error "错误：--remote-dir 需要一个值"
        exit 1
      fi
      REMOTE_DIR="$2"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      log_error "错误：未知参数 $1"
      usage
      exit 1
      ;;
  esac
done

if [[ -z "$REMOTE_ALIAS" ]]; then
  log_error "错误：远端别名不能为空"
  exit 1
fi
if [[ -z "$REMOTE_DIR" ]]; then
  log_error "错误：远端目录不能为空"
  exit 1
fi
if [[ "$REMOTE_DIR" =~ [[:space:]] ]]; then
  log_error "错误：远端目录不支持空格，请使用无空格路径"
  exit 1
fi

if [[ "$REMOTE_DIR" == /* ]]; then
  REMOTE_DIR_SSH="$REMOTE_DIR"
  REMOTE_DIR_SCP="$REMOTE_DIR"
else
  REMOTE_DIR_SSH="\$HOME/$REMOTE_DIR"
  REMOTE_DIR_SCP="~/$REMOTE_DIR"
fi

require_cmd ssh
require_cmd scp
require_cmd curl
require_cmd jq
require_cmd gzip

init_colors

step "本地准备二进制"
prepare_proxy_binary
prepare_mihomo_binary
prepare_local_bin_dir

step "上传部署文件"
log_info "检查远端连接: $REMOTE_ALIAS"
ssh "$REMOTE_ALIAS" "echo connected >/dev/null"
log_info "创建远端目录: $REMOTE_DIR_SSH"
ssh "$REMOTE_ALIAS" "set -e; mkdir -p \"$REMOTE_DIR_SSH/bin\" \"$REMOTE_DIR_SSH/data\""

TMP_PROXY_BIN="proxy-pool.tmp.$$"
TMP_MIHOMO_BIN="mihomo.tmp.$$"
log_info "上传 proxy-pool -> $REMOTE_DIR_SCP/bin/proxy-pool"
scp "$LOCAL_PROXY_BINARY" "$REMOTE_ALIAS:$REMOTE_DIR_SCP/bin/$TMP_PROXY_BIN"
log_info "上传 mihomo -> $REMOTE_DIR_SCP/bin/mihomo"
scp "$LOCAL_MIHOMO_BINARY" "$REMOTE_ALIAS:$REMOTE_DIR_SCP/bin/$TMP_MIHOMO_BIN"
ssh "$REMOTE_ALIAS" "set -e; mv \"$REMOTE_DIR_SSH/bin/$TMP_PROXY_BIN\" \"$REMOTE_DIR_SSH/bin/proxy-pool\"; mv \"$REMOTE_DIR_SSH/bin/$TMP_MIHOMO_BIN\" \"$REMOTE_DIR_SSH/bin/mihomo\"; chmod +x \"$REMOTE_DIR_SSH/bin/proxy-pool\" \"$REMOTE_DIR_SSH/bin/mihomo\""

log_info "上传 Dockerfile"
scp "$ROOT_DIR/Dockerfile" "$REMOTE_ALIAS:$REMOTE_DIR_SCP/Dockerfile"
if [[ -f "$ROOT_DIR/.dockerignore" ]]; then
  log_info "上传 .dockerignore"
  scp "$ROOT_DIR/.dockerignore" "$REMOTE_ALIAS:$REMOTE_DIR_SCP/.dockerignore"
fi
if ssh "$REMOTE_ALIAS" "test -f \"$REMOTE_DIR_SSH/docker-compose.yml\""; then
  log_warn "远端已存在 docker-compose.yml，跳过覆盖"
else
  log_info "上传 docker-compose.yml（远端不存在）"
  scp "$ROOT_DIR/docker-compose.yml" "$REMOTE_ALIAS:$REMOTE_DIR_SCP/docker-compose.yml"
fi

log_info "清理远端旧部署遗留文件（若存在）"
ssh "$REMOTE_ALIAS" "rm -f \"$REMOTE_DIR_SSH/docker-compose.deploy.yml\" \"$REMOTE_DIR_SSH/Dockerfile.runtime\""

step "远端启动服务"
log_info "执行远端命令: cd $REMOTE_DIR_SSH && docker compose up -d --force-recreate"
ssh "$REMOTE_ALIAS" "set -e; cd \"$REMOTE_DIR_SSH\"; if docker compose version >/dev/null 2>&1; then docker compose up -d --force-recreate; docker compose ps proxy-pool; elif command -v docker-compose >/dev/null 2>&1; then docker-compose up -d --force-recreate; docker-compose ps proxy-pool; else echo '错误：远端未安装 docker compose/docker-compose' >&2; exit 1; fi"
log_ok "远端服务已启动"

step "完成"
log_ok "部署文件上传完成"
log_ok "远端目录: $REMOTE_DIR_SSH"
log_ok "二进制路径: $REMOTE_DIR_SSH/bin/proxy-pool"
log_ok "内核路径: $REMOTE_DIR_SSH/bin/mihomo"
log_ok "mihomo 版本: $MIHOMO_TAG ($MIHOMO_ASSET_NAME)"
