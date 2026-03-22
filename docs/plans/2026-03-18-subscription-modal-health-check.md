# 订阅弹窗与检测体验优化 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 统一订阅相关弹窗体验，修复节点弹窗检测后的提示和滚动问题，并收紧订阅节点批量检测并发。

**Architecture:** 保持现有 Go + Gin + HTMX 服务端渲染结构，在 `ui_htmx.go` 中统一订阅编辑与节点弹窗输出，在 `htmx.js` 中扩展滚动保留到 `#ui-extra`，在 `style.css` 中为节点表格操作列提供固定宽度样式，在 `app.go` 中单独限制订阅节点批量检测并发。实现以最小改动为主，不改 API 契约。

**Tech Stack:** Go 1.25, Gin, HTMX, 原生 JavaScript, CSS

---

### Task 1: 为订阅编辑弹窗与节点弹窗交互写失败测试

**Files:**
- Modify: `api/src/server/ui_htmx_test.go`
- Modify: `api/src/server/ui_assets_test.go`
- Modify: `tests/go/auth/ui_htmx_smoke_test.go`

**Step 1: Write the failing test**

- 为订阅编辑入口新增断言：
  - `/ui/tab/subscriptions/edit/:id` 返回 `class="modal"`
  - 包含 `proxyPoolCloseModal`
  - 不再输出页内普通面板结构作为顶层容器
- 为节点弹窗新增断言：
  - 检测按钮带 `data-preserve-scroll`
  - 成功检测返回不包含“节点检测完成”/“全部检测完成”
  - 操作列表头和单元格包含固定宽度 class
- 为前端脚本新增断言：
  - `onAfterSwap` 或同等逻辑支持 `#ui-extra` 滚动恢复

**Step 2: Run test to verify it fails**

Run: `cd api && go test ./src/server -run 'TestUIHTMXSubscriptionsEditUsesModal|TestUIHTMXSubscriptionProxiesModalUsesQuietRefreshAndPreserveScroll|TestUIAssetsPreserveScrollForUIExtra' -count=1`
Expected: FAIL，提示仍为页内编辑面板、节点弹窗缺少滚动保留或成功提示未静默

**Step 3: Write minimal implementation**

- 先补最小结构和属性，让测试能精准指向 UI 形态与交互约束。

**Step 4: Run test to verify it passes**

Run: `cd api && go test ./src/server -run 'TestUIHTMXSubscriptionsEditUsesModal|TestUIHTMXSubscriptionProxiesModalUsesQuietRefreshAndPreserveScroll|TestUIAssetsPreserveScrollForUIExtra' -count=1`
Expected: PASS

### Task 2: 实现订阅编辑 modal 与节点弹窗体验优化

**Files:**
- Modify: `api/src/server/ui_htmx.go`
- Modify: `api/src/web/public/style.css`
- Modify: `api/src/web/public/htmx.js`
- Test: `api/src/server/ui_htmx_test.go`

**Step 1: Write minimal implementation**

- 将 `renderSubscriptionEditPanel(...)` 包装成 `renderUIModal(...)`。
- 给节点弹窗检测按钮补 `data-preserve-scroll`。
- 单节点检测和检测全部成功后静默刷新弹窗，不再传成功 flash。
- 为节点表格操作列增加固定列 class 和按钮容器 class。
- 扩展 `htmx.js` 的滚动恢复逻辑，使 `#ui-extra` 刷新也能恢复原位置。

**Step 2: Run targeted verification**

Run: `cd api && go test ./src/server -run 'TestUIHTMXSubscriptionsEditUsesModal|TestUIHTMXSubscriptionProxiesModalUsesQuietRefreshAndPreserveScroll|TestUIAssetsPreserveScrollForUIExtra' -count=1`
Expected: PASS

### Task 3: 收紧订阅节点批量检测并发

**Files:**
- Modify: `api/src/server/app.go`
- Modify: `api/src/server/health_check_concurrency_test.go`

**Step 1: Write the failing test**

- 新增订阅节点批量检测并发上限函数测试：
  - 默认值返回 1
  - 显式配置 1 返回 1
  - 显式配置大于 1 时，批量检测最多只取 2
  - 总数更小时仍受总数限制

**Step 2: Run test to verify it fails**

Run: `cd api && go test ./src/server -run 'TestSubscriptionProxyCheckConcurrencyLimit' -count=1`
Expected: FAIL，提示尚未区分订阅节点批量检测并发策略

**Step 3: Write minimal implementation**

- 新增订阅节点批量检测专用并发上限函数。
- 在 `/api/subscriptions/:id/proxies/check` 的 `runWithConcurrency(...)` 中改用该函数。

**Step 4: Run test to verify it passes**

Run: `cd api && go test ./src/server -run 'TestSubscriptionProxyCheckConcurrencyLimit' -count=1`
Expected: PASS

### Task 4: 完成回归与容器验证

**Files:**
- Modify: `api/src/server/app.go`
- Modify: `api/src/server/ui_htmx.go`
- Modify: `api/src/web/public/htmx.js`
- Modify: `api/src/web/public/style.css`
- Modify: `api/src/server/ui_htmx_test.go`
- Modify: `api/src/server/ui_assets_test.go`
- Modify: `tests/go/auth/ui_htmx_smoke_test.go`

**Step 1: Run full server verification**

Run: `cd api && go test ./src/server -count=1`
Expected: PASS

**Step 2: Restart docker compose environment**

Run: `./scripts/dev.sh restart`
Expected: 容器成功重建并启动

**Step 3: Run UI smoke verification**

Run: `cd tests/go && go test ./auth -run 'TestUI_HTMX_EntryAndLoginFlow|TestUI_HTMX_SubscriptionLoadingMarkers' -count=1`
Expected: PASS
