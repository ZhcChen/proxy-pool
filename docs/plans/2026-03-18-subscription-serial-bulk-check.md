# 订阅节点串行批量检测 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 把订阅节点弹窗的“检测全部”改成前端串行逐节点检测，并让当前节点行按钮显示加载态。

**Architecture:** 保持现有 Go + Gin + HTMX 服务端渲染结构，不引入流式通道。服务端继续提供单节点检测 API 与完整弹窗 HTML；前端在 `htmx.js` 中编排 bulk 检测顺序并直接更新当前弹窗行状态。后端 bulk API 同步收口为串行，避免语义漂移。

**Tech Stack:** Go 1.25, Gin, HTMX, 原生 JavaScript, CSS

---

### Task 1: 为订阅节点串行批量检测补失败测试

**Files:**
- Modify: `api/src/server/ui_htmx_test.go`
- Modify: `api/src/server/ui_assets_test.go`
- Modify: `api/src/server/health_check_concurrency_test.go`
- Modify: `tests/go/auth/ui_htmx_smoke_test.go`

**Step 1: Write the failing test**

- 断言节点弹窗：
  - “检测全部”按钮带前端串行检测数据属性。
  - 行内健康状态单元格与节点名存在稳定数据属性。
  - 行内检测按钮存在可供 bulk 调度复用的节点名与检测 URL 标记。
- 断言前端脚本：
  - 存在订阅节点 bulk 串行检测函数。
  - 使用单节点 `/api/subscriptions/:id/proxies/check` 请求。
  - 检测过程中会切换当前按钮 loading 态并更新健康状态单元格。
- 断言 bulk 并发限制：
  - 无论配置值多大，订阅节点 bulk 并发上限都为 `1`。

**Step 2: Run test to verify it fails**

Run: `cd api && go test ./src/server -run 'TestUIHTMXSubscriptionProxiesModalUsesQuietRefreshAndPreserveScroll|TestUIAssetsProvideSubscriptionBulkCheckProgress|TestSubscriptionProxyCheckConcurrencyLimit' -count=1`

Expected: FAIL，提示当前节点弹窗缺少前端串行检测钩子或 bulk 并发仍非 1

**Step 3: Write minimal implementation**

- 先只补 DOM 数据标记与 JS 函数骨架，让失败点聚焦到行为缺口。

**Step 4: Run test to verify it passes**

Run: `cd api && go test ./src/server -run 'TestUIHTMXSubscriptionProxiesModalUsesQuietRefreshAndPreserveScroll|TestUIAssetsProvideSubscriptionBulkCheckProgress|TestSubscriptionProxyCheckConcurrencyLimit' -count=1`

Expected: PASS

### Task 2: 实现前端串行逐节点检测

**Files:**
- Modify: `api/src/web/public/htmx.js`
- Modify: `api/src/web/public/style.css`
- Modify: `api/src/server/ui_htmx.go`

**Step 1: Write minimal implementation**

- 在节点弹窗渲染里补充：
  - bulk 按钮的前端触发属性
  - 行级节点名、状态单元格、单节点请求 URL
- 在 `htmx.js` 中实现：
  - bulk 检测入口函数
  - 当前节点按钮 loading 切换
  - 单节点结果解析与状态单元格更新
  - 异常 toast
  - 弹窗关闭后的中止逻辑
- 复用现有按钮 loading 样式，必要时补轻量禁用态。

**Step 2: Run targeted verification**

Run: `cd api && go test ./src/server -run 'TestUIHTMXSubscriptionProxiesModalUsesQuietRefreshAndPreserveScroll|TestUIAssetsProvideSubscriptionBulkCheckProgress' -count=1`

Expected: PASS

### Task 3: 收口后端订阅节点 bulk 检测语义

**Files:**
- Modify: `api/src/server/app.go`
- Modify: `api/src/server/health_check_concurrency_test.go`

**Step 1: Write minimal implementation**

- 将 `subscriptionProxyCheckConcurrencyLimit(...)` 固定为串行 `1`。
- 保持总数约束与最小值保护。

**Step 2: Run targeted verification**

Run: `cd api && go test ./src/server -run 'TestSubscriptionProxyCheckConcurrencyLimit' -count=1`

Expected: PASS

### Task 4: 完成回归与浏览器验收

**Files:**
- Modify: `tests/go/auth/ui_htmx_smoke_test.go`

**Step 1: Run full server verification**

Run: `cd api && go test ./src/server -count=1`

Expected: PASS

**Step 2: Restart docker compose environment**

Run: `./scripts/dev.sh restart`

Expected: 容器成功重建并启动

**Step 3: Run UI smoke verification**

Run: `cd tests/go && go test ./auth -run 'TestUI_HTMX_EntryAndLoginFlow|TestUI_HTMX_SubscriptionLoadingMarkers|TestUI_HTMX_SubscriptionProxiesRenderInModal' -count=1`

Expected: PASS

**Step 4: Run real-browser verification**

- 登录管理页
- 打开“订阅 -> 节点”弹窗
- 点击“检测全部”
- 确认当前节点按钮逐个显示“检测中...”
- 确认状态逐行更新且滚动位置保持
