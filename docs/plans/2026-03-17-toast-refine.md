# Toast Refine Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 将顶部提示调整为轻量悬浮样式，并优化出现/消失的过渡动画。

**Architecture:** 保持现有 Go + Gin + HTMX 的消息输出方式，只在 `ui_htmx.go` 微调 toast 标记，在 `style.css` 重做 toast 视觉和动画，在 `htmx.js` 微调 toast 开合时序。实现采用最小改动，不影响既有页面结构和 HTMX 行为。

**Tech Stack:** Go 1.25, Gin, HTMX, 原生 JavaScript, CSS

---

### Task 1: 写 toast 样式与动画的失败测试

**Files:**
- Modify: `api/src/server/ui_assets_test.go`
- Test: `api/src/server/ui_assets_test.go`

**Step 1: Write the failing test**

- 增加断言，要求样式中存在：
  - 轻量悬浮容器宽度和间距定义
  - toast 左侧强调条或同等状态装饰
  - 优化后的 `toast.is-open` / `toast.is-closing` 动画状态

**Step 2: Run test to verify it fails**

Run: `cd api && go test ./src/server -run 'TestUIAssetsProvideToastRootAndModalTransitionStyles|TestUIAssetsProvideLightweightToastPresentation' -count=1`
Expected: FAIL，提示缺少新的 toast 结构或动画样式

**Step 3: Write minimal implementation**

- 先只改测试所需的结构类名和关键样式片段。

**Step 4: Run test to verify it passes**

Run: `cd api && go test ./src/server -run 'TestUIAssetsProvideToastRootAndModalTransitionStyles|TestUIAssetsProvideLightweightToastPresentation' -count=1`
Expected: PASS

### Task 2: 实现轻量悬浮 toast 样式

**Files:**
- Modify: `api/src/server/ui_htmx.go`
- Modify: `api/src/web/public/style.css`

**Step 1: Write minimal implementation**

- 为 toast 增加更轻的内容层次和状态装饰结构。
- 重写 toast 宽度、背景、边框、阴影、关闭按钮弱化样式。

**Step 2: Run test to verify it passes**

Run: `cd api && go test ./src/server -run 'TestUIAssetsProvideToastRootAndModalTransitionStyles|TestUIAssetsProvideLightweightToastPresentation' -count=1`
Expected: PASS

### Task 3: 优化 toast 开合动画

**Files:**
- Modify: `api/src/web/public/htmx.js`
- Modify: `api/src/web/public/style.css`

**Step 1: Write minimal implementation**

- 调整 toast 初始位移、缩放和透明度。
- 使用更柔和的过渡曲线与更干净的退出节奏。

**Step 2: Run targeted verification**

Run: `cd api && go test ./src/server -run 'TestUIAssetsReplaceNativeDialogsWithToastAndCustomConfirm|TestUIAssetsProvideToastRootAndModalTransitionStyles|TestUIAssetsProvideLightweightToastPresentation' -count=1`
Expected: PASS

### Task 4: 完成回归与容器验证

**Files:**
- Modify: `api/src/server/ui_htmx.go`
- Modify: `api/src/web/public/htmx.js`
- Modify: `api/src/web/public/style.css`
- Modify: `api/src/server/ui_assets_test.go`

**Step 1: Run full server-side verification**

Run: `cd api && go test ./src/server -count=1`
Expected: PASS

**Step 2: Restart docker compose environment**

Run: `./scripts/dev.sh restart`
Expected: 容器成功重建并启动

**Step 3: Run auth smoke verification**

Run: `cd tests/go && go test ./auth -run 'TestUI_HTMX_EntryAndLoginFlow|TestUI_HTMX_SubscriptionLoadingMarkers' -count=1`
Expected: PASS
