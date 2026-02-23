# proxy-pool

`proxy-pool` 用于批量管理 `mihomo` 多实例，实现“一个端口对应一个节点/出口 IP”的代理池。

## 核心能力

- 订阅导入：支持 URL / YAML 导入，自动解析 `proxies`
- 实例管理：单创建、批量创建、启动/停止、日志查看、可用性检测
- 节点策略：支持 `autoSwitch`（故障自动切换同订阅可用节点）
- 代理池导出：统一导出 `host:port` 列表
- 鉴权：
  - 管理端：`ADMIN_TOKEN`
  - OpenAPI：`OPENAPI_TOKEN`（独立 token，仅开放 `GET /openapi/pool`）

## 技术栈

- 后端：Go 1.25 + Gin
- 存储：SQLite（`database/sql + modernc.org/sqlite`）
- 前端：静态页面（内嵌到 Go 二进制）
- 运行：Docker Compose

## 启动（统一方式）

推荐使用快捷脚本（底层仍是 `docker compose`）：

```bash
./scripts/dev.sh up
```

常用命令：

```bash
./scripts/dev.sh status
./scripts/dev.sh logs
./scripts/dev.sh restart
./scripts/dev.sh down
```

也可直接执行：

```bash
docker compose up -d --build
```

旧版 Docker：

```bash
docker-compose up -d --build
```

停止：

```bash
docker compose down
```

启动后访问：

- 管理页（HTMX）：`http://127.0.0.1:3320`
- 登录方式：输入 `ADMIN_TOKEN`

## 环境变量

常用变量（见 `docker-compose.yml`）：

- `HOST`：监听地址，默认 `0.0.0.0`
- `PORT`：管理端口，默认 `3320`
- `DATA_DIR`：数据目录，默认 `/data`
- `ADMIN_TOKEN`：管理端 token（必填）
- `OPENAPI_TOKEN`：OpenAPI token（选填，独立于 ADMIN）
- `PROXY_HOST`：首次初始化导出 Host（后续可在设置页修改）
- `MIHOMO_REPO`：mihomo release 仓库（默认 `MetaCubeX/mihomo`）

## OpenAPI

仅开放实例池列表：

```bash
curl -H "Authorization: Bearer <OPENAPI_TOKEN>" \
  http://127.0.0.1:3320/openapi/pool
```

文档见：`docs/openapi/index.md`

## 测试

```bash
cd tests/go
TEST_BASE_URL=http://127.0.0.1:3320 \
TEST_ADMIN_TOKEN=<ADMIN_TOKEN> \
TEST_OPENAPI_TOKEN=<OPENAPI_TOKEN> \
go test ./... -v
```

## 目录

- `api/`：Go 服务
- `data/`：运行数据（SQLite、订阅 YAML、实例配置等）
- `docs/`：需求/方案/OpenAPI 文档
- `tests/go/`：Go 测试
