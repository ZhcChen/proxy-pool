# SPEC-001：Go + Gin + HTML/HTMX 全量迁移技术方案（`src` 目录版）

## 1. 方案目标

1. 基于当前 Bun 实现做功能基线冻结，按基线全量迁移到 Go 1.25 + Gin。
2. 管理页改造为 HTML + HTMX，`htmx` 仅本地引用。
3. Web 模板与静态资产通过 `go:embed` 打包到单二进制。
4. 工程目录采用 `src` 风格组织，保证后续维护可扩展。

## 2. Bun 现状功能分析

### 2.1 核心文件职责映射

| Bun 文件 | 当前职责 | Go 迁移目标 |
| --- | --- | --- |
| `api/src/index.ts` | 路由、鉴权、业务编排、调度入口、静态文件 | `src/transport/http/*` + `src/service/*` + `src/bootstrap/*` |
| `api/src/storage.ts` | SQLite kv、状态归一化、旧 state.json 迁移 | `src/store/sqlite_kv.go` + `src/store/state_store.go` |
| `api/src/mihomo.ts` | mihomo 进程、日志、健康检测、订阅检测器池 | `src/runtime/mihomo_manager.go` + `src/runtime/proxy_checker_pool.go` |
| `api/src/mihomoInstaller.ts` | GitHub release 解析与安装 | `src/runtime/mihomo_installer.go` |
| `api/src/subscription.ts` | YAML/base64 订阅解析 | `src/service/subscription_parser.go` |
| `web/public/app.js` | 管理页全部交互逻辑 | `src/web/templates/*` + `src/transport/http/pages/*` + 少量辅助 JS |

### 2.2 全量 API 基线清单（必须等价）

| 方法 | 路径 | 鉴权 | 说明 |
| --- | --- | --- | --- |
| POST | `/api/login` | 无 | Token 登录 |
| GET | `/api/system/ips` | ADMIN | 获取网卡 IP 列表 |
| POST | `/api/settings/detect-public-ip` | ADMIN | 公网 IP 探测并可写回设置 |
| GET | `/api/mihomo/status` | ADMIN | 内核状态 |
| POST | `/api/mihomo/latest` | ADMIN | 查询最新版本 |
| POST | `/api/mihomo/install` | ADMIN | 安装/更新内核 |
| GET | `/api/state` | ADMIN | 全量状态 |
| GET | `/api/settings` | ADMIN | 读取设置 |
| POST | `/api/settings/reset-proxy-auth` | ADMIN | 重置代理认证凭据 |
| PUT | `/api/settings` | ADMIN | 更新设置 |
| GET | `/api/subscriptions` | ADMIN | 订阅列表 |
| POST | `/api/subscriptions` | ADMIN | 创建订阅（URL/YAML） |
| PUT | `/api/subscriptions/:id` | ADMIN | 更新订阅 |
| POST | `/api/subscriptions/:id/refresh` | ADMIN | 更新订阅（刷新） |
| DELETE | `/api/subscriptions/:id` | ADMIN | 删除订阅（有实例引用时拒绝） |
| GET | `/api/subscriptions/:id/proxies` | ADMIN | 获取节点列表与健康状态 |
| GET | `/api/subscriptions/availability` | ADMIN | 全局可用性统计 |
| GET | `/api/subscriptions/:id/availability` | ADMIN | 订阅级可用性统计 |
| POST | `/api/subscriptions/:id/proxies/check` | ADMIN | 节点检测（单个/批量/全部） |
| GET | `/api/instances` | ADMIN | 实例列表 |
| PUT | `/api/instances/:id` | ADMIN | 更新实例（当前仅 autoSwitch） |
| POST | `/api/instances/batch` | ADMIN | 批量创建实例 |
| POST | `/api/instances/check-all` | ADMIN | 检测全部运行实例 |
| POST | `/api/instances` | ADMIN | 创建实例（单个/自动选节点） |
| POST | `/api/instances/:id/start` | ADMIN | 启动实例 |
| POST | `/api/instances/:id/stop` | ADMIN | 停止实例 |
| GET | `/api/instances/:id/logs` | ADMIN | 获取日志 |
| POST | `/api/instances/:id/check` | ADMIN | 检测实例节点可用性 |
| DELETE | `/api/instances/:id` | ADMIN | 删除实例 |
| GET | `/api/pool` | ADMIN | 管理端代理池列表 |
| GET | `/openapi/pool` | OPENAPI | 对外代理池列表 |

## 3. 目标 Go 工程目录（`src` 风格）

```text
api/
├── cmd/
│   └── proxy-pool/
│       └── main.go
├── src/
│   ├── bootstrap/
│   │   ├── app.go
│   │   ├── scheduler.go
│   │   └── shutdown.go
│   ├── config/
│   │   ├── env.go
│   │   └── constants.go
│   ├── domain/
│   │   ├── model.go
│   │   └── errors.go
│   ├── store/
│   │   ├── sqlite_kv.go
│   │   └── state_store.go
│   ├── runtime/
│   │   ├── mihomo_manager.go
│   │   ├── proxy_checker_pool.go
│   │   └── mihomo_installer.go
│   ├── service/
│   │   ├── auth_service.go
│   │   ├── settings_service.go
│   │   ├── subscription_service.go
│   │   ├── instance_service.go
│   │   ├── pool_service.go
│   │   ├── health_service.go
│   │   └── subscription_parser.go
│   ├── transport/
│   │   └── http/
│   │       ├── router.go
│   │       ├── middleware/
│   │       │   ├── admin_auth.go
│   │       │   └── openapi_auth.go
│   │       ├── handlers/
│   │       │   ├── api_auth.go
│   │       │   ├── api_settings.go
│   │       │   ├── api_subscriptions.go
│   │       │   ├── api_instances.go
│   │       │   ├── api_pool.go
│   │       │   └── openapi_pool.go
│   │       └── pages/
│   │           ├── page_root.go
│   │           ├── page_instances.go
│   │           ├── page_subscriptions.go
│   │           ├── page_settings.go
│   │           └── page_pool.go
│   └── web/
│       ├── embed.go
│       ├── templates/
│       │   ├── layout.html
│       │   ├── page_login.html
│       │   ├── page_instances.html
│       │   ├── page_subscriptions.html
│       │   ├── page_settings.html
│       │   ├── page_pool.html
│       │   └── partials/
│       └── assets/
│           ├── style.css
│           ├── favicon.ico
│           ├── favicon.svg
│           ├── app-lite.js
│           └── vendor/
│               └── htmx.min.js
├── go.mod
└── go.sum
```

## 4. 数据与状态兼容设计

1. 继续使用 `data/state.sqlite` 的 `kv` 表，不做结构性变更。
2. 关键键保持一致：`state`、`mihomo_install`、`proxy_health:{subscriptionId}`。
3. 启动时仍支持读取旧 `state.json` 并迁移到 SQLite，再备份为 `state.json.bak`。
4. 继续使用 `data/subscriptions/{id}.yaml` 存储订阅快照。
5. 继续使用 `data/instances/{instanceId}` 和 `data/proxy-checkers/{subId}`。
6. Go 数据访问采用 `database/sql` 抽象层，驱动选型为 `modernc.org/sqlite`（纯 Go，无 CGO）。
7. SQLite 连接初始化包含基础 PRAGMA（如 WAL、busy_timeout、foreign_keys）以保持并发和一致性行为。

### 4.1 SQLite 驱动决策（已确认）

1. 主方案：`database/sql + modernc.org/sqlite`。
2. 选型理由：
   - 无需 CGO，容器构建和跨平台更稳定。
   - 保持单二进制部署目标，不引入 C 编译工具链。
3. 构建约束：
   - 默认按 `CGO_ENABLED=0` 构建与发布。
4. 兜底方案：
   - 若后续出现驱动级兼容问题，可回退到 `github.com/mattn/go-sqlite3`（需 CGO）。
   - 通过 `src/store` 抽象层隔离驱动差异，避免业务层改动。

## 5. 运行时与调度迁移设计

1. 实例运行时：
   - `os/exec` 启动 mihomo 进程。
   - 启动前写入配置：`mixed-port`、`external-controller`、`proxy-groups`、`rules`。
   - `autoSwitch=true` 使用 `fallback` 组，`autoSwitch=false` 使用 `select` 组。
2. 健康检测：
   - 单实例检测调用 controller `/proxies/{name}/delay`。
   - 订阅节点检测采用订阅级“检测器进程池”，按 `subscriptionId + updatedAt` 复用。
3. 调度任务：
   - 启动后执行 autoStart。
   - 按 `healthCheckIntervalSec` 定时检测运行中实例。
4. 进程清理：
   - 优先优雅退出，超时后强制 kill。
   - 按端口反查并清理遗留进程，保持现网行为一致。

## 6. 订阅解析与 fallback 迁移设计

1. 保持 YAML 与 base64 URI 解析兼容。
2. 保持 warning-only 订阅识别逻辑（提示节点场景继续 fallback）。
3. 保持 URL fallback 顺序：
   - 原始 URL
   - `flag=clash-meta`
   - `flag=meta`
   - `flag=clash`
4. 成功解析后持久化“有效 URL”（包含有效 flag），供后续刷新直接使用。

## 7. HTTP 与页面设计

### 7.1 JSON API 兼容策略

1. `/api/*` 与 `/openapi/*` 路径和响应结构保持兼容。
2. 状态码、错误字段（`ok/error/details`）保持兼容。
3. 鉴权行为保持兼容：
   - 管理 API 只认 `ADMIN_TOKEN`
   - OpenAPI 只认 `OPENAPI_TOKEN`

### 7.2 HTML + HTMX 页面策略

1. 页面入口：
   - `GET /` 返回布局与首屏内容。
   - 页面通过 HTMX 拉取分区片段（实例、订阅、设置、代理池）。
2. 建议片段接口（新增，不影响 JSON API）：
   - `GET /ui/instances`
   - `GET /ui/subscriptions`
   - `GET /ui/settings`
   - `GET /ui/pool`
   - `GET /ui/subscriptions/:id/proxies-modal`
   - `GET /ui/instances/:id/logs-modal`
3. 前端交互原则：
   - 业务动作走 HTMX 请求并返回 HTML 片段。
   - 保留少量 `app-lite.js` 仅处理复制、modal 开关、确认弹窗等非核心逻辑。
4. 必须保留现有页面功能：
   - 订阅“更新订阅”按钮语义。
   - 节点弹窗宽度 `70%`。
   - 实例创建多选节点能力。
   - 二次添加实例不得重复创建同一 `subscriptionId + proxyName`。

## 8. Web 资源内嵌方案

1. 在 `src/web/embed.go` 使用 `go:embed` 打包：
   - `src/web/templates/**`
   - `src/web/assets/**`
2. 本地依赖固定：
   - `src/web/assets/vendor/htmx.min.js`
3. 禁止页面引用外部 CDN 的 JS/CSS 字体资源。

## 9. 配置与环境变量

1. 保留环境变量：
   - `HOST`、`PORT`、`DATA_DIR`
   - `ADMIN_TOKEN`、`OPENAPI_TOKEN`
   - `MIHOMO_REPO`、`PROXY_HOST`、`PUBLIC_IP_OVERRIDE`
2. 移除运行时对 `WEB_DIR` 的强依赖（调试模式可选覆盖，不作为生产依赖）。
3. `docker-compose.yml` 继续使用 `proxy-pool` 服务名与 `network_mode: host`。

## 10. 迁移执行计划（全量）

### 阶段 A：基线冻结与契约提取

1. 锁定 Bun 基线代码与行为（接口、状态码、字段、页面流程）。
2. 输出接口契约清单与页面功能矩阵。

### 阶段 B：Go 骨架与存储/runtime 迁移

1. 建立 `api/src` 工程骨架与依赖注入。
2. 完成 `database/sql + modernc.org/sqlite` 的存储接入与兼容迁移。
3. 完成 storage、installer、mihomo manager、scheduler 迁移。

### 阶段 C：JSON API 全量迁移

1. 按清单迁移全部 `/api/*` 和 `/openapi/*`。
2. 以 `tests/go/auth/*` 为最小门禁，补齐遗漏接口用例。

### 阶段 D：HTML + HTMX 页面全量迁移

1. 完成登录/实例/订阅/设置/代理池四大页面。
2. 完成 modal 与片段刷新，覆盖现有交互能力。
3. 本地 `htmx` 引入与 embed 验证。

### 阶段 E：容器切换与验收

1. Dockerfile 切为 Go 多阶段构建。
2. `docker compose` 跑通回归测试与 smoke。
3. 输出上线步骤与回滚步骤。

## 11. 测试策略

1. 现有 Go 集成测试保留并迁移运行目标到 Go 服务。
2. 新增 API 契约回归：
   - 订阅全链路（新增/更新/刷新/删除/检测）
   - 实例全链路（创建/批量/多选/启停/检测/删除）
   - 设置与 proxy auth
3. 新增页面 smoke（Go 测试）：
   - 登录
   - 订阅更新
   - 多选创建实例
   - 代理池复制文本渲染
4. 测试执行环境统一 `docker compose`。
5. 新增 SQLite 专项回归：在真实 `state.sqlite` 上执行读写与升级兼容测试。

## 12. 完成定义（DoD）

1. API/页面功能矩阵全部打勾，无功能缺失。
2. 所有 Go 测试通过。
3. 容器镜像仅依赖 Go 二进制运行。
4. 文档更新完成（部署、排障、回滚）。
5. SQLite 运行链路不依赖 CGO。

## 13. 回滚方案

1. 保留 Bun 稳定标签（如 `bun-last-stable`）。
2. 切换失败时回退镜像与入口命令。
3. 因数据结构兼容，回滚继续复用同一 `data/`。

## 14. 附录引用

1. 全功能对照矩阵与 Docker 测试链路：`docs/specs/SPEC-001-migration-matrix-and-test-chain.md`
