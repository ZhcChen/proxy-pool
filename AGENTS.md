# AGENTS.md

## 角色

你是 **Orchestrator**，负责协调软件开发流程、拆解任务、管理确认节点，并在需要时产出 PRD/技术方案/实现与测试。

## 项目目标

为 `mihomo` 代理内核提供**多实例管理**与“代理池”能力：

- 启动/停止 N 个 mihomo 实例（每个实例对应一个节点）
- **一个端口对应一个出口 IP/节点**（mixed-port）
- Web 管理页面：订阅导入、实例管理、日志查看、代理池导出

## 项目结构（当前）

```
proxy-pool/
├── api/                # 后端（Go + Gin）
│   └── src/web/public/ # 管理页静态资源（编译时 embed 进二进制）
├── data/               # 运行时数据（SQLite、实例配置、订阅 YAML）
├── docs/               # 文档驱动开发（PRD/技术方案/规范）
└── tests/              # 测试（优先 Go 1.25）
```

## 工作流程（简化版）

1. 需求澄清 → 输出 PRD（`docs/requirements/`）
2. 技术设计 → 输出技术方案（`docs/specs/`）
3. 方案确认 → 再进入编码与自测

> 说明：PRD 与技术方案两处需要用户确认后再继续推进。

## 本地开发与测试启动规范

- 后续**本地开发、联调、测试**统一使用 `docker compose` 启动服务，不再使用 `bun run dev` / `bun src/index.ts` 等本机直启方式。
- 推荐统一使用根目录脚本：`./scripts/dev.sh up|down|restart|logs|status`（脚本内部封装 `docker compose` / `docker-compose`）。
- 默认启动命令：`docker compose up -d --build`（或旧版本 `docker-compose up -d --build`）。
- 停止命令：`docker compose down`（或 `docker-compose down`）。
- 开发与测试前应先确认容器 `proxy-pool` 处于运行状态，再执行接口验证与测试用例。
