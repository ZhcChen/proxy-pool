# scripts 使用说明

本目录用于放置本地开发/测试的快捷脚本。  
当前脚本：

- `dev.sh`：统一管理 `proxy-pool` 的容器启动、停止、重启、日志与状态。
- `build.sh`：构建 Go 二进制（支持 macOS / Linux / Windows）。

## 前置条件

- 已安装 Docker Desktop / Docker Engine
- 可用 `docker compose` 或 `docker-compose`

## dev.sh 用法

在项目根目录执行：

```bash
./scripts/dev.sh <command>
```

支持命令：

- `up`：构建并后台启动服务（等价 `docker compose up -d --build`）
- `down`：停止并删除服务容器（等价 `docker compose down`）
- `restart`：重启服务（先 `down` 再 `up`）
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
