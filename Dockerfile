FROM debian:bookworm-slim

RUN set -eux; \
  apt-get update; \
  apt-get install -y --no-install-recommends ca-certificates lsof; \
  rm -rf /var/lib/apt/lists/*; \
  mkdir -p /data; \
  chmod 777 /data

WORKDIR /app

ENV HOST=0.0.0.0
ENV PORT=3320
ENV DATA_DIR=/data

EXPOSE 3320

ENTRYPOINT ["proxy-pool"]
