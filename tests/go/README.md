# Go 测试

## 运行

在项目根目录启动 API 后再跑测试并不必要：测试会自行拉起 `api`。

执行：

```bash
cd tests/go
go test ./... -v
```

说明：

- 测试会临时拉起 `bun --cwd=api src/index.ts`，并从服务端输出中解析随机账号/密码完成登录验证
- 会使用临时目录作为 `DATA_DIR`，不会污染本地 `data/`

