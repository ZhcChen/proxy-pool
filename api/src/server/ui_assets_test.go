package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUIAssetsReplaceNativeDialogsWithToastAndCustomConfirm(t *testing.T) {
	jsPath := filepath.Join("..", "web", "public", "htmx.js")
	body, err := os.ReadFile(jsPath)
	if err != nil {
		t.Fatalf("读取 htmx.js 失败: %v", err)
	}

	text := string(body)
	if strings.Contains(text, "window.alert(") {
		t.Fatalf("前端脚本不应继续使用 window.alert")
	}
	if strings.Contains(text, "window.confirm(") {
		t.Fatalf("前端脚本不应继续使用 window.confirm")
	}
	if !strings.Contains(text, "window.proxyPoolShowToast = function") {
		t.Fatalf("前端脚本缺少统一顶部 toast 能力")
	}
	if !strings.Contains(text, "window.proxyPoolConfirmAction = function") {
		t.Fatalf("前端脚本缺少自定义确认弹窗能力")
	}
	if !strings.Contains(text, `htmx.trigger(button, "confirmed")`) {
		t.Fatalf("前端脚本缺少确认后触发 HTMX 请求的逻辑")
	}
}

func TestUIAssetsProvideToastRootAndModalTransitionStyles(t *testing.T) {
	cssPath := filepath.Join("..", "web", "public", "style.css")
	body, err := os.ReadFile(cssPath)
	if err != nil {
		t.Fatalf("读取 style.css 失败: %v", err)
	}

	text := string(body)
	if !strings.Contains(text, ".toast-root {") {
		t.Fatalf("样式文件缺少顶部 toast 容器样式")
	}
	if !strings.Contains(text, ".toast.is-open {") {
		t.Fatalf("样式文件缺少 toast 入场状态样式")
	}
	if !strings.Contains(text, "@keyframes toast-enter-soft {") {
		t.Fatalf("样式文件缺少优化后的 toast 入场关键帧")
	}
	if !strings.Contains(text, ".modal.is-open {") {
		t.Fatalf("样式文件缺少 modal 入场动画状态样式")
	}
	if !strings.Contains(text, ".modal.is-closing {") {
		t.Fatalf("样式文件缺少 modal 离场动画状态样式")
	}
	if !strings.Contains(text, ".modal.is-open .modal-card {") {
		t.Fatalf("样式文件缺少 modal 卡片过渡样式")
	}
}

func TestUIAssetsProvideLightweightToastPresentation(t *testing.T) {
	cssPath := filepath.Join("..", "web", "public", "style.css")
	body, err := os.ReadFile(cssPath)
	if err != nil {
		t.Fatalf("读取 style.css 失败: %v", err)
	}

	text := string(body)
	if !strings.Contains(text, ".toast::before {") {
		t.Fatalf("样式文件缺少 toast 状态强调条")
	}
	if !strings.Contains(text, ".toast:hover {") {
		t.Fatalf("样式文件缺少 toast 轻量悬浮交互态")
	}
	if !strings.Contains(text, ".toast-close.btn.ghost {") {
		t.Fatalf("样式文件缺少弱化关闭按钮样式")
	}
	if !strings.Contains(text, "width: min(560px, calc(100vw - 24px));") {
		t.Fatalf("样式文件缺少更轻量的 toast 容器宽度")
	}
}

func TestUIAssetsProvideCustomSelectComponent(t *testing.T) {
	jsPath := filepath.Join("..", "web", "public", "htmx.js")
	jsBody, err := os.ReadFile(jsPath)
	if err != nil {
		t.Fatalf("读取 htmx.js 失败: %v", err)
	}
	jsText := string(jsBody)
	if !strings.Contains(jsText, `data-ui-select`) {
		t.Fatalf("前端脚本缺少 ui-select 事件委托入口")
	}
	if !strings.Contains(jsText, "function closeSelectMenus") {
		t.Fatalf("前端脚本缺少统一下拉关闭逻辑")
	}
	if !strings.Contains(jsText, "form.requestSubmit()") {
		t.Fatalf("前端脚本缺少下拉切换后的自动提交能力")
	}
	if !strings.Contains(jsText, `document.addEventListener("click", (evt) => {`) || !strings.Contains(jsText, `}, true);`) {
		t.Fatalf("统一下拉点击委托应在捕获阶段注册，避免被弹窗 stopPropagation 截断")
	}

	cssPath := filepath.Join("..", "web", "public", "style.css")
	cssBody, err := os.ReadFile(cssPath)
	if err != nil {
		t.Fatalf("读取 style.css 失败: %v", err)
	}
	cssText := string(cssBody)
	if !strings.Contains(cssText, ".ui-select {") {
		t.Fatalf("样式文件缺少 ui-select 容器样式")
	}
	if !strings.Contains(cssText, ".ui-select-trigger {") {
		t.Fatalf("样式文件缺少 ui-select 触发器样式")
	}
	if !strings.Contains(cssText, ".ui-select-menu {") {
		t.Fatalf("样式文件缺少 ui-select 菜单样式")
	}
	if !strings.Contains(cssText, ".ui-select-option {") {
		t.Fatalf("样式文件缺少 ui-select 选项样式")
	}
}

func TestUIAssetsPreserveScrollForUIExtra(t *testing.T) {
	jsPath := filepath.Join("..", "web", "public", "htmx.js")
	body, err := os.ReadFile(jsPath)
	if err != nil {
		t.Fatalf("读取 htmx.js 失败: %v", err)
	}

	text := string(body)
	if !strings.Contains(text, `target.id !== "ui-tab" && target.id !== "ui-extra"`) {
		t.Fatalf("前端脚本缺少对 #ui-extra 的滚动恢复支持")
	}
}

func TestUIAssetsProvideSubscriptionBulkCheckProgress(t *testing.T) {
	jsPath := filepath.Join("..", "web", "public", "htmx.js")
	body, err := os.ReadFile(jsPath)
	if err != nil {
		t.Fatalf("读取 htmx.js 失败: %v", err)
	}

	text := string(body)
	if !strings.Contains(text, "window.proxyPoolRunSubscriptionBulkCheck = async function") {
		t.Fatalf("前端脚本缺少订阅节点串行 bulk 检测入口")
	}
	if !strings.Contains(text, `credentials: "same-origin"`) {
		t.Fatalf("订阅节点串行 bulk 检测缺少 same-origin 凭据透传")
	}
	if !strings.Contains(text, `"proxyName": proxyName`) {
		t.Fatalf("订阅节点串行 bulk 检测缺少单节点请求体")
	}
	if !strings.Contains(text, `data-subscription-proxy-health`) {
		t.Fatalf("订阅节点串行 bulk 检测缺少对健康状态单元格的更新钩子")
	}
}
