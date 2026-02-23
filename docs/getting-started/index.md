# 快速开始

## 依赖

- Docker + Docker Compose
- 无需预装 `mihomo`：可在管理页「设置」里一键安装（从 GitHub Release 下载适配版本）

## 启动（统一方式）

在项目根目录执行：

```bash
docker compose up -d --build
```

旧版 Docker：

```bash
docker-compose up -d --build
```

默认管理页（HTMX）：`http://127.0.0.1:3320`

## 登录与鉴权

- 管理页登录：输入服务端 `ADMIN_TOKEN`
- OpenAPI：使用独立 `OPENAPI_TOKEN`

示例：

```bash
curl -H "Authorization: Bearer <OPENAPI_TOKEN>" \
  http://127.0.0.1:3320/openapi/pool
```

> 安全提示：请勿将管理端口直接暴露到公网，并妥善保管 `ADMIN_TOKEN` 与 `OPENAPI_TOKEN`。
