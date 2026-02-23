# 测试

## 约定

- 若无其他强制规范，测试用例优先使用 Go 编写（建议 Go 1.25）
- 开发与联调统一先使用 `docker compose` 启动服务

## 运行

```bash
cd tests/go
TEST_BASE_URL=http://127.0.0.1:3320 \
TEST_ADMIN_TOKEN=<ADMIN_TOKEN> \
TEST_OPENAPI_TOKEN=<OPENAPI_TOKEN> \
go test ./... -v
```

说明：

- 测试默认连接已启动服务，不再自行拉起 Bun/Go 进程
- 详情见 `tests/go/README.md`
