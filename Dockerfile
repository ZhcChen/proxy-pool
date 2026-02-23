FROM golang:1.25-bookworm AS builder

WORKDIR /src

COPY api/go.mod api/go.sum ./api/
RUN cd api && go mod download

COPY api ./api

RUN cd api && \
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w" -o /out/proxy-pool ./cmd/proxy-pool

FROM debian:bookworm-slim

RUN set -eux; \
  apt-get update; \
  apt-get install -y --no-install-recommends ca-certificates lsof; \
  rm -rf /var/lib/apt/lists/*; \
  mkdir -p /data; \
  chmod 777 /data

WORKDIR /app

COPY --from=builder /out/proxy-pool /usr/local/bin/proxy-pool

ENV HOST=0.0.0.0
ENV PORT=3320
ENV DATA_DIR=/data

EXPOSE 3320

ENTRYPOINT ["proxy-pool"]
