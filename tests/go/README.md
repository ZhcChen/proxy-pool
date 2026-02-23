# Go 测试

## 前置条件

1. 在项目根目录启动服务（统一容器链路）：

```bash
docker compose up -d --build
```

若本机是旧版 Docker，也可使用：

```bash
docker-compose up -d --build
```

2. 确认服务可访问：`http://127.0.0.1:3320`

## 运行

```bash
cd tests/go
TEST_BASE_URL=http://127.0.0.1:3320 \
TEST_ADMIN_TOKEN=<ADMIN_TOKEN> \
TEST_OPENAPI_TOKEN=<OPENAPI_TOKEN> \
go test ./... -v
```

说明：

- `TEST_BASE_URL` 默认 `http://127.0.0.1:3320`
- `TEST_ADMIN_TOKEN`、`TEST_OPENAPI_TOKEN` 若不传，会回退到 `docker-compose.yml` 中当前写死值
- 测试不会再自行拉起 bun/go 进程，统一连接已运行服务
- 涉及“服务回连本地 mock 订阅服务”的用例，在当前网络环境不可达时会自动 `skip`
