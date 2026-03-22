# Instances Create Modal Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 将实例页改为弹窗式按节点多选创建，并修正实例列表操作区抖动、检测后滚动回顶和复制链接效率问题。

**Architecture:** 保持现有 Go + Gin + HTMX 的服务端渲染结构，在 `ui_htmx.go` 中新增实例创建弹窗路由和渲染函数，在 `style.css` 中补充卡片网格与固定按钮宽度样式，在 `htmx.js` 中增加实例页滚动恢复与直接复制支持。创建请求继续走现有 `/ui/action/instances/create`，仅收敛输入与默认值。

**Tech Stack:** Go 1.25, Gin, HTMX, 原生 JavaScript, CSS

---

### Task 1: 写实例页回归测试

**Files:**
- Modify: `tests/go/auth/ui_htmx_smoke_test.go`
- Test: `tests/go/auth/ui_htmx_smoke_test.go`

**Step 1: Write the failing test**

- 为实例页新增断言，覆盖：
  - 顶部存在“创建实例”按钮入口而不是内联大表单
  - 创建弹窗路由返回 `class="modal"` 和实例创建表单
  - 节点选择区域包含 4 列卡片容器标记
  - 实例列表存在两个直接复制按钮
  - 操作列使用固定宽度或稳定布局 class

**Step 2: Run test to verify it fails**

Run: `go test ./tests/go/auth -run TestUI_HTMX_ -count=1`
Expected: FAIL，提示实例页或创建弹窗缺少新结构

**Step 3: Write minimal implementation**

- 先只改模板输出，让测试能定位到新的弹窗入口和稳定操作区 class。

**Step 4: Run test to verify it passes**

Run: `go test ./tests/go/auth -run TestUI_HTMX_ -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add tests/go/auth/ui_htmx_smoke_test.go api/src/server/ui_htmx.go
git commit -m "test: cover instances create modal ui"
```

### Task 2: 实现实例创建弹窗和节点卡片

**Files:**
- Modify: `api/src/server/ui_htmx.go`
- Modify: `api/src/web/public/style.css`

**Step 1: Write the failing test**

- 让测试明确要求：
  - 创建弹窗内支持“全部订阅”与具体订阅过滤
  - 节点按卡片渲染
  - 自动切换默认关
  - 不再渲染“自动启动”“数量”“批量创建”

**Step 2: Run test to verify it fails**

Run: `go test ./tests/go/auth -run TestUI_HTMX_ -count=1`
Expected: FAIL，提示弹窗内容仍为旧表单

**Step 3: Write minimal implementation**

- 新增实例创建弹窗路由和渲染函数。
- 复用现有 `handleUIActionInstancesCreate`，调整默认值与输入收敛。
- 增加四列卡片网格样式和选中态样式。

**Step 4: Run test to verify it passes**

Run: `go test ./tests/go/auth -run TestUI_HTMX_ -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add api/src/server/ui_htmx.go api/src/web/public/style.css tests/go/auth/ui_htmx_smoke_test.go
git commit -m "feat: add instances create modal"
```

### Task 3: 修复实例列表操作体验

**Files:**
- Modify: `api/src/server/ui_htmx.go`
- Modify: `api/src/web/public/style.css`
- Modify: `api/src/web/public/htmx.js`
- Test: `tests/go/auth/ui_htmx_smoke_test.go`

**Step 1: Write the failing test**

- 为实例列表增加断言，要求：
  - 操作列容器具备固定宽度稳定 class
  - 出现 `复制 SOCKS5` 和 `复制 HTTP`

**Step 2: Run test to verify it fails**

Run: `go test ./tests/go/auth -run TestUI_HTMX_ -count=1`
Expected: FAIL，提示实例列表仍输出旧按钮

**Step 3: Write minimal implementation**

- 为实例操作按钮增加固定宽度/最小宽度 class。
- 将复制行为改为直接复制链接。
- 在 `htmx.js` 中保存并恢复实例页滚动位置。

**Step 4: Run test to verify it passes**

Run: `go test ./tests/go/auth -run TestUI_HTMX_ -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add api/src/server/ui_htmx.go api/src/web/public/style.css api/src/web/public/htmx.js tests/go/auth/ui_htmx_smoke_test.go
git commit -m "fix: stabilize instances table actions"
```

### Task 4: 做定向验证

**Files:**
- Modify: `api/src/server/ui_htmx.go`
- Modify: `api/src/web/public/style.css`
- Modify: `api/src/web/public/htmx.js`
- Modify: `tests/go/auth/ui_htmx_smoke_test.go`

**Step 1: Run targeted tests**

Run: `go test ./tests/go/auth -run TestUI_HTMX_ -count=1`
Expected: PASS

**Step 2: Run package-level server tests if needed**

Run: `go test ./api/src/server/... ./tests/go/auth -count=1`
Expected: PASS，或明确列出与本次改动无关的失败

**Step 3: Check final diff**

Run: `git diff -- docs/plans/2026-03-17-instances-create-modal-design.md docs/plans/2026-03-17-instances-create-modal.md api/src/server/ui_htmx.go api/src/web/public/style.css api/src/web/public/htmx.js tests/go/auth/ui_htmx_smoke_test.go`
Expected: 仅包含本次实例页 UI 改动

**Step 4: Commit**

```bash
git add docs/plans/2026-03-17-instances-create-modal-design.md docs/plans/2026-03-17-instances-create-modal.md api/src/server/ui_htmx.go api/src/web/public/style.css api/src/web/public/htmx.js tests/go/auth/ui_htmx_smoke_test.go
git commit -m "feat: improve instances creation workflow"
```
