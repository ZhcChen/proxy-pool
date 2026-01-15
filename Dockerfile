FROM oven/bun:1

WORKDIR /app

# 安装运行时依赖（不同基础镜像可能是 Debian 或 Alpine，做兼容）
RUN set -eux; \
  if command -v apt-get >/dev/null 2>&1; then \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates lsof; \
    rm -rf /var/lib/apt/lists/*; \
  elif command -v apk >/dev/null 2>&1; then \
    apk add --no-cache ca-certificates lsof; \
  else \
    echo "unsupported base image: missing apt-get/apk"; \
    exit 1; \
  fi; \
  mkdir -p /data; \
  chmod 777 /data

# 先复制依赖清单以提升构建缓存命中率
COPY package.json bun.lock ./
COPY api/package.json api/tsconfig.json ./api/
COPY web/package.json ./web/

RUN bun install --frozen-lockfile

# 再复制源码
COPY api/src ./api/src
COPY web/public ./web/public

ENV HOST=0.0.0.0
ENV PORT=3320
ENV DATA_DIR=/data
ENV WEB_DIR=/app/web/public

EXPOSE 3320

CMD ["bun", "--cwd=api", "run", "start"]
