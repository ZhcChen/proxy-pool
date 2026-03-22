# Instances Pagination And Health Concurrency Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 为实例页增加服务端分页，并新增可配置的延迟检测并发数，降低批量检测与单个检测结果的偏差。

**Architecture:** 保持当前 Go + Gin + HTMX 服务端渲染方案。实例页分页通过 `page` 查询参数驱动，批量检测并发通过 `Settings.healthCheckConcurrency` 统一配置，并复用到实例与订阅的批量检测路径。

**Tech Stack:** Go 1.25、Gin、HTMX、SQLite

---

### Task 1: 写失败测试

**Files:**
- Modify: `api/src/server/ui_htmx_test.go`
- Create: `api/src/server/settings_health_concurrency_test.go`

**Step 1: 写实例页分页失败测试**

- 构造超过 20 条实例。
- 访问 `/ui/tab/instances?page=2`。
- 断言只渲染第二页数据，并带有分页控件。

**Step 2: 写设置项失败测试**

- 断言设置页包含 `healthCheckConcurrency` 输入项。
- 断言设置接口接受正整数并拒绝非正整数。

**Step 3: 写并发 helper 失败测试**

- 断言默认并发为 `2`。
- 断言配置值会被限制在 `[1, total]` 范围内。

### Task 2: 实现实例页分页

**Files:**
- Modify: `api/src/server/ui_htmx.go`

**Step 1: 增加分页 helper**

- 解析 `page` 参数。
- 计算总页数、当前页、当前页切片。

**Step 2: 渲染分页控件**

- 在实例列表底部输出页码信息和上一页/下一页按钮。

**Step 3: 保持页码上下文**

- 所有刷新实例页的按钮与表单透传 `page`。

### Task 3: 实现检测并发设置

**Files:**
- Modify: `api/src/server/types.go`
- Modify: `api/src/server/storage.go`
- Modify: `api/src/server/app.go`
- Modify: `api/src/server/ui_htmx.go`

**Step 1: 新增设置字段**

- `Settings.healthCheckConcurrency`
- 默认值 `2`
- 缺省或非法时回退到默认值

**Step 2: 接入设置保存与展示**

- 设置页增加输入框
- 保存接口校验为正整数

**Step 3: 统一接入批量检测**

- `checkAllInstances(...)`
- 订阅节点批量检测
- 自动健康检查通过 `checkAllInstances(...)` 自动生效

### Task 4: 验证

**Files:**
- Modify: `tests/go/auth/ui_htmx_smoke_test.go`（如有必要）

**Step 1: 运行服务端测试**

- `cd api && go test ./src/server -count=1`

**Step 2: 运行相关集成测试**

- `cd tests/go && go test ./auth -run 'TestUI_HTMX_EntryAndLoginFlow|TestSettings_HealthCheckConcurrency' -count=1`

**Step 3: 重启容器验证**

- `./scripts/dev.sh restart`
- 手动确认实例页分页和设置页新字段可见
