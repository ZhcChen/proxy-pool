package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestUI_HTMX_EntryAndLoginFlow(t *testing.T) {
	baseURL, adminToken, _ := startServer(t)
	client := &http.Client{Timeout: 3 * time.Second}
	redirectClient := &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	{
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("期望 GET / 返回 200，实际=%d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		text := string(body)
		if !strings.Contains(text, `id="htmx-root"`) {
			t.Fatalf("首页未返回 HTMX 根节点: %s", text)
		}
		if !strings.Contains(text, `id="ui-toast-root"`) {
			t.Fatalf("首页未返回顶部 toast 容器: %s", text)
		}
		if !strings.Contains(text, `/vendor/htmx.min.js`) || !strings.Contains(text, `/htmx.js`) {
			t.Fatalf("首页未返回 HTMX 脚本依赖: %s", text)
		}
	}

	{
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/ui", nil)
		resp, err := redirectClient.Do(req)
		if err != nil {
			t.Fatalf("GET /ui: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("期望 GET /ui 返回 302，实际=%d", resp.StatusCode)
		}
		if got := resp.Header.Get("Location"); got != "/" {
			t.Fatalf("期望 GET /ui 重定向到 /，实际=%q", got)
		}
	}

	{
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/ui/page", nil)
		resp, err := redirectClient.Do(req)
		if err != nil {
			t.Fatalf("GET /ui/page: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("期望普通 GET /ui/page 返回 302，实际=%d", resp.StatusCode)
		}
		if got := resp.Header.Get("Location"); got != "/" {
			t.Fatalf("期望 GET /ui/page 重定向到 /，实际=%q", got)
		}
	}

	{
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/ui/page", nil)
		req.Header.Set("HX-Request", "true")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("HTMX GET /ui/page: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("期望 HTMX GET /ui/page 返回 200，实际=%d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), `name="token"`) {
			t.Fatalf("HTMX 未登录页面未返回 token 登录表单: %s", string(body))
		}
	}

	var sessionCookie *http.Cookie
	{
		form := url.Values{}
		form.Set("token", adminToken)
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/ui/login", strings.NewReader(form.Encode()))
		req.Header.Set("content-type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /ui/login: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望 POST /ui/login 返回 200，实际=%d body=%s", resp.StatusCode, string(body))
		}
		for _, ck := range resp.Cookies() {
			if strings.TrimSpace(ck.Name) == "proxy-pool-token" {
				sessionCookie = ck
				break
			}
		}
		body, _ := io.ReadAll(resp.Body)
		text := string(body)
		if !strings.Contains(text, "退出登录") {
			t.Fatalf("登录后页面未返回管理壳: %s", text)
		}
	}

	if sessionCookie == nil || strings.TrimSpace(sessionCookie.Value) == "" {
		t.Fatal("登录后未写入 proxy-pool-token cookie")
	}

	{
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/ui/tab/instances", nil)
		req.AddCookie(sessionCookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /ui/tab/instances: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望带 cookie 访问 /ui/tab/instances 返回 200，实际=%d body=%s", resp.StatusCode, string(body))
		}
		body, _ := io.ReadAll(resp.Body)
		text := string(body)
		if !strings.Contains(text, "创建实例") {
			t.Fatalf("实例页关键文案缺失: %s", text)
		}
		if !strings.Contains(text, `hx-get="/ui/tab/instances/create`) {
			t.Fatalf("实例页缺少创建弹窗入口: %s", text)
		}
		if !strings.Contains(text, `hx-post="/ui/action/instances/check-all`) {
			t.Fatalf("实例页缺少检测全部实例按钮: %s", text)
		}
		if !strings.Contains(text, "检测中...") {
			t.Fatalf("实例页检测按钮缺少加载文案: %s", text)
		}
		if !strings.Contains(text, `data-loading-button`) {
			t.Fatalf("实例页检测按钮缺少加载状态标记: %s", text)
		}
		if strings.Contains(text, `name="count"`) {
			t.Fatalf("实例页不应再渲染批量创建数量输入: %s", text)
		}
		if strings.Contains(text, `name="proxyName"`) {
			t.Fatalf("实例页不应再内联渲染旧创建表单: %s", text)
		}
	}

	{
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/ui/tab/instances/create", nil)
		req.AddCookie(sessionCookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /ui/tab/instances/create: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望带 cookie 访问 /ui/tab/instances/create 返回 200，实际=%d body=%s", resp.StatusCode, string(body))
		}
		body, _ := io.ReadAll(resp.Body)
		text := string(body)
		if !strings.Contains(text, `class="modal"`) {
			t.Fatalf("创建实例弹窗未渲染 modal 容器: %s", text)
		}
		if !strings.Contains(text, `class="instance-proxy-grid"`) {
			t.Fatalf("创建实例弹窗缺少节点卡片网格: %s", text)
		}
		if !strings.Contains(text, `class="ui-select"`) {
			t.Fatalf("创建实例弹窗缺少统一 ui-select 组件: %s", text)
		}
		if strings.Contains(text, `<select name="subscriptionId"`) {
			t.Fatalf("创建实例弹窗不应继续使用原生订阅下拉: %s", text)
		}
		if strings.Contains(text, `<select name="autoSwitch"`) {
			t.Fatalf("创建实例弹窗不应继续使用原生自动切换下拉: %s", text)
		}
		if strings.Contains(text, `name="autoStart"`) {
			t.Fatalf("创建实例弹窗不应包含自动启动: %s", text)
		}
	}

	{
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/ui/tab/settings", nil)
		req.AddCookie(sessionCookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /ui/tab/settings: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望带 cookie 访问 /ui/tab/settings 返回 200，实际=%d body=%s", resp.StatusCode, string(body))
		}
		body, _ := io.ReadAll(resp.Body)
		text := string(body)
		if !strings.Contains(text, `class="ui-select"`) {
			t.Fatalf("设置页缺少统一 ui-select 组件: %s", text)
		}
		if strings.Contains(text, `<select name="allowLan"`) || strings.Contains(text, `<select name="logLevel"`) {
			t.Fatalf("设置页不应继续使用原生下拉: %s", text)
		}
	}
}

func TestUI_HTMX_TabNeedAuth(t *testing.T) {
	baseURL, _, _ := startServer(t)
	client := &http.Client{Timeout: 3 * time.Second}

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/ui/tab/instances", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /ui/tab/instances: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("期望未授权访问 /ui/tab/instances 返回 401，实际=%d body=%s", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "未登录") {
		t.Fatalf("未授权页面文案不符合预期: %s", string(body))
	}
}

func TestUI_HTMX_SubscriptionLoadingMarkers(t *testing.T) {
	baseURL, adminToken, _ := startServer(t)
	apiToken := loginAndGetToken(t, baseURL, adminToken)
	client := &http.Client{Timeout: 3 * time.Second}

	var createdID string
	{
		payload, _ := json.Marshal(map[string]any{
			"name": "ui-loading-marker-sub",
			"url":  "http://127.0.0.1:9/sub?token=demo",
		})
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/subscriptions", bytes.NewReader(payload))
		req.Header.Set("content-type", "application/json")
		req.Header.Set("authorization", "Bearer "+apiToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/subscriptions: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望创建订阅 200，实际=%d body=%s", resp.StatusCode, string(b))
		}
		var out struct {
			OK           bool `json:"ok"`
			Subscription struct {
				ID string `json:"id"`
			} `json:"subscription"`
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode create subscription response: %v", err)
		}
		if !out.OK || strings.TrimSpace(out.Subscription.ID) == "" {
			t.Fatalf("创建订阅返回异常: ok=%v id=%q error=%q", out.OK, out.Subscription.ID, out.Error)
		}
		createdID = strings.TrimSpace(out.Subscription.ID)
	}
	defer func() {
		if createdID == "" {
			return
		}
		req, _ := http.NewRequest(http.MethodDelete, baseURL+"/api/subscriptions/"+createdID, nil)
		req.Header.Set("authorization", "Bearer "+apiToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("清理订阅失败: %v", err)
			return
		}
		_ = resp.Body.Close()
	}()

	var sessionCookie *http.Cookie
	{
		form := url.Values{}
		form.Set("token", adminToken)
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/ui/login", strings.NewReader(form.Encode()))
		req.Header.Set("content-type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /ui/login: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望 POST /ui/login 返回 200，实际=%d body=%s", resp.StatusCode, string(body))
		}
		for _, ck := range resp.Cookies() {
			if strings.TrimSpace(ck.Name) == "proxy-pool-token" {
				sessionCookie = ck
				break
			}
		}
	}
	if sessionCookie == nil || strings.TrimSpace(sessionCookie.Value) == "" {
		t.Fatal("登录后未写入 proxy-pool-token cookie")
	}

	{
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/ui/tab/subscriptions", nil)
		req.AddCookie(sessionCookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /ui/tab/subscriptions: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望带 cookie 访问 /ui/tab/subscriptions 返回 200，实际=%d body=%s", resp.StatusCode, string(body))
		}
		body, _ := io.ReadAll(resp.Body)
		text := string(body)
		if !strings.Contains(text, "添加中...") {
			t.Fatalf("订阅添加按钮缺少加载文案: %s", text)
		}
		if !strings.Contains(text, "更新中...") {
			t.Fatalf("订阅更新按钮缺少加载文案: %s", text)
		}
		if !strings.Contains(text, `data-loading-submit`) || !strings.Contains(text, `data-loading-button`) {
			t.Fatalf("订阅页缺少加载状态标记: %s", text)
		}
		if strings.Contains(text, `hx-confirm=`) {
			t.Fatalf("订阅页不应继续使用原生 hx-confirm: %s", text)
		}
	}

	{
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/ui/tab/subscriptions/edit/"+createdID, nil)
		req.AddCookie(sessionCookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /ui/tab/subscriptions/edit/:id: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望带 cookie 访问 /ui/tab/subscriptions/edit/:id 返回 200，实际=%d body=%s", resp.StatusCode, string(body))
		}
		body, _ := io.ReadAll(resp.Body)
		text := string(body)
		if !strings.Contains(text, "保存中...") {
			t.Fatalf("编辑订阅按钮缺少加载文案: %s", text)
		}
		if !strings.Contains(text, `data-loading-submit`) || !strings.Contains(text, `data-loading-button`) {
			t.Fatalf("编辑订阅表单缺少加载状态标记: %s", text)
		}
	}
}

func TestUI_HTMX_SubscriptionProxiesRenderInModal(t *testing.T) {
	baseURL, adminToken, _ := startServer(t)
	apiToken := loginAndGetToken(t, baseURL, adminToken)
	client := &http.Client{Timeout: 3 * time.Second}

	var createdID string
	{
		yamlText := "proxies:\n  - name: modal-node-1\n    type: socks5\n    server: 1.1.1.1\n    port: 1080\n"
		payload, _ := json.Marshal(map[string]any{
			"name": "ui-modal-sub",
			"yaml": yamlText,
		})
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/subscriptions", bytes.NewReader(payload))
		req.Header.Set("content-type", "application/json")
		req.Header.Set("authorization", "Bearer "+apiToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/subscriptions: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望创建订阅 200，实际=%d body=%s", resp.StatusCode, string(b))
		}
		var out struct {
			OK           bool `json:"ok"`
			Subscription struct {
				ID string `json:"id"`
			} `json:"subscription"`
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode create subscription response: %v", err)
		}
		if !out.OK || strings.TrimSpace(out.Subscription.ID) == "" {
			t.Fatalf("创建订阅返回异常: ok=%v id=%q error=%q", out.OK, out.Subscription.ID, out.Error)
		}
		createdID = strings.TrimSpace(out.Subscription.ID)
	}
	defer func() {
		if createdID == "" {
			return
		}
		req, _ := http.NewRequest(http.MethodDelete, baseURL+"/api/subscriptions/"+createdID, nil)
		req.Header.Set("authorization", "Bearer "+apiToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("清理订阅失败: %v", err)
			return
		}
		_ = resp.Body.Close()
	}()

	var sessionCookie *http.Cookie
	{
		form := url.Values{}
		form.Set("token", adminToken)
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/ui/login", strings.NewReader(form.Encode()))
		req.Header.Set("content-type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /ui/login: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望 POST /ui/login 返回 200，实际=%d body=%s", resp.StatusCode, string(body))
		}
		for _, ck := range resp.Cookies() {
			if strings.TrimSpace(ck.Name) == "proxy-pool-token" {
				sessionCookie = ck
				break
			}
		}
	}
	if sessionCookie == nil || strings.TrimSpace(sessionCookie.Value) == "" {
		t.Fatal("登录后未写入 proxy-pool-token cookie")
	}

	{
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/ui/tab/subscriptions/proxies/"+createdID, nil)
		req.AddCookie(sessionCookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /ui/tab/subscriptions/proxies/:id: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望带 cookie 访问 /ui/tab/subscriptions/proxies/:id 返回 200，实际=%d body=%s", resp.StatusCode, string(body))
		}
		body, _ := io.ReadAll(resp.Body)
		text := string(body)
		if !strings.Contains(text, `class="modal"`) {
			t.Fatalf("节点列表未渲染为弹窗容器: %s", text)
		}
		if !strings.Contains(text, `class="panel modal-card modal-wide"`) {
			t.Fatalf("节点列表弹窗未使用封装卡片: %s", text)
		}
		if !strings.Contains(text, "节点列表") {
			t.Fatalf("节点列表标题缺失: %s", text)
		}
		if !strings.Contains(text, "proxyPoolCloseModal") {
			t.Fatalf("节点列表弹窗关闭动作缺失: %s", text)
		}
		if !strings.Contains(text, `hx-target="#ui-extra"`) {
			t.Fatalf("节点列表弹窗操作 target 异常: %s", text)
		}
		if !strings.Contains(text, `data-subscription-bulk-check="true"`) {
			t.Fatalf("节点列表弹窗缺少前端串行 bulk 检测标记: %s", text)
		}
		if strings.Contains(text, `hx-post="/ui/action/subscriptions/check-all/`+createdID+`"`) {
			t.Fatalf("节点列表弹窗的 bulk 检测不应继续直接走整窗 HTMX 请求: %s", text)
		}
		if !strings.Contains(text, `hx-post="/ui/action/subscriptions/check/`+createdID+`?name=`) {
			t.Fatalf("节点列表弹窗缺少单节点检测按钮: %s", text)
		}
		if !strings.Contains(text, `data-subscription-proxy-check-url="/api/subscriptions/`+createdID+`/proxies/check"`) {
			t.Fatalf("节点列表弹窗缺少单节点 API 检测地址标记: %s", text)
		}
		if !strings.Contains(text, `data-subscription-proxy-health="modal-node-1"`) {
			t.Fatalf("节点列表弹窗缺少健康状态单元格标记: %s", text)
		}
		if strings.Count(text, `data-preserve-scroll`) < 2 {
			t.Fatalf("节点列表检测按钮缺少滚动保留标记: %s", text)
		}
		if !strings.Contains(text, `class="subscription-proxy-actions-col`) {
			t.Fatalf("节点列表弹窗缺少固定操作列表头 class: %s", text)
		}
		if !strings.Contains(text, `class="subscription-proxy-actions-cell`) {
			t.Fatalf("节点列表弹窗缺少固定操作列单元格 class: %s", text)
		}
		if strings.Count(text, "检测中...") < 2 {
			t.Fatalf("节点列表检测按钮缺少加载文案: %s", text)
		}
		if strings.Count(text, `data-loading-button`) < 2 {
			t.Fatalf("节点列表检测按钮缺少加载状态标记: %s", text)
		}
	}

	{
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/ui/tab/subscriptions/edit/"+createdID, nil)
		req.AddCookie(sessionCookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /ui/tab/subscriptions/edit/:id: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望带 cookie 访问 /ui/tab/subscriptions/edit/:id 返回 200，实际=%d body=%s", resp.StatusCode, string(body))
		}
		body, _ := io.ReadAll(resp.Body)
		text := string(body)
		if !strings.Contains(text, `class="modal"`) {
			t.Fatalf("编辑订阅未渲染为弹窗容器: %s", text)
		}
		if !strings.Contains(text, `class="panel modal-card"`) {
			t.Fatalf("编辑订阅弹窗未使用封装卡片: %s", text)
		}
		if !strings.Contains(text, "编辑订阅") {
			t.Fatalf("编辑订阅标题缺失: %s", text)
		}
		if !strings.Contains(text, "proxyPoolCloseModal") {
			t.Fatalf("编辑订阅弹窗关闭动作缺失: %s", text)
		}
	}
}
