# scripts 使用说明

本目录用于放置本地开发/测试的快捷脚本。  
当前脚本：

- `dev.sh`：统一管理 `proxy-pool` 的容器启动、停止、重启、日志与状态。
- `build.sh`：构建 Go 二进制（支持 macOS / Linux / Windows）。
- `deploy.sh`：编译并上传部署产物到远端服务器（默认别名 `ali3`）。

## 前置条件

- 已安装 Docker Desktop / Docker Engine
- 可用 `docker compose` 或 `docker-compose`

## dev.sh 用法

在项目根目录执行：

```bash
./scripts/dev.sh <command>
```

支持命令：

- `up`：先准备本地二进制（`proxy-pool` + `mihomo`）并后台启动服务
- `down`：停止并删除服务容器（等价 `docker compose down`）
- `restart`：先准备本地二进制，再重启服务（先 `down` 再 `up`）
- `logs`：持续查看 `proxy-pool` 服务日志
- `status`：查看 `proxy-pool` 服务状态

## build.sh 用法

在项目根目录执行：

```bash
./scripts/build.sh <target> [arch]
```

无参数默认构建 `linux/amd64`：

```bash
./scripts/build.sh
```

支持目标：

- `local`：构建当前机器平台二进制
- `macos [amd64|arm64]`：构建 macOS 二进制
- `linux [amd64|arm64]`：构建 Linux 二进制
- `windows [amd64|arm64]`：构建 Windows 二进制（输出 `.exe`）
- `all`：一次性构建三平台常用架构（amd64 + arm64）

输出目录：`./dist`

分平台示例：

```bash
# macOS
./scripts/build.sh macos arm64

# Linux
./scripts/build.sh linux amd64

# Windows
./scripts/build.sh windows amd64
```

## deploy.sh 用法

在项目根目录执行：

```bash
./scripts/deploy.sh [--remote <ssh别名>] [--remote-dir <远端目录>]
```

默认行为：

- 默认远端别名：`ali3`
- 默认远端目录：`/opt/proxy-pool`
- 动态解析 mihomo 最新 `linux/amd64` 资源（`MetaCubeX/mihomo`）：
  - 本地缓存存在则直接复用
  - 本地缓存不存在则自动下载
- 构建 `proxy-pool` 二进制：`dist/proxy-pool-linux-amd64`
- 解压准备 `mihomo` 二进制：`dist/mihomo-linux-amd64`
- mihomo 下载缓存位于 `api/src/server/assets/mihomo/cache/`，已在 `.gitignore` 中忽略，不会提交到 Git
- 上传以下文件：
  - `dist/proxy-pool-linux-amd64` -> `<远端>/bin/proxy-pool`
  - `dist/mihomo-linux-amd64` -> `<远端>/bin/mihomo`
  - `Dockerfile`
  - `.dockerignore`（若存在）
  - `docker-compose.yml`（仅当远端不存在时上传；若已存在则跳过覆盖）
- 远端自动执行：
  - `docker compose up -d --force-recreate`

示例：

```bash
# 默认上传到 ali3:/opt/proxy-pool
./scripts/deploy.sh

# 指定服务器别名
./scripts/deploy.sh --remote prod-a

# 指定远端目录
./scripts/deploy.sh --remote prod-a --remote-dir /opt/proxy-pool
```

## 常用流程

开发启动：

```bash
./scripts/dev.sh up
./scripts/dev.sh status
```

调试日志：

```bash
./scripts/dev.sh logs
```

结束开发：

```bash
./scripts/dev.sh down
```

## 常见问题

- 报错“未找到 docker compose 或 docker-compose”：
  - 请先安装 Docker Compose，并确认命令可在终端执行。
