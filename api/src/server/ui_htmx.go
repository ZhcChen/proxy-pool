package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type uiInstance struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	SubscriptionID string        `json:"subscriptionId"`
	ProxyName      string        `json:"proxyName"`
	MixedPort      int           `json:"mixedPort"`
	ControllerPort int           `json:"controllerPort"`
	AutoStart      bool          `json:"autoStart"`
	AutoSwitch     bool          `json:"autoSwitch"`
	CreatedAt      string        `json:"createdAt"`
	UpdatedAt      string        `json:"updatedAt"`
	Runtime        runtimeStatus `json:"runtime"`
	Health         *HealthStatus `json:"health"`
}

type uiPoolProxy struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MixedPort int    `json:"mixedPort"`
	Proxy     string `json:"proxy"`
	Running   bool   `json:"running"`
}

type uiProxyItem struct {
	Name    string        `json:"name"`
	Type    string        `json:"type"`
	Server  string        `json:"server"`
	Port    any           `json:"port"`
	Network string        `json:"network"`
	TLS     *bool         `json:"tls"`
	UDP     *bool         `json:"udp"`
	Health  *HealthStatus `json:"health"`
}

type uiProxyChoice struct {
	SubscriptionID   string
	SubscriptionName string
	ProxyName        string
}

type uiMihomoStatusResp struct {
	OK     bool `json:"ok"`
	Status struct {
		Repo      string             `json:"repo"`
		System    MihomoSystem       `json:"system"`
		BinPath   string             `json:"binPath"`
		Installed *MihomoInstallInfo `json:"installed"`
	} `json:"status"`
	Error string `json:"error"`
}

func h(text string) string {
	return html.EscapeString(strings.TrimSpace(text))
}

func uiFmtTime(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func uiBoolText(v bool) string {
	if v {
		return "是"
	}
	return "否"
}

func uiSelected(current, value string) string {
	if current == value {
		return " selected"
	}
	return ""
}

func uiParseBool(raw string, fallback bool) bool {
	s := strings.TrimSpace(strings.ToLower(raw))
	switch s {
	case "1", "true", "on", "yes", "y":
		return true
	case "0", "false", "off", "no", "n":
		return false
	default:
		return fallback
	}
}

func uiActiveTab(raw string) string {
	s := strings.TrimSpace(strings.ToLower(raw))
	switch s {
	case "instances", "subscriptions", "settings", "pool":
		return s
	default:
		return "instances"
	}
}

func uiEncodeProxyChoice(subID, proxyName string) string {
	raw := strings.TrimSpace(subID) + "\x00" + strings.TrimSpace(proxyName)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func uiDecodeProxyChoice(token string) (string, string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", "", errors.New("空节点值")
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", "", fmt.Errorf("节点值解析失败：%w", err)
	}
	parts := strings.SplitN(string(b), "\x00", 2)
	if len(parts) != 2 {
		return "", "", errors.New("节点值格式不正确")
	}
	subID := strings.TrimSpace(parts[0])
	proxyName := strings.TrimSpace(parts[1])
	if subID == "" || proxyName == "" {
		return "", "", errors.New("节点值缺少订阅或节点名称")
	}
	return subID, proxyName, nil
}

func uiBuildProxyChoices(subs []Subscription, instances []uiInstance) []uiProxyChoice {
	used := map[string]map[string]struct{}{}
	for _, inst := range instances {
		if used[inst.SubscriptionID] == nil {
			used[inst.SubscriptionID] = map[string]struct{}{}
		}
		used[inst.SubscriptionID][inst.ProxyName] = struct{}{}
	}

	out := make([]uiProxyChoice, 0)
	for _, sub := range subs {
		seen := map[string]struct{}{}
		for _, p := range sub.Proxies {
			name := strings.TrimSpace(p.Name())
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			if _, ok := used[sub.ID][name]; ok {
				continue
			}
			out = append(out, uiProxyChoice{
				SubscriptionID:   sub.ID,
				SubscriptionName: sub.Name,
				ProxyName:        name,
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SubscriptionName != out[j].SubscriptionName {
			return strings.Compare(out[i].SubscriptionName, out[j].SubscriptionName) < 0
		}
		return strings.Compare(out[i].ProxyName, out[j].ProxyName) < 0
	})
	return out
}

func uiBuildInstanceProxyURLs(inst uiInstance, settings Settings) (socks5URL, httpURL, hostHint, authHint string) {
	host := strings.TrimSpace(settings.ExportHost)
	if host != "" {
		hostHint = "已使用 exportHost：" + host
	} else {
		host = "127.0.0.1"
		bindAddress := strings.TrimSpace(settings.BindAddress)
		if settings.AllowLan {
			if bindAddress != "" && bindAddress != "0.0.0.0" && bindAddress != "::" && bindAddress != "127.0.0.1" {
				host = bindAddress
			}
		}
		hostHint = "未设置 exportHost，回退到：" + host
	}

	userInfo := ""
	if settings.ProxyAuth.Enabled {
		userInfo = url.PathEscape(settings.ProxyAuth.Username) + ":" + url.PathEscape(settings.ProxyAuth.Password) + "@"
		authHint = "已启用认证（用户名/密码已 URL 编码）"
	} else {
		authHint = "未启用认证（链接中不含用户名密码）"
	}
	hostPart := hostWithIPv6Bracket(host)
	socks5URL = fmt.Sprintf("socks5://%s%s:%d", userInfo, hostPart, inst.MixedPort)
	httpURL = fmt.Sprintf("http://%s%s:%d", userInfo, hostPart, inst.MixedPort)
	return socks5URL, httpURL, hostHint, authHint
}

func uiErrorMessage(body []byte, status int, fallback string) string {
	if strings.TrimSpace(fallback) != "" {
		fallback = strings.TrimSpace(fallback)
	}
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err == nil && strings.TrimSpace(out.Error) != "" {
		return out.Error
	}
	if fallback != "" {
		return fallback
	}
	return fmt.Sprintf("请求失败（HTTP %d）", status)
}

func (a *App) callAdminAPI(method, reqPath string, payload any) (int, []byte, error) {
	if a.router == nil {
		return 0, nil, fmt.Errorf("router 未初始化")
	}
	var bodyBuf *bytes.Reader
	if payload == nil {
		bodyBuf = bytes.NewReader(nil)
	} else {
		b, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		bodyBuf = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, reqPath, bodyBuf)
	req.Header.Set("authorization", "Bearer "+a.adminToken)
	if payload != nil {
		req.Header.Set("content-type", "application/json")
	}
	rec := httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes(), nil
}

func (a *App) fetchInstances() ([]uiInstance, error) {
	status, body, err := a.callAdminAPI(http.MethodGet, "/api/instances", nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, errors.New(uiErrorMessage(body, status, "获取实例失败"))
	}
	var out struct {
		OK        bool         `json:"ok"`
		Instances []uiInstance `json:"instances"`
		Error     string       `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, errors.New(strings.TrimSpace(out.Error))
	}
	if out.Instances == nil {
		out.Instances = []uiInstance{}
	}
	return out.Instances, nil
}

func (a *App) fetchSubscriptions() ([]Subscription, error) {
	status, body, err := a.callAdminAPI(http.MethodGet, "/api/subscriptions", nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, errors.New(uiErrorMessage(body, status, "获取订阅失败"))
	}
	var out struct {
		OK            bool           `json:"ok"`
		Subscriptions []Subscription `json:"subscriptions"`
		Error         string         `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, errors.New(strings.TrimSpace(out.Error))
	}
	if out.Subscriptions == nil {
		out.Subscriptions = []Subscription{}
	}
	return out.Subscriptions, nil
}

func (a *App) fetchSettings() (Settings, error) {
	status, body, err := a.callAdminAPI(http.MethodGet, "/api/settings", nil)
	if err != nil {
		return Settings{}, err
	}
	if status < 200 || status >= 300 {
		return Settings{}, errors.New(uiErrorMessage(body, status, "获取设置失败"))
	}
	var out struct {
		OK       bool     `json:"ok"`
		Settings Settings `json:"settings"`
		Error    string   `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return Settings{}, err
	}
	if !out.OK {
		return Settings{}, errors.New(strings.TrimSpace(out.Error))
	}
	return out.Settings, nil
}

func (a *App) fetchPool() ([]uiPoolProxy, error) {
	status, body, err := a.callAdminAPI(http.MethodGet, "/api/pool", nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, errors.New(uiErrorMessage(body, status, "获取代理池失败"))
	}
	var out struct {
		OK      bool          `json:"ok"`
		Proxies []uiPoolProxy `json:"proxies"`
		Error   string        `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, errors.New(strings.TrimSpace(out.Error))
	}
	if out.Proxies == nil {
		out.Proxies = []uiPoolProxy{}
	}
	return out.Proxies, nil
}

func (a *App) fetchSubscriptionProxies(subID string) ([]uiProxyItem, error) {
	status, body, err := a.callAdminAPI(http.MethodGet, "/api/subscriptions/"+subID+"/proxies", nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, errors.New(uiErrorMessage(body, status, "获取节点失败"))
	}
	var out struct {
		OK      bool          `json:"ok"`
		Proxies []uiProxyItem `json:"proxies"`
		Error   string        `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, errors.New(strings.TrimSpace(out.Error))
	}
	if out.Proxies == nil {
		out.Proxies = []uiProxyItem{}
	}
	return out.Proxies, nil
}

func renderFlash(msg string, isErr bool) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}
	if isErr {
		return `<div class="badge bad" style="margin: 0 2px 12px 2px">` + h(msg) + `</div>`
	}
	return `<div class="badge ok" style="margin: 0 2px 12px 2px">` + h(msg) + `</div>`
}

func (a *App) renderUILogin(errMsg string) string {
	var b strings.Builder
	b.WriteString(`<div id="htmx-root">`)
	b.WriteString(`<div class="login-shell"><div class="panel login-card">`)
	b.WriteString(`<div class="login-brand"><div class="login-logo">proxy-pool</div><div class="login-tag">多实例代理池管理（HTMX）</div></div>`)
	if strings.TrimSpace(errMsg) != "" {
		b.WriteString(`<div class="badge bad" style="margin-bottom: 10px">` + h(errMsg) + `</div>`)
	}
	b.WriteString(`<form hx-post="/ui/login" hx-target="#htmx-root" hx-swap="outerHTML">`)
	b.WriteString(`<div class="field"><label>访问 Token</label><input name="token" type="password" placeholder="粘贴 ADMIN_TOKEN" autocomplete="current-password" required /></div>`)
	b.WriteString(`<div class="help" style="margin-bottom: 10px">请输入服务端环境变量 <code>ADMIN_TOKEN</code>。</div>`)
	b.WriteString(`<button class="btn primary login-btn" type="submit">登录</button>`)
	b.WriteString(`</form>`)
	b.WriteString(`</div></div></div>`)
	return b.String()
}

func (a *App) renderUIShell(activeTab, flash string, flashErr bool) string {
	activeTab = uiActiveTab(activeTab)
	var b strings.Builder
	b.WriteString(`<div id="htmx-root">`)
	b.WriteString(`<header class="header">`)
	b.WriteString(`<div class="header-main">`)
	b.WriteString(`<div class="title">proxy-pool</div>`)
	b.WriteString(`<div class="header-logout"><button class="btn danger sm" hx-post="/ui/logout" hx-target="#htmx-root" hx-swap="outerHTML" type="button">退出登录</button></div>`)
	b.WriteString(`</div>`)
	b.WriteString(`<nav class="nav">`)
	addTab := func(id, label string) {
		activeCls := ""
		activeAttr := ""
		if activeTab == id {
			activeCls = " active"
			activeAttr = ` aria-current="page"`
		}
		b.WriteString(`<button class="tab` + activeCls + `" data-ui-tab="` + h(id) + `"` + activeAttr + ` onclick="proxyPoolSetActiveTab(this)" hx-get="/ui/tab/` + id + `" hx-push-url="/?tab=` + h(id) + `" hx-target="#ui-tab" hx-swap="innerHTML" type="button">` + h(label) + `</button>`)
	}
	addTab("instances", "实例")
	addTab("subscriptions", "订阅")
	addTab("settings", "设置")
	addTab("pool", "代理池")
	b.WriteString(`</nav>`)
	b.WriteString(`</header>`)
	b.WriteString(`<main class="container">`)
	b.WriteString(renderFlash(flash, flashErr))
	b.WriteString(`<section id="ui-tab" hx-get="/ui/tab/` + activeTab + `" hx-trigger="load" hx-swap="innerHTML">加载中...</section>`)
	b.WriteString(`</main></div>`)
	return b.String()
}

func (a *App) handleHTMXRoot(c *gin.Context) {
	activeTab := uiActiveTab(c.Query("tab"))
	page := `<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>proxy-pool</title>
    <meta name="theme-color" content="#0a0e27" />
    <link rel="icon" href="/favicon.svg" type="image/svg+xml" />
    <link rel="icon" href="/favicon.ico" sizes="16x16 32x32" />
    <link rel="stylesheet" href="/style.css" />
  </head>
  <body>
    <div id="htmx-root" hx-get="/ui/page?tab=` + url.QueryEscape(activeTab) + `" hx-trigger="load" hx-swap="outerHTML">加载中...</div>
    <script src="/vendor/htmx.min.js"></script>
    <script src="/htmx.js"></script>
  </body>
</html>`
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page))
}

func (a *App) handleUIPage(c *gin.Context) {
	tab := uiActiveTab(c.Query("tab"))
	if !a.isAdminAuthorized(c) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderUILogin("")))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderUIShell(tab, "", false)))
}

func (a *App) handleUILogin(c *gin.Context) {
	token := strings.TrimSpace(c.PostForm("token"))
	if token == "" {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderUILogin("token 不能为空")))
		return
	}
	if !sameToken(token, a.adminToken) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderUILogin("token 无效")))
		return
	}
	c.SetCookie(authTokenKey, token, 30*24*3600, "/", "", false, true)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderUIShell("instances", "登录成功", false)))
}

func (a *App) handleUILogout(c *gin.Context) {
	c.SetCookie(authTokenKey, "", -1, "/", "", false, true)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderUILogin("已退出登录")))
}

func (a *App) uiAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if a.isAdminAuthorized(c) {
			c.Next()
			return
		}
		c.Data(http.StatusUnauthorized, "text/html; charset=utf-8", []byte(a.renderUILogin("未登录或登录已失效")))
		c.Abort()
	}
}

func (a *App) registerUIRoutes(r *gin.Engine) {
	ui := r.Group("/ui")
	ui.Use(a.uiAuthMiddleware())
	{
		ui.GET("/tab/subscriptions/proxies/:id", a.handleUITabSubscriptionProxies)
		ui.GET("/tab/subscriptions/edit/:id", a.handleUITabSubscriptionEdit)
		ui.GET("/tab/instances/logs/:id", a.handleUITabInstanceLogs)
		ui.GET("/tab/instances/copy/:id", a.handleUITabInstanceCopy)
		ui.GET("/tab/:tab", a.handleUITab)

		ui.POST("/action/subscriptions/create", a.handleUIActionSubscriptionsCreate)
		ui.POST("/action/subscriptions/update/:id", a.handleUIActionSubscriptionsUpdate)
		ui.POST("/action/subscriptions/refresh/:id", a.handleUIActionSubscriptionsRefresh)
		ui.POST("/action/subscriptions/delete/:id", a.handleUIActionSubscriptionsDelete)
		ui.POST("/action/subscriptions/check-all/:id", a.handleUIActionSubscriptionsCheckAll)
		ui.POST("/action/subscriptions/check/:id", a.handleUIActionSubscriptionsCheckOne)

		ui.POST("/action/instances/create", a.handleUIActionInstancesCreate)
		ui.POST("/action/instances/batch", a.handleUIActionInstancesBatch)
		ui.POST("/action/instances/start/:id", a.handleUIActionInstancesStart)
		ui.POST("/action/instances/stop/:id", a.handleUIActionInstancesStop)
		ui.POST("/action/instances/check/:id", a.handleUIActionInstancesCheck)
		ui.POST("/action/instances/check-all", a.handleUIActionInstancesCheckAll)
		ui.POST("/action/instances/delete/:id", a.handleUIActionInstancesDelete)
		ui.POST("/action/instances/toggle-auto-switch/:id", a.handleUIActionInstancesToggleAutoSwitch)

		ui.POST("/action/settings/save", a.handleUIActionSettingsSave)
		ui.POST("/action/settings/detect-ip", a.handleUIActionSettingsDetectIP)
		ui.POST("/action/settings/reset-proxy-auth", a.handleUIActionSettingsResetProxyAuth)
		ui.POST("/action/settings/install-mihomo", a.handleUIActionSettingsInstallMihomo)
		ui.POST("/action/settings/check-mihomo-latest", a.handleUIActionSettingsCheckMihomoLatest)
	}
}

func (a *App) handleUITab(c *gin.Context) {
	tab := uiActiveTab(c.Param("tab"))
	var htmlOut string
	switch tab {
	case "instances":
		htmlOut = a.renderTabInstances("", false)
	case "subscriptions":
		htmlOut = a.renderTabSubscriptions("", false)
	case "settings":
		htmlOut = a.renderTabSettings("", false)
	case "pool":
		htmlOut = a.renderTabPool("", false)
	default:
		htmlOut = a.renderTabInstances("", false)
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(htmlOut))
}

func (a *App) renderTabInstances(msg string, isErr bool) string {
	subs, subErr := a.fetchSubscriptions()
	instances, instErr := a.fetchInstances()
	if subErr != nil {
		return `<div class="panel"><div class="badge bad">` + h(subErr.Error()) + `</div></div>`
	}
	if instErr != nil {
		return `<div class="panel"><div class="badge bad">` + h(instErr.Error()) + `</div></div>`
	}
	choices := uiBuildProxyChoices(subs, instances)
	availability := a.availabilityFor(nil, true)
	toInt := func(v any) int {
		switch n := v.(type) {
		case int:
			return n
		case int64:
			return int(n)
		case float64:
			return int(n)
		case json.Number:
			i, _ := n.Int64()
			return int(i)
		default:
			return 0
		}
	}
	availLine := fmt.Sprintf(
		"剩余可用节点：%d（总%d / 已用%d / 未测%d / 不可用%d）",
		toInt(availability["available"]),
		toInt(availability["total"]),
		toInt(availability["used"]),
		toInt(availability["untested"]),
		toInt(availability["unhealthy"]),
	)

	var b strings.Builder
	b.WriteString(renderFlash(msg, isErr))
	b.WriteString(`<div class="panel">`)
	b.WriteString(`<div class="panel-header"><div><div class="panel-title">创建实例</div><div class="panel-subtitle">支持单个创建、节点多选创建与批量创建。默认 <code>__AUTO__</code> 自动选择可用且未占用节点。</div></div><div class="panel-actions"><button class="btn sm" hx-get="/ui/tab/instances" hx-target="#ui-tab" hx-swap="innerHTML" type="button">刷新列表</button><button class="btn sm" hx-post="/ui/action/instances/check-all" hx-target="#ui-tab" hx-swap="innerHTML" type="button">检测全部实例</button></div></div>`)
	b.WriteString(`<div class="help" style="margin-bottom:10px">` + h(availLine) + `</div>`)
	b.WriteString(`<div class="row">`)
	b.WriteString(`<form hx-post="/ui/action/instances/create" hx-target="#ui-tab" hx-swap="innerHTML" style="width:100%">`)
	b.WriteString(`<div class="row"><div><label>订阅</label><select name="subscriptionId">`)
	b.WriteString(`<option value="__ALL__">全部订阅</option>`)
	for _, s := range subs {
		b.WriteString(`<option value="` + h(s.ID) + `">` + h(s.Name) + `（` + strconv.Itoa(len(s.Proxies)) + `）</option>`)
	}
	b.WriteString(`</select></div>`)
	b.WriteString(`<div><label>节点名（单个创建）</label><input name="proxyName" value="__AUTO__" placeholder="__AUTO__ 或具体节点名" /></div>`)
	b.WriteString(`<div><label>mixed-port（可选）</label><input name="mixedPort" type="number" placeholder="留空自动分配" /></div></div>`)
	b.WriteString(`<div><label>多选节点（可选，选择后将忽略“节点名”并一次创建多个实例）</label>`)
	b.WriteString(`<select name="proxyTargets" class="proxy-multi-select" multiple`)
	if len(choices) == 0 {
		b.WriteString(` disabled`)
	}
	b.WriteString(`>`)
	if len(choices) == 0 {
		b.WriteString(`<option value="">暂无可用且未占用节点</option>`)
	} else {
		for _, ch := range choices {
			label := ch.SubscriptionName + ` · ` + ch.ProxyName
			value := uiEncodeProxyChoice(ch.SubscriptionID, ch.ProxyName)
			b.WriteString(`<option value="` + h(value) + `">` + h(label) + `</option>`)
		}
	}
	b.WriteString(`</select>`)
	b.WriteString(`<div class="help">多选创建不支持指定 mixed-port，会按配置自动分配端口。</div></div>`)
	b.WriteString(`<div class="row"><div><label>自动启动</label><select name="autoStart"><option value="true" selected>是</option><option value="false">否</option></select></div>`)
	b.WriteString(`<div><label>自动切换</label><select name="autoSwitch"><option value="true" selected>开</option><option value="false">关</option></select></div>`)
	b.WriteString(`<div style="display:flex;align-items:end"><button class="btn primary" type="submit">创建实例（单个或多选）</button></div></div>`)
	b.WriteString(`</form>`)
	b.WriteString(`</div>`)

	b.WriteString(`<div class="row" style="margin-top: 10px"><form hx-post="/ui/action/instances/batch" hx-target="#ui-tab" hx-swap="innerHTML" style="width:100%">`)
	b.WriteString(`<div class="row"><div><label>批量订阅</label><select name="subscriptionId"><option value="__ALL__">全部订阅</option>`)
	for _, s := range subs {
		b.WriteString(`<option value="` + h(s.ID) + `">` + h(s.Name) + `</option>`)
	}
	b.WriteString(`</select></div>`)
	b.WriteString(`<div><label>数量</label><input name="count" type="number" min="1" value="5" /></div>`)
	b.WriteString(`<div><label>自动启动</label><select name="autoStart"><option value="true" selected>是</option><option value="false">否</option></select></div>`)
	b.WriteString(`<div><label>自动切换</label><select name="autoSwitch"><option value="true" selected>开</option><option value="false">关</option></select></div>`)
	b.WriteString(`<div style="display:flex;align-items:end"><button class="btn" type="submit">批量创建</button></div></div></form></div>`)
	b.WriteString(`</div>`)

	b.WriteString(`<div class="panel" style="margin-top: 14px">`)
	b.WriteString(`<div class="panel-header"><div><div class="panel-title">实例列表</div><div class="panel-subtitle">支持启动、停止、检测、自动切换、复制代理链接与日志查看。</div></div></div>`)
	b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>名称</th><th>端口</th><th>状态</th><th>可用性</th><th>自动切换</th><th>创建时间</th><th>操作</th></tr></thead><tbody>`)
	if len(instances) == 0 {
		b.WriteString(`<tr><td colspan="7" class="muted">暂无实例</td></tr>`)
	}
	for _, it := range instances {
		running := `<span class="badge bad">已停止</span>`
		if it.Runtime.Running {
			running = `<span class="badge ok">运行中</span>`
		}
		health := `<span class="badge">未检测</span>`
		if it.Health != nil {
			if it.Health.OK {
				lat := "-"
				if it.Health.LatencyMs != nil {
					lat = strconv.Itoa(int(*it.Health.LatencyMs)) + "ms"
				}
				health = `<span class="badge ok">可用</span> <span class="muted">` + h(lat) + `</span>`
			} else {
				errText := "检测失败"
				if it.Health.Error != nil {
					errText = *it.Health.Error
				}
				health = `<span class="badge bad">不可用</span> <span class="muted">` + h(errText) + `</span>`
			}
		}
		autoSwitch := `<span class="badge">关</span>`
		if it.AutoSwitch {
			autoSwitch = `<span class="badge ok">开</span>`
		}
		b.WriteString(`<tr>`)
		b.WriteString(`<td>` + h(it.Name) + `<div class="muted" style="font-size:12px">proxy=` + h(it.ProxyName) + `</div></td>`)
		b.WriteString(`<td>` + strconv.Itoa(it.MixedPort) + `</td>`)
		b.WriteString(`<td>` + running + `</td>`)
		b.WriteString(`<td>` + health + `</td>`)
		b.WriteString(`<td>` + autoSwitch + `</td>`)
		b.WriteString(`<td>` + h(uiFmtTime(it.CreatedAt)) + `</td>`)
		b.WriteString(`<td><div class="btn-group">`)
		if it.Runtime.Running {
			b.WriteString(`<button class="btn danger sm" hx-post="/ui/action/instances/stop/` + h(it.ID) + `" hx-target="#ui-tab" hx-swap="innerHTML" type="button">停止</button>`)
		} else {
			b.WriteString(`<button class="btn ok sm" hx-post="/ui/action/instances/start/` + h(it.ID) + `" hx-target="#ui-tab" hx-swap="innerHTML" type="button">启动</button>`)
		}
		b.WriteString(`<button class="btn sm" hx-post="/ui/action/instances/check/` + h(it.ID) + `" hx-target="#ui-tab" hx-swap="innerHTML" type="button">检测</button>`)
		b.WriteString(`<button class="btn sm" hx-post="/ui/action/instances/toggle-auto-switch/` + h(it.ID) + `" hx-target="#ui-tab" hx-swap="innerHTML" type="button">切换自动</button>`)
		b.WriteString(`<button class="btn sm" hx-get="/ui/tab/instances/copy/` + h(it.ID) + `" hx-target="#ui-extra" hx-swap="innerHTML" type="button">复制链接</button>`)
		b.WriteString(`<button class="btn sm" hx-get="/ui/tab/instances/logs/` + h(it.ID) + `" hx-target="#ui-extra" hx-swap="innerHTML" type="button">日志</button>`)
		b.WriteString(`<button class="btn danger sm" hx-confirm="确认删除该实例？" hx-post="/ui/action/instances/delete/` + h(it.ID) + `" hx-target="#ui-tab" hx-swap="innerHTML" type="button">删除</button>`)
		b.WriteString(`</div></td></tr>`)
	}
	b.WriteString(`</tbody></table></div></div>`)
	b.WriteString(`<div id="ui-extra"></div>`)
	return b.String()
}

func (a *App) renderTabSubscriptions(msg string, isErr bool) string {
	subs, err := a.fetchSubscriptions()
	if err != nil {
		return `<div class="panel"><div class="badge bad">` + h(err.Error()) + `</div></div>`
	}
	var b strings.Builder
	b.WriteString(renderFlash(msg, isErr))
	b.WriteString(`<div class="panel">`)
	b.WriteString(`<div class="panel-header"><div><div class="panel-title">添加订阅</div><div class="panel-subtitle">支持 URL 或 YAML；URL 会自动尝试 flag 兼容参数，并在名称留空时自动识别（如 SKYLUMO）。</div></div></div>`)
	b.WriteString(`<form hx-post="/ui/action/subscriptions/create" hx-target="#ui-tab" hx-swap="innerHTML">`)
	b.WriteString(`<div class="row"><div><label>名称（可选）</label><input name="name" placeholder="留空自动识别，例如 SKYLUMO" /></div>`)
	b.WriteString(`<div><label>URL（可选）</label><input name="url" placeholder="https://..." /></div></div>`)
	b.WriteString(`<div><label>YAML（可选）</label><textarea name="yaml" placeholder="proxies:\n  - name: ..."></textarea></div>`)
	b.WriteString(`<div style="margin-top:10px"><button class="btn primary" type="submit">添加订阅</button></div>`)
	b.WriteString(`</form></div>`)

	b.WriteString(`<div class="panel" style="margin-top: 14px">`)
	b.WriteString(`<div class="panel-header"><div><div class="panel-title">订阅列表</div><div class="panel-subtitle">更新、删除、查看节点与检测。</div></div></div>`)
	b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>名称</th><th>URL</th><th>节点数</th><th>更新时间</th><th>状态</th><th>操作</th></tr></thead><tbody>`)
	if len(subs) == 0 {
		b.WriteString(`<tr><td colspan="6" class="muted">暂无订阅</td></tr>`)
	}
	for _, s := range subs {
		statusText := `<span class="badge ok">正常</span>`
		if s.LastError != nil && strings.TrimSpace(*s.LastError) != "" {
			statusText = `<span class="badge bad">` + h(*s.LastError) + `</span>`
		}
		urlText := "-"
		if s.URL != nil && strings.TrimSpace(*s.URL) != "" {
			urlText = h(*s.URL)
		}
		b.WriteString(`<tr>`)
		b.WriteString(`<td>` + h(s.Name) + `</td>`)
		b.WriteString(`<td style="max-width:360px;word-break:break-all">` + urlText + `</td>`)
		b.WriteString(`<td>` + strconv.Itoa(len(s.Proxies)) + `</td>`)
		b.WriteString(`<td>` + h(uiFmtTime(s.UpdatedAt)) + `</td>`)
		b.WriteString(`<td>` + statusText + `</td>`)
		b.WriteString(`<td><div class="btn-group">`)
		b.WriteString(`<button class="btn sm" hx-get="/ui/tab/subscriptions/proxies/` + h(s.ID) + `" hx-target="#ui-extra" hx-swap="innerHTML" type="button">节点</button>`)
		b.WriteString(`<button class="btn sm" hx-get="/ui/tab/subscriptions/edit/` + h(s.ID) + `" hx-target="#ui-extra" hx-swap="innerHTML" type="button">编辑</button>`)
		if s.URL != nil && strings.TrimSpace(*s.URL) != "" {
			b.WriteString(`<button class="btn sm" hx-post="/ui/action/subscriptions/refresh/` + h(s.ID) + `" hx-target="#ui-tab" hx-swap="innerHTML" type="button">更新订阅</button>`)
		}
		b.WriteString(`<button class="btn danger sm" hx-confirm="确认删除该订阅？" hx-post="/ui/action/subscriptions/delete/` + h(s.ID) + `" hx-target="#ui-tab" hx-swap="innerHTML" type="button">删除</button>`)
		b.WriteString(`</div></td></tr>`)
	}
	b.WriteString(`</tbody></table></div></div>`)
	b.WriteString(`<div id="ui-extra"></div>`)
	return b.String()
}

func (a *App) renderTabSettings(msg string, isErr bool) string {
	settings, err := a.fetchSettings()
	if err != nil {
		return `<div class="panel"><div class="badge bad">` + h(err.Error()) + `</div></div>`
	}
	statusCode, statusBody, statusErr := a.callAdminAPI(http.MethodGet, "/api/mihomo/status", nil)
	mihomo := uiMihomoStatusResp{}
	if statusErr == nil && statusCode >= 200 && statusCode < 300 {
		_ = json.Unmarshal(statusBody, &mihomo)
	}

	var b strings.Builder
	b.WriteString(renderFlash(msg, isErr))
	b.WriteString(`<div class="panel">`)
	b.WriteString(`<div class="panel-header"><div><div class="panel-title">设置</div><div class="panel-subtitle">保存后对新创建实例和重启实例生效。</div></div></div>`)
	b.WriteString(`<form hx-post="/ui/action/settings/save" hx-target="#ui-tab" hx-swap="innerHTML">`)
	b.WriteString(`<div class="row"><div><label>bindAddress</label><input name="bindAddress" value="` + h(settings.BindAddress) + `" /></div>`)
	b.WriteString(`<div><label>allowLan</label><select name="allowLan"><option value="false"` + uiSelected(strconv.FormatBool(settings.AllowLan), "false") + `>否</option><option value="true"` + uiSelected(strconv.FormatBool(settings.AllowLan), "true") + `>是</option></select></div>`)
	b.WriteString(`<div><label>logLevel</label><select name="logLevel">`)
	for _, lv := range []string{"silent", "error", "warning", "info", "debug"} {
		b.WriteString(`<option value="` + lv + `"` + uiSelected(settings.LogLevel, lv) + `>` + lv + `</option>`)
	}
	b.WriteString(`</select></div></div>`)

	b.WriteString(`<div class="row"><div><label>baseMixedPort</label><input name="baseMixedPort" type="number" value="` + strconv.Itoa(settings.BaseMixedPort) + `" /></div>`)
	b.WriteString(`<div><label>baseControllerPort</label><input name="baseControllerPort" type="number" value="` + strconv.Itoa(settings.BaseControllerPort) + `" /></div>`)
	b.WriteString(`<div><label>maxLogLines</label><input name="maxLogLines" type="number" value="` + strconv.Itoa(settings.MaxLogLines) + `" /></div></div>`)

	b.WriteString(`<div class="row"><div><label>healthCheckIntervalSec</label><input name="healthCheckIntervalSec" type="number" min="0" value="` + strconv.Itoa(settings.HealthCheckIntervalSec) + `" /></div>`)
	b.WriteString(`<div><label>subscriptionRefreshIntervalMin</label><input name="subscriptionRefreshIntervalMin" type="number" min="0" value="` + strconv.Itoa(settings.SubscriptionRefreshMin) + `" /></div>`)
	b.WriteString(`<div><label>healthCheckUrl</label><input name="healthCheckUrl" value="` + h(settings.HealthCheckURL) + `" /></div></div>`)
	b.WriteString(`<div class="row"><div><label>exportHost</label><input name="exportHost" value="` + h(settings.ExportHost) + `" placeholder="1.2.3.4 或 example.com" /></div></div>`)
	b.WriteString(`<div class="row"><div><label>内核更新通道</label><select name="releaseChannel"><option value="stable" selected>稳定版</option><option value="prerelease">预发布</option></select><div class="help">“检查最新版本 / 安装更新”会使用该通道。</div></div></div>`)

	b.WriteString(`<div class="row"><div><label>proxyAuth.enabled</label><select name="proxyAuthEnabled"><option value="false"` + uiSelected(strconv.FormatBool(settings.ProxyAuth.Enabled), "false") + `>否</option><option value="true"` + uiSelected(strconv.FormatBool(settings.ProxyAuth.Enabled), "true") + `>是</option></select></div>`)
	b.WriteString(`<div><label>proxyAuth.username</label><input readonly value="` + h(settings.ProxyAuth.Username) + `" /></div>`)
	b.WriteString(`<div><label>proxyAuth.password</label><input readonly value="` + h(settings.ProxyAuth.Password) + `" /></div></div>`)

	b.WriteString(`<div class="row" style="margin-top:10px">`)
	b.WriteString(`<button class="btn primary" type="submit">保存设置</button>`)
	b.WriteString(`<button class="btn" hx-post="/ui/action/settings/detect-ip" hx-target="#ui-tab" hx-swap="innerHTML" type="button">自动获取公网 IP</button>`)
	b.WriteString(`<button class="btn" hx-post="/ui/action/settings/reset-proxy-auth" hx-target="#ui-tab" hx-swap="innerHTML" type="button">重置代理认证</button>`)
	b.WriteString(`<button class="btn" hx-post="/ui/action/settings/check-mihomo-latest" hx-target="#ui-tab" hx-swap="innerHTML" type="button">检查最新版本</button>`)
	b.WriteString(`<button class="btn ok" hx-post="/ui/action/settings/install-mihomo" hx-target="#ui-tab" hx-swap="innerHTML" type="button">安装/更新 mihomo</button>`)
	b.WriteString(`</div></form></div>`)

	b.WriteString(`<div class="panel" style="margin-top:14px"><div class="panel-header"><div><div class="panel-title">内核状态</div><div class="panel-subtitle">`)
	if mihomo.OK {
		b.WriteString(`仓库：` + h(mihomo.Status.Repo) + `，系统：` + h(mihomo.Status.System.OS) + `/` + h(mihomo.Status.System.Arch))
	} else {
		b.WriteString(`读取失败（可重试）`)
	}
	b.WriteString(`</div></div></div>`)
	if mihomo.OK && mihomo.Status.Installed != nil {
		b.WriteString(`<div class="badge ok">已安装：` + h(mihomo.Status.Installed.Tag) + `（` + h(mihomo.Status.Installed.AssetName) + `）</div>`)
	} else {
		b.WriteString(`<div class="badge">尚未安装</div>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

func (a *App) renderTabPool(msg string, isErr bool) string {
	pool, err := a.fetchPool()
	if err != nil {
		return `<div class="panel"><div class="badge bad">` + h(err.Error()) + `</div></div>`
	}
	lines := make([]string, 0, len(pool))
	for _, p := range pool {
		state := "已停止"
		if p.Running {
			state = "运行中"
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s", p.Proxy, state, p.Name))
	}
	var b strings.Builder
	b.WriteString(renderFlash(msg, isErr))
	b.WriteString(`<div class="panel">`)
	b.WriteString(`<div class="panel-header"><div><div class="panel-title">代理池</div><div class="panel-subtitle">每行：proxy / 状态 / 名称（Tab 分隔）</div></div><div class="panel-actions"><button class="btn sm" type="button" onclick="proxyPoolCopyText(document.getElementById('pool-export-text').value)">复制列表</button></div></div>`)
	b.WriteString(`<textarea id="pool-export-text" readonly>` + h(strings.Join(lines, "\n")) + `</textarea>`)
	b.WriteString(`</div>`)
	b.WriteString(`<div class="panel" style="margin-top:14px"><div class="table-wrap"><table class="table"><thead><tr><th>名称</th><th>端口</th><th>地址</th><th>状态</th></tr></thead><tbody>`)
	if len(pool) == 0 {
		b.WriteString(`<tr><td colspan="4" class="muted">暂无实例</td></tr>`)
	}
	for _, p := range pool {
		state := `<span class="badge bad">已停止</span>`
		if p.Running {
			state = `<span class="badge ok">运行中</span>`
		}
		b.WriteString(`<tr><td>` + h(p.Name) + `</td><td>` + strconv.Itoa(p.MixedPort) + `</td><td>` + h(p.Proxy) + `</td><td>` + state + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div></div>`)
	return b.String()
}

func (a *App) handleUITabSubscriptionProxies(c *gin.Context) {
	subID := strings.TrimSpace(c.Param("id"))
	htmlOut := a.renderSubscriptionProxiesPanel(subID, "", false)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(htmlOut))
}

func (a *App) handleUITabSubscriptionEdit(c *gin.Context) {
	subID := strings.TrimSpace(c.Param("id"))
	htmlOut := a.renderSubscriptionEditPanel(subID, "", false)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(htmlOut))
}

func (a *App) renderSubscriptionProxiesPanel(subID, msg string, isErr bool) string {
	proxies, err := a.fetchSubscriptionProxies(subID)
	if err != nil {
		return `<div class="panel"><div class="badge bad">` + h(err.Error()) + `</div></div>`
	}
	tableID := "proxy-table-" + strings.ReplaceAll(subID, "-", "")
	filterID := "proxy-filter-" + strings.ReplaceAll(subID, "-", "")
	copyTextID := "proxy-copy-" + strings.ReplaceAll(subID, "-", "")
	proxyNames := make([]string, 0, len(proxies))
	for _, p := range proxies {
		if strings.TrimSpace(p.Name) != "" {
			proxyNames = append(proxyNames, strings.TrimSpace(p.Name))
		}
	}
	var b strings.Builder
	b.WriteString(`<div class="panel" style="margin-top:14px;width:min(70%,1400px);margin-left:auto;margin-right:auto">`)
	b.WriteString(`<div class="panel-header"><div><div class="panel-title">节点列表</div><div class="panel-subtitle">支持单个或全部检测。</div></div>`)
	b.WriteString(`<div class="panel-actions"><button class="btn sm" hx-post="/ui/action/subscriptions/check-all/` + h(subID) + `" hx-target="#ui-extra" hx-swap="innerHTML" type="button">检测全部</button><button class="btn sm" type="button" onclick="proxyPoolCopyText(document.getElementById('` + h(copyTextID) + `').value)">复制节点名称</button></div></div>`)
	b.WriteString(renderFlash(msg, isErr))
	b.WriteString(`<div class="row"><div><label>搜索节点</label><input id="` + h(filterID) + `" placeholder="输入关键字筛选节点" oninput="proxyPoolFilterTableRows('` + h(tableID) + `', this.value)" /></div></div>`)
	b.WriteString(`<textarea id="` + h(copyTextID) + `" class="hidden" readonly>` + h(strings.Join(proxyNames, "\n")) + `</textarea>`)
	b.WriteString(`<div class="table-wrap"><table class="table" id="` + h(tableID) + `"><thead><tr><th>名称</th><th>类型</th><th>地址</th><th>可用性</th><th>操作</th></tr></thead><tbody>`)
	if len(proxies) == 0 {
		b.WriteString(`<tr><td colspan="5" class="muted">暂无节点</td></tr>`)
	}
	for _, p := range proxies {
		addr := "-"
		if strings.TrimSpace(p.Server) != "" {
			portText := ""
			switch v := p.Port.(type) {
			case float64:
				portText = strconv.Itoa(int(v))
			case string:
				portText = strings.TrimSpace(v)
			case int:
				portText = strconv.Itoa(v)
			}
			if portText != "" {
				addr = p.Server + ":" + portText
			} else {
				addr = p.Server
			}
		}
		health := `<span class="badge">未检测</span>`
		if p.Health != nil {
			if p.Health.OK {
				lat := "-"
				if p.Health.LatencyMs != nil {
					lat = strconv.Itoa(int(*p.Health.LatencyMs)) + "ms"
				}
				health = `<span class="badge ok">可用</span> ` + h(lat)
			} else {
				errText := "检测失败"
				if p.Health.Error != nil {
					errText = *p.Health.Error
				}
				health = `<span class="badge bad">不可用</span> ` + h(errText)
			}
		}
		b.WriteString(`<tr data-proxy-name="` + h(strings.ToLower(strings.TrimSpace(p.Name))) + `"><td>` + h(p.Name) + `</td><td>` + h(p.Type) + `</td><td>` + h(addr) + `</td><td>` + health + `</td><td><button class="btn sm" hx-post="/ui/action/subscriptions/check/` + h(subID) + `?name=` + url.QueryEscape(p.Name) + `" hx-target="#ui-extra" hx-swap="innerHTML" type="button">检测</button></td></tr>`)
	}
	b.WriteString(`</tbody></table></div></div>`)
	return b.String()
}

func (a *App) renderSubscriptionEditPanel(subID, msg string, isErr bool) string {
	subs, err := a.fetchSubscriptions()
	if err != nil {
		return `<div class="panel"><div class="badge bad">` + h(err.Error()) + `</div></div>`
	}
	var sub *Subscription
	for i := range subs {
		if subs[i].ID == subID {
			sub = &subs[i]
			break
		}
	}
	if sub == nil {
		return `<div class="panel"><div class="badge bad">订阅不存在</div></div>`
	}
	urlVal := ""
	if sub.URL != nil {
		urlVal = *sub.URL
	}
	var b strings.Builder
	b.WriteString(`<div class="panel" style="margin-top:14px">`)
	b.WriteString(`<div class="panel-header"><div><div class="panel-title">编辑订阅</div><div class="panel-subtitle">可修改名称、URL，或用 YAML 覆盖节点列表。</div></div></div>`)
	b.WriteString(renderFlash(msg, isErr))
	b.WriteString(`<form hx-post="/ui/action/subscriptions/update/` + h(sub.ID) + `" hx-target="#ui-tab" hx-swap="innerHTML">`)
	b.WriteString(`<div class="row"><div><label>名称</label><input name="name" value="` + h(sub.Name) + `" required /></div></div>`)
	b.WriteString(`<div class="row"><div><label>URL（可选）</label><input name="url" value="` + h(urlVal) + `" placeholder="https://..." /></div></div>`)
	b.WriteString(`<div><label>YAML（可选，填写则优先）</label><textarea name="yaml" placeholder="留空则按 URL 处理，填写则覆盖节点"></textarea></div>`)
	b.WriteString(`<div class="help" style="margin-top:8px">若 URL 返回非 Clash YAML，服务会自动尝试追加 <code>flag</code> 参数进行兼容拉取。</div>`)
	b.WriteString(`<div style="margin-top:10px"><button class="btn primary" type="submit">保存更新</button></div>`)
	b.WriteString(`</form></div>`)
	return b.String()
}

func (a *App) handleUITabInstanceLogs(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	status, body, err := a.callAdminAPI(http.MethodGet, "/api/instances/"+id+"/logs", nil)
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<div class="panel"><div class="badge bad">`+h(err.Error())+`</div></div>`))
		return
	}
	if status < 200 || status >= 300 {
		msg := uiErrorMessage(body, status, "读取日志失败")
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<div class="panel"><div class="badge bad">`+h(msg)+`</div></div>`))
		return
	}
	var out struct {
		OK    bool     `json:"ok"`
		Lines []string `json:"lines"`
		Error string   `json:"error"`
	}
	_ = json.Unmarshal(body, &out)
	if !out.OK {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<div class="panel"><div class="badge bad">`+h(out.Error)+`</div></div>`))
		return
	}
	text := strings.Join(out.Lines, "\n")
	copyID := "inst-log-copy-" + strings.ReplaceAll(id, "-", "")
	htmlOut := `<div class="panel" style="margin-top:14px"><div class="panel-header"><div><div class="panel-title">实例日志</div><div class="panel-subtitle">最近日志（内存缓存）</div></div><div class="panel-actions"><button class="btn sm" type="button" onclick="proxyPoolCopyText(document.getElementById('` + h(copyID) + `').value)">复制日志</button></div></div><textarea id="` + h(copyID) + `" readonly>` + h(text) + `</textarea></div>`
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(htmlOut))
}

func (a *App) handleUITabInstanceCopy(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	instances, err := a.fetchInstances()
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<div class="panel"><div class="badge bad">`+h(err.Error())+`</div></div>`))
		return
	}
	var target *uiInstance
	for i := range instances {
		if instances[i].ID == id {
			target = &instances[i]
			break
		}
	}
	if target == nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<div class="panel"><div class="badge bad">实例不存在</div></div>`))
		return
	}
	settings, err := a.fetchSettings()
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<div class="panel"><div class="badge bad">`+h(err.Error())+`</div></div>`))
		return
	}
	socks5URL, httpURL, hostHint, authHint := uiBuildInstanceProxyURLs(*target, settings)
	socksID := "inst-socks-" + strings.ReplaceAll(id, "-", "")
	httpID := "inst-http-" + strings.ReplaceAll(id, "-", "")
	var b strings.Builder
	b.WriteString(`<div class="panel" style="margin-top:14px">`)
	b.WriteString(`<div class="panel-header"><div><div class="panel-title">复制链接</div><div class="panel-subtitle">实例：` + h(target.Name) + `（mixed-port=` + strconv.Itoa(target.MixedPort) + `）</div></div><div class="panel-actions"><button class="btn sm" type="button" onclick="proxyPoolCopyText(document.getElementById('` + h(socksID) + `').value)">复制 SOCKS5</button><button class="btn sm" type="button" onclick="proxyPoolCopyText(document.getElementById('` + h(httpID) + `').value)">复制 HTTP</button></div></div>`)
	b.WriteString(`<div class="row"><div><label>SOCKS5</label><input id="` + h(socksID) + `" readonly value="` + h(socks5URL) + `" /></div><div><label>HTTP</label><input id="` + h(httpID) + `" readonly value="` + h(httpURL) + `" /></div></div>`)
	b.WriteString(`<div class="help" style="margin-top:10px">` + h(authHint) + `<br/>` + h(hostHint) + `<br/>说明：本项目 mixed-port 同时支持 HTTP 与 SOCKS5。</div>`)
	b.WriteString(`</div>`)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(b.String()))
}

func (a *App) handleUIActionSubscriptionsCreate(c *gin.Context) {
	payload := map[string]any{
		"name": strings.TrimSpace(c.PostForm("name")),
		"url":  strings.TrimSpace(c.PostForm("url")),
		"yaml": c.PostForm("yaml"),
	}
	status, body, err := a.callAdminAPI(http.MethodPost, "/api/subscriptions", payload)
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSubscriptions(err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSubscriptions("订阅添加成功", false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSubscriptions(uiErrorMessage(body, status, "订阅添加失败"), true)))
}

func (a *App) handleUIActionSubscriptionsUpdate(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	name := strings.TrimSpace(c.PostForm("name"))
	urlStr := strings.TrimSpace(c.PostForm("url"))
	yamlText := c.PostForm("yaml")
	payload := map[string]any{
		"name": name,
		"url":  urlStr,
	}
	if strings.TrimSpace(yamlText) != "" {
		payload["yaml"] = yamlText
	}
	status, body, err := a.callAdminAPI(http.MethodPut, "/api/subscriptions/"+id, payload)
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderSubscriptionEditPanel(id, err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSubscriptions("订阅已更新", false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderSubscriptionEditPanel(id, uiErrorMessage(body, status, "更新订阅失败"), true)))
}

func (a *App) handleUIActionSubscriptionsRefresh(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	status, body, err := a.callAdminAPI(http.MethodPost, "/api/subscriptions/"+id+"/refresh", map[string]any{})
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSubscriptions(err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSubscriptions("订阅已更新", false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSubscriptions(uiErrorMessage(body, status, "更新订阅失败"), true)))
}

func (a *App) handleUIActionSubscriptionsDelete(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	status, body, err := a.callAdminAPI(http.MethodDelete, "/api/subscriptions/"+id, nil)
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSubscriptions(err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSubscriptions("订阅已删除", false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSubscriptions(uiErrorMessage(body, status, "删除订阅失败"), true)))
}

func (a *App) handleUIActionSubscriptionsCheckAll(c *gin.Context) {
	subID := strings.TrimSpace(c.Param("id"))
	payload := map[string]any{"all": true}
	status, body, err := a.callAdminAPI(http.MethodPost, "/api/subscriptions/"+subID+"/proxies/check", payload)
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderSubscriptionProxiesPanel(subID, err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderSubscriptionProxiesPanel(subID, "全部检测完成", false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderSubscriptionProxiesPanel(subID, uiErrorMessage(body, status, "检测失败"), true)))
}

func (a *App) handleUIActionSubscriptionsCheckOne(c *gin.Context) {
	subID := strings.TrimSpace(c.Param("id"))
	name := strings.TrimSpace(c.Query("name"))
	payload := map[string]any{"proxyName": name}
	status, body, err := a.callAdminAPI(http.MethodPost, "/api/subscriptions/"+subID+"/proxies/check", payload)
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderSubscriptionProxiesPanel(subID, err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderSubscriptionProxiesPanel(subID, "节点检测完成："+name, false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderSubscriptionProxiesPanel(subID, uiErrorMessage(body, status, "检测失败"), true)))
}

func (a *App) handleUIActionInstancesCreate(c *gin.Context) {
	subID := strings.TrimSpace(c.PostForm("subscriptionId"))
	if subID == "" {
		subID = "__ALL__"
	}
	autoStart := uiParseBool(c.PostForm("autoStart"), true)
	autoSwitch := uiParseBool(c.PostForm("autoSwitch"), true)
	mixedPortRaw := strings.TrimSpace(c.PostForm("mixedPort"))
	targetValues := c.PostFormArray("proxyTargets")
	targetSet := map[string]struct{}{}
	targets := make([]struct {
		SubscriptionID string
		ProxyName      string
	}, 0)
	for _, raw := range targetValues {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		subscriptionID, proxyName, err := uiDecodeProxyChoice(raw)
		if err != nil {
			c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(err.Error(), true)))
			return
		}
		key := subscriptionID + "\x00" + proxyName
		if _, ok := targetSet[key]; ok {
			continue
		}
		targetSet[key] = struct{}{}
		targets = append(targets, struct {
			SubscriptionID string
			ProxyName      string
		}{
			SubscriptionID: subscriptionID,
			ProxyName:      proxyName,
		})
	}

	if len(targets) > 0 {
		if mixedPortRaw != "" {
			c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances("多选创建不支持指定 mixed-port，请留空自动分配", true)))
			return
		}
		created := 0
		failed := 0
		firstErr := ""
		for _, t := range targets {
			payload := map[string]any{
				"subscriptionId": t.SubscriptionID,
				"proxyName":      t.ProxyName,
				"autoStart":      autoStart,
				"autoSwitch":     autoSwitch,
			}
			status, body, err := a.callAdminAPI(http.MethodPost, "/api/instances", payload)
			if err != nil {
				failed++
				if firstErr == "" {
					firstErr = err.Error()
				}
				continue
			}
			if status >= 200 && status < 300 {
				created++
				continue
			}
			failed++
			if firstErr == "" {
				firstErr = uiErrorMessage(body, status, "创建实例失败")
			}
		}
		if created == 0 {
			if firstErr == "" {
				firstErr = "多选创建失败"
			}
			c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(firstErr, true)))
			return
		}
		if failed > 0 {
			msg := fmt.Sprintf("已创建 %d 个，失败 %d 个", created, failed)
			if strings.TrimSpace(firstErr) != "" {
				msg += "（首个错误：" + firstErr + "）"
			}
			c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(msg, true)))
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(fmt.Sprintf("已创建 %d 个实例", created), false)))
		return
	}

	proxyName := strings.TrimSpace(c.PostForm("proxyName"))
	if proxyName == "" {
		proxyName = "__AUTO__"
	}
	payload := map[string]any{
		"subscriptionId": subID,
		"proxyName":      proxyName,
		"autoStart":      autoStart,
		"autoSwitch":     autoSwitch,
	}
	if mixedPortRaw != "" {
		if n, err := strconv.Atoi(mixedPortRaw); err == nil && n > 0 {
			payload["mixedPort"] = n
		}
	}
	status, body, err := a.callAdminAPI(http.MethodPost, "/api/instances", payload)
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances("实例创建成功", false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(uiErrorMessage(body, status, "创建实例失败"), true)))
}

func (a *App) handleUIActionInstancesBatch(c *gin.Context) {
	subID := strings.TrimSpace(c.PostForm("subscriptionId"))
	if subID == "" {
		subID = "__ALL__"
	}
	count := 5
	if n, err := strconv.Atoi(strings.TrimSpace(c.PostForm("count"))); err == nil && n > 0 {
		count = n
	}
	payload := map[string]any{
		"subscriptionId": subID,
		"count":          count,
		"autoStart":      uiParseBool(c.PostForm("autoStart"), true),
		"autoSwitch":     uiParseBool(c.PostForm("autoSwitch"), true),
	}
	status, body, err := a.callAdminAPI(http.MethodPost, "/api/instances/batch", payload)
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances("批量创建完成", false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(uiErrorMessage(body, status, "批量创建失败"), true)))
}

func (a *App) handleUIActionInstancesStart(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	status, body, err := a.callAdminAPI(http.MethodPost, "/api/instances/"+id+"/start", map[string]any{})
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances("实例已启动", false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(uiErrorMessage(body, status, "启动失败"), true)))
}

func (a *App) handleUIActionInstancesStop(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	status, body, err := a.callAdminAPI(http.MethodPost, "/api/instances/"+id+"/stop", map[string]any{})
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances("实例已停止", false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(uiErrorMessage(body, status, "停止失败"), true)))
}

func (a *App) handleUIActionInstancesCheck(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	status, body, err := a.callAdminAPI(http.MethodPost, "/api/instances/"+id+"/check", map[string]any{})
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances("检测完成", false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(uiErrorMessage(body, status, "检测失败"), true)))
}

func (a *App) handleUIActionInstancesCheckAll(c *gin.Context) {
	status, body, err := a.callAdminAPI(http.MethodPost, "/api/instances/check-all", map[string]any{})
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances("全部实例检测完成", false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(uiErrorMessage(body, status, "检测失败"), true)))
}

func (a *App) handleUIActionInstancesDelete(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	status, body, err := a.callAdminAPI(http.MethodDelete, "/api/instances/"+id, nil)
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances("实例已删除", false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(uiErrorMessage(body, status, "删除失败"), true)))
}

func (a *App) handleUIActionInstancesToggleAutoSwitch(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	instances, err := a.fetchInstances()
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(err.Error(), true)))
		return
	}
	var found *uiInstance
	for i := range instances {
		if instances[i].ID == id {
			found = &instances[i]
			break
		}
	}
	if found == nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances("实例不存在", true)))
		return
	}
	next := !found.AutoSwitch
	payload := map[string]any{"autoSwitch": next}
	status, body, err := a.callAdminAPI(http.MethodPut, "/api/instances/"+id, payload)
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		text := "自动切换已关闭"
		if next {
			text = "自动切换已开启"
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(text, false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabInstances(uiErrorMessage(body, status, "更新失败"), true)))
}

func (a *App) handleUIActionSettingsSave(c *gin.Context) {
	payload := map[string]any{
		"bindAddress":                    strings.TrimSpace(c.PostForm("bindAddress")),
		"allowLan":                       uiParseBool(c.PostForm("allowLan"), false),
		"logLevel":                       strings.TrimSpace(c.PostForm("logLevel")),
		"healthCheckUrl":                 strings.TrimSpace(c.PostForm("healthCheckUrl")),
		"exportHost":                     strings.TrimSpace(c.PostForm("exportHost")),
		"proxyAuth":                      map[string]any{"enabled": uiParseBool(c.PostForm("proxyAuthEnabled"), false)},
		"baseMixedPort":                  0,
		"baseControllerPort":             0,
		"maxLogLines":                    0,
		"healthCheckIntervalSec":         0,
		"subscriptionRefreshIntervalMin": 0,
	}
	if v, err := strconv.Atoi(strings.TrimSpace(c.PostForm("baseMixedPort"))); err == nil {
		payload["baseMixedPort"] = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(c.PostForm("baseControllerPort"))); err == nil {
		payload["baseControllerPort"] = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(c.PostForm("maxLogLines"))); err == nil {
		payload["maxLogLines"] = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(c.PostForm("healthCheckIntervalSec"))); err == nil {
		payload["healthCheckIntervalSec"] = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(c.PostForm("subscriptionRefreshIntervalMin"))); err == nil {
		payload["subscriptionRefreshIntervalMin"] = v
	}
	status, body, err := a.callAdminAPI(http.MethodPut, "/api/settings", payload)
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings(err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings("设置已保存", false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings(uiErrorMessage(body, status, "保存设置失败"), true)))
}

func (a *App) handleUIActionSettingsDetectIP(c *gin.Context) {
	status, body, err := a.callAdminAPI(http.MethodPost, "/api/settings/detect-public-ip", map[string]any{"force": true})
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings(err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings("公网 IP 获取成功", false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings(uiErrorMessage(body, status, "获取公网 IP 失败"), true)))
}

func (a *App) handleUIActionSettingsResetProxyAuth(c *gin.Context) {
	status, body, err := a.callAdminAPI(http.MethodPost, "/api/settings/reset-proxy-auth", map[string]any{})
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings(err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings("代理认证凭据已重置", false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings(uiErrorMessage(body, status, "重置失败"), true)))
}

func (a *App) handleUIActionSettingsInstallMihomo(c *gin.Context) {
	releaseChannel := strings.TrimSpace(strings.ToLower(c.PostForm("releaseChannel")))
	includePrerelease := releaseChannel == "prerelease"
	payload := map[string]any{
		"includePrerelease": includePrerelease,
		"force":             false,
	}
	status, body, err := a.callAdminAPI(http.MethodPost, "/api/mihomo/install", payload)
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings(err.Error(), true)))
		return
	}
	if status >= 200 && status < 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings("mihomo 安装/更新成功", false)))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings(uiErrorMessage(body, status, "mihomo 安装失败"), true)))
}

func (a *App) handleUIActionSettingsCheckMihomoLatest(c *gin.Context) {
	releaseChannel := strings.TrimSpace(strings.ToLower(c.PostForm("releaseChannel")))
	includePrerelease := releaseChannel == "prerelease"
	payload := map[string]any{
		"includePrerelease": includePrerelease,
	}
	status, body, err := a.callAdminAPI(http.MethodPost, "/api/mihomo/latest", payload)
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings(err.Error(), true)))
		return
	}
	if status < 200 || status >= 300 {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings(uiErrorMessage(body, status, "查询最新版本失败"), true)))
		return
	}
	var out struct {
		OK     bool `json:"ok"`
		Latest struct {
			Tag        string `json:"tag"`
			Prerelease bool   `json:"prerelease"`
		} `json:"latest"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings(err.Error(), true)))
		return
	}
	if !out.OK || strings.TrimSpace(out.Latest.Tag) == "" {
		msg := strings.TrimSpace(out.Error)
		if msg == "" {
			msg = "查询最新版本失败"
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings(msg, true)))
		return
	}
	msg := "最新版本：" + out.Latest.Tag
	if out.Latest.Prerelease {
		msg += "（预发布）"
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.renderTabSettings(msg, false)))
}
