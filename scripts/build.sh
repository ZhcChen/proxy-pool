#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_DIR="$ROOT_DIR/api"
DIST_DIR="$ROOT_DIR/dist"
PKG="./cmd/proxy-pool"

usage() {
  cat <<'EOF'
用法:
  ./scripts/build.sh
  ./scripts/build.sh local
  ./scripts/build.sh macos [amd64|arm64]
  ./scripts/build.sh linux [amd64|arm64]
  ./scripts/build.sh windows [amd64|arm64]
  ./scripts/build.sh all

说明:
  - 无参数默认构建: linux/amd64
  - 默认构建输出目录: ./dist
  - 默认架构: amd64
  - all 会构建:
    - macos: amd64, arm64
    - linux: amd64, arm64
    - windows: amd64, arm64
EOF
}

build_one() {
  local goos="$1"
  local goarch="$2"
  local ext=""
  local output="$DIST_DIR/proxy-pool-${goos}-${goarch}"
  if [[ "$goos" == "windows" ]]; then
    ext=".exe"
  fi

  mkdir -p "$DIST_DIR"
  echo "构建: ${goos}/${goarch} -> ${output}${ext}"
  (
    cd "$API_DIR"
    GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "-s -w" -o "${output}${ext}" "$PKG"
  )
}

build_local() {
  local goos
  local goarch
  goos="$(go env GOOS)"
  goarch="$(go env GOARCH)"
  build_one "$goos" "$goarch"
}

target="${1:-linux}"
arch="${2:-amd64}"

case "$target" in
  local)
    build_local
    ;;
  macos)
    build_one "darwin" "$arch"
    ;;
  linux)
    build_one "linux" "$arch"
    ;;
  windows)
    build_one "windows" "$arch"
    ;;
  all)
    build_one "darwin" "amd64"
    build_one "darwin" "arm64"
    build_one "linux" "amd64"
    build_one "linux" "arm64"
    build_one "windows" "amd64"
    build_one "windows" "arm64"
    ;;
  *)
    usage
    exit 1
    ;;
esac
