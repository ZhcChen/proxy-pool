# SPEC-001 附录：全功能对照矩阵与 Docker 测试链路

## 1. 目标

本附录用于把 Bun 现网能力冻结为可执行清单，并给出 Go 迁移后的验收链路，确保迁移过程可验证、可回归、可追踪。

## 2. 基线来源（冻结）

1. 后端入口：`api/src/index.ts`
2. 运行时：`api/src/mihomo.ts`
3. 存储：`api/src/storage.ts`
4. 安装器：`api/src/mihomoInstaller.ts`
5. 订阅解析：`api/src/subscription.ts`
6. 前端行为：`web/public/app.js` + `web/public/index.html`
7. 现有回归：`tests/go/auth/*.go`

## 3. 后端 API 全量对照矩阵

| 编号 | 接口 | Bun 行为基线 | Go 目标落点（`src`） | 当前测试覆盖 | 迁移后要求 |
| --- | --- | --- | --- | --- | --- |
| A-01 | `POST /api/login` | token 登录与错误码 | `transport/http/handlers/api_auth.go` | `login_test.go` | 保持响应字段与状态码 |
| A-02 | `GET /api/system/ips` | 返回网卡 IP 列表与最佳地址 | `handlers/api_settings.go` | 无 | 新增测试 |
| A-03 | `POST /api/settings/detect-public-ip` | 多 provider 探测并可写回 | `handlers/api_settings.go` | 无 | 新增测试 |
| A-04 | `GET /api/mihomo/status` | 返回安装状态 | `handlers/api_mihomo.go` | 无 | 新增测试 |
| A-05 | `POST /api/mihomo/latest` | 查询最新 release | `handlers/api_mihomo.go` | 无 | 新增测试（mock） |
| A-06 | `POST /api/mihomo/install` | 下载并安装内核 | `handlers/api_mihomo.go` | 无 | 新增测试（mock） |
| A-07 | `GET /api/state` | 返回全量 state + runtime | `handlers/api_state.go` | 无 | 新增测试 |
| A-08 | `GET /api/settings` | 返回 settings | `handlers/api_settings.go` | `login_test.go`（间接） | 保持兼容 |
| A-09 | `POST /api/settings/reset-proxy-auth` | 重置凭据并保留 enabled | `handlers/api_settings.go` | `proxy_auth_test.go` | 保持兼容 |
| A-10 | `PUT /api/settings` | 更新设置 + 参数校验 + 重启定时任务 | `handlers/api_settings.go` | `proxy_auth_test.go`（部分） | 补全校验测试 |
| A-11 | `GET /api/subscriptions` | 列表查询 | `handlers/api_subscriptions.go` | 无 | 新增测试 |
| A-12 | `POST /api/subscriptions` | URL/YAML 新增 + fallback + snapshot | `handlers/api_subscriptions.go` | `subscription_auto_flag_test.go`（部分） | 保持兼容 |
| A-13 | `PUT /api/subscriptions/:id` | 编辑名称/URL/YAML | `handlers/api_subscriptions.go` | `subscription_update_test.go` | 保持兼容 |
| A-14 | `POST /api/subscriptions/:id/refresh` | 刷新订阅并回写有效 URL | `handlers/api_subscriptions.go` | `subscription_auto_flag_test.go`（部分） | 保持兼容 |
| A-15 | `DELETE /api/subscriptions/:id` | 有实例引用则拒绝删除 | `handlers/api_subscriptions.go` | `subscription_delete_test.go`（不含引用拒删） | 新增引用拒删测试 |
| A-16 | `GET /api/subscriptions/:id/proxies` | 节点+健康状态 | `handlers/api_subscriptions.go` | 无 | 新增测试 |
| A-17 | `GET /api/subscriptions/availability` | 全局可用性统计 | `handlers/api_subscriptions.go` | 无 | 新增测试 |
| A-18 | `GET /api/subscriptions/:id/availability` | 订阅维度可用性统计 | `handlers/api_subscriptions.go` | 无 | 新增测试 |
| A-19 | `POST /api/subscriptions/:id/proxies/check` | 节点检测（all/names/proxyName） | `handlers/api_subscriptions.go` | 无 | 新增测试 |
| A-20 | `GET /api/instances` | 实例列表 + runtime/health | `handlers/api_instances.go` | 无 | 新增测试 |
| A-21 | `PUT /api/instances/:id` | 更新 autoSwitch，运行中实例需重启 | `handlers/api_instances.go` | 无 | 新增测试 |
| A-22 | `POST /api/instances/batch` | 批量创建 + 可用性筛选 + 启动 | `handlers/api_instances.go` | 无 | 新增测试 |
| A-23 | `POST /api/instances/check-all` | 检测所有运行实例 | `handlers/api_instances.go` | 无 | 新增测试 |
| A-24 | `POST /api/instances` | 单实例创建（auto/指定节点） | `handlers/api_instances.go` | 无 | 新增测试 |
| A-25 | `POST /api/instances/:id/start` | 启动前检测 + autoSwitch fallback | `handlers/api_instances.go` | 无 | 新增测试 |
| A-26 | `POST /api/instances/:id/stop` | 停止实例并清理 | `handlers/api_instances.go` | 无 | 新增测试 |
| A-27 | `GET /api/instances/:id/logs` | 读取日志缓存 | `handlers/api_instances.go` | 无 | 新增测试 |
| A-28 | `POST /api/instances/:id/check` | 单实例健康检测并落库 | `handlers/api_instances.go` | 无 | 新增测试 |
| A-29 | `DELETE /api/instances/:id` | 删除前静默 stop | `handlers/api_instances.go` | 无 | 新增测试 |
| A-30 | `GET /api/pool` | 管理端代理池导出 | `handlers/api_pool.go` | 无 | 新增测试 |
| A-31 | `GET /openapi/pool` | OpenAPI 独立 token 鉴权 | `handlers/openapi_pool.go` | `openapi_pool_test.go` | 保持兼容 |

## 4. 前端（页面）全量对照矩阵

| 编号 | 页面/模块 | 现有行为基线 | Go + HTMX 迁移要求 |
| --- | --- | --- | --- |
| U-01 | 登录页 | Token 输入登录，localStorage 持久化 | 保持 token 登录流程与错误提示 |
| U-02 | 顶部导航 | 实例/订阅/设置/代理池切换 | 保持 4 tab 导航 |
| U-03 | 设置-内核 | 检查版本、安装/修复安装 | 保持功能与文案语义 |
| U-04 | 设置-网络 | bindAddress/allowLan/logLevel 保存 | 参数校验与保存行为一致 |
| U-05 | 设置-代理认证 | 启用开关、凭据展示、重置 | reset 后 enabled 保持不变 |
| U-06 | 设置-导出 Host | 自动探测公网 IP 并写入 | 覆盖/不覆盖逻辑保持一致 |
| U-07 | 设置-端口与检测 | base port、maxLogLines、health 配置 | 保存后调度刷新行为一致 |
| U-08 | 订阅-新增 | name + url/yaml 添加 | URL fallback + snapshot 行为一致 |
| U-09 | 订阅-更新订阅 | 刷新按钮语义为更新订阅 | 按钮保留“更新订阅”文案 |
| U-10 | 订阅-编辑 | 修改 name/url/yaml | 支持仅改名称、改 URL、改 YAML |
| U-11 | 订阅-删除 | 删除前确认，被引用则失败 | 错误提示可读 |
| U-12 | 节点弹窗 | 节点列表、搜索、单测/全测、复制 | 弹窗宽度保持 `70%` |
| U-13 | 实例-可用性提示 | 可用节点统计展示 | 与 `/availability` 计算一致 |
| U-14 | 实例-单创建 | 订阅+节点+端口+autoStart+autoSwitch | 创建前检测逻辑一致 |
| U-15 | 实例-多选创建 | 多选节点一次性创建多个实例 | 保持功能，不得重复创建同节点实例 |
| U-16 | 实例-批量创建 | 按可用节点批量创建 | 可用性不足时返回详细统计 |
| U-17 | 实例-启停删 | 启动、停止、删除 | 状态刷新与错误提示一致 |
| U-18 | 实例-检测 | 单实例检测/检测全部 | 结果写回健康缓存 |
| U-19 | 实例-编辑 | 编辑 autoSwitch，运行中会重启 | 保持“失败回滚旧配置”语义 |
| U-20 | 实例-复制链接 | 生成 socks5/http URL（可含认证） | IPv6 包裹、auth 编码保持一致 |
| U-21 | 实例-日志 | 弹窗查看并复制日志 | 保持最近 N 行语义 |
| U-22 | 代理池页 | 文本导出 + 复制 | 与 `/api/pool` 保持一致 |
| U-23 | 通用反馈 | toast、loading、错误提示 | 保持关键交互反馈 |

## 5. 运行时与存储对照矩阵

| 编号 | 模块 | Bun 基线 | Go 迁移要求 |
| --- | --- | --- | --- |
| R-01 | 存储表结构 | `state.sqlite` + `kv` | 表结构不变 |
| R-02 | 关键键 | `state`/`mihomo_install`/`proxy_health:*` | 键名不变 |
| R-03 | 旧状态迁移 | `state.json -> sqlite` | 保持迁移与 `.bak` |
| R-04 | 订阅快照 | `data/subscriptions/{id}.yaml` | 保持路径与写入时机 |
| R-05 | 实例配置目录 | `data/instances/{id}` | 保持路径 |
| R-06 | 检测器目录 | `data/proxy-checkers/{subId}` | 保持路径 |
| R-07 | 进程管理 | start/stop/kill/reap | 行为等价 |
| R-08 | 端口清理 | 按端口识别遗留进程并清理 | 行为等价 |
| R-09 | autoStart | 服务启动恢复实例 | 行为等价 |
| R-10 | 定时健康检测 | `healthCheckIntervalSec` 驱动 | 行为等价 |
| R-11 | autoSwitch | fallback/select 配置差异 | 行为等价 |
| R-12 | 订阅检测器复用 | `subscriptionId + updatedAt` | 行为等价 |
| R-13 | SQLite 驱动 | Bun sqlite | Go 用 `database/sql + modernc.org/sqlite` |
| R-14 | 构建模式 | Bun runtime | Go `CGO_ENABLED=0` |

## 6. 测试覆盖矩阵（迁移后）

| 编号 | 测试集 | 目标 | 备注 |
| --- | --- | --- | --- |
| T-01 | `tests/go/auth/login_test.go` | 登录/鉴权兼容 | 保留并改为容器模式 |
| T-02 | `tests/go/auth/openapi_pool_test.go` | OpenAPI 双 token 兼容 | 保留并改为容器模式 |
| T-03 | `tests/go/auth/proxy_auth_test.go` | proxyAuth reset/toggle | 保留并改为容器模式 |
| T-04 | `tests/go/auth/subscription_auto_flag_test.go` | fallback + flag 持久化 | 保留并改为容器模式 |
| T-05 | `tests/go/auth/subscription_update_test.go` | 订阅编辑流程 | 保留并改为容器模式 |
| T-06 | `tests/go/auth/subscription_delete_test.go` | 删除/快照清理 | 保留并补“被引用拒删” |
| T-07 | `tests/go/api/instances_*` | 实例全链路 | 新增 |
| T-08 | `tests/go/api/settings_*` | 设置校验/探测 | 新增 |
| T-09 | `tests/go/api/subscriptions_*` | proxies/availability/check | 新增 |
| T-10 | `tests/go/ui/smoke_*` | HTMX 页面关键流程 | 新增（Go 编写） |
| T-11 | `tests/go/store/sqlite_*` | sqlite 兼容与迁移 | 新增 |

## 7. Docker 启动与测试链路（标准流程）

### 7.1 开发启动链路（必走）

1. 启动：`docker compose up -d --build`
2. 检查容器：`docker compose ps`
3. 健康探测：`curl -fsS http://127.0.0.1:3320/ >/dev/null`
4. 检查鉴权：未带 token 访问 `GET /api/settings` 预期 `401`

通过标准：
1. `proxy-pool` 容器状态 `Up`
2. 页面入口返回 `200`
3. 鉴权接口返回预期状态码

### 7.2 联调链路（接口）

1. 管理 token 登录：`POST /api/login`
2. 读取设置：`GET /api/settings`
3. 读取代理池：`GET /api/pool`
4. OpenAPI 验证：`GET /openapi/pool`（分别验证 401/200/503 场景）

通过标准：
1. 响应字段 `ok/error` 与基线一致
2. 状态码一致

### 7.3 自动回归链路（Go 测试）

1. `docker compose up -d --build`
2. 在宿主执行 Go 测试（统一走容器服务）：

```bash
TEST_BASE_URL=http://127.0.0.1:3320 \
TEST_ADMIN_TOKEN=<ADMIN_TOKEN> \
TEST_OPENAPI_TOKEN=<OPENAPI_TOKEN> \
go test ./tests/go/... -v
```

3. 测试结束：`docker compose down`

要求：
1. 测试框架不再自行拉起 bun/go 进程，统一连容器地址。
2. 若需隔离数据，测试前后清理 `./data` 或使用临时 compose 覆盖挂载。

### 7.4 UI Smoke 链路（Go）

1. 启动容器后执行 `tests/go/ui/...`。
2. 至少覆盖：登录、添加订阅、更新订阅、创建实例（含多选）、代理池展示。
3. 验证页面引用：`/assets/vendor/htmx.min.js`，不存在 CDN。

### 7.5 失败排查链路

1. 收集容器日志：`docker compose logs --tail=300 proxy-pool`
2. 收集 API 响应体与状态码
3. 收集 `data/state.sqlite` 与 `data/subscriptions/*.yaml` 样本
4. 定位到矩阵编号（A/U/R/T）后修复并回归

## 8. 迁移完成门禁（100% 判定）

1. API 对照矩阵 A-01 ~ A-31 全部通过。
2. 页面对照矩阵 U-01 ~ U-23 全部通过。
3. 运行时/存储矩阵 R-01 ~ R-14 全部通过。
4. 测试矩阵 T-01 ~ T-11 全部通过。
5. Docker 启动与测试链路 7.1 ~ 7.5 全部可重复执行。

