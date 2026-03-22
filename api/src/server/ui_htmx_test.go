package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTestUIApp(t *testing.T) *App {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dataDir := t.TempDir()
	storage := NewStorage(dataDir)
	state, err := storage.LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	app := &App{
		dataDir:      dataDir,
		host:         "127.0.0.1",
		port:         0,
		adminToken:   "test-admin-token",
		openapiToken: "test-openapi-token",
		storage:      storage,
		mihomo:       NewMihomoManager(dataDir),
		installer:    NewMihomoInstaller(dataDir, storage, defaultMihomoRepo),
		state:        state,
	}

	r := gin.New()
	r.Use(gin.Recovery())
	app.registerRoutes(r)

	t.Cleanup(func() {
		app.shutdown()
	})
	return app
}

func authedRequest(t *testing.T, method, path string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("authorization", "Bearer test-admin-token")
	return req
}

func TestUIHTMXRootIsTheOnlyPublicEntry(t *testing.T) {
	app := newTestUIApp(t)

	rec := httptest.NewRecorder()
	app.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("GET /ui 应重定向到根路径，实际状态码=%d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("GET /ui 重定向地址异常: %q", loc)
	}

	rec = httptest.NewRecorder()
	app.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/page?tab=settings", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("普通 GET /ui/page 应重定向到根路径，实际状态码=%d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/?tab=settings" {
		t.Fatalf("GET /ui/page 重定向地址异常: %q", loc)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/page?tab=settings", nil)
	req.Header.Set("HX-Request", "true")
	app.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTMX GET /ui/page 状态码异常: %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `name="token"`) {
		t.Fatalf("HTMX GET /ui/page 应继续返回局部 UI 内容: %s", rec.Body.String())
	}
}

func TestUIHTMXRootVersionsStaticAssetsAndDisablesCaching(t *testing.T) {
	app := newTestUIApp(t)

	rec := httptest.NewRecorder()
	app.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / 状态码异常: %d body=%s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, `/style.css?v=`) {
		t.Fatalf("根页面样式资源缺少版本参数，浏览器可能继续使用旧缓存: %s", body)
	}
	if !strings.Contains(body, `/vendor/htmx.min.js?v=`) {
		t.Fatalf("根页面 HTMX vendor 资源缺少版本参数，浏览器可能继续使用旧缓存: %s", body)
	}
	if !strings.Contains(body, `/htmx.js?v=`) {
		t.Fatalf("根页面自定义脚本资源缺少版本参数，浏览器可能继续使用旧缓存: %s", body)
	}
	if !strings.Contains(strings.ToLower(rec.Header().Get("Cache-Control")), "no-store") {
		t.Fatalf("根页面缺少 no-store 缓存控制头，可能继续复用旧 HTML: %q", rec.Header().Get("Cache-Control"))
	}
}

func TestUIStaticAssetsDisableCaching(t *testing.T) {
	app := newTestUIApp(t)

	for _, reqPath := range []string{"/htmx.js", "/style.css", "/vendor/htmx.min.js"} {
		rec := httptest.NewRecorder()
		app.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, reqPath, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s 状态码异常: %d body=%s", reqPath, rec.Code, rec.Body.String())
		}
		if !strings.Contains(strings.ToLower(rec.Header().Get("Cache-Control")), "no-store") {
			t.Fatalf("%s 缺少 no-store 缓存控制头，可能继续复用旧静态资源: %q", reqPath, rec.Header().Get("Cache-Control"))
		}
	}
}

func TestUIHTMXInstancesTabUsesCreateModalTrigger(t *testing.T) {
	app := newTestUIApp(t)
	st := app.getState()
	st.Subscriptions = []Subscription{
		{
			ID:   "sub-1",
			Name: "订阅一",
			Proxies: []MihomoProxy{
				{"name": "节点-A", "type": "socks5", "server": "1.1.1.1", "port": 1080},
			},
		},
	}
	if err := app.saveStateDirect(st); err != nil {
		t.Fatalf("saveStateDirect: %v", err)
	}

	rec := httptest.NewRecorder()
	app.router.ServeHTTP(rec, authedRequest(t, http.MethodGet, "/ui/tab/instances"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ui/tab/instances 状态码异常: %d body=%s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, `hx-get="/ui/tab/instances/create`) {
		t.Fatalf("实例页缺少创建弹窗入口: %s", body)
	}
	if strings.Contains(body, `name="proxyName"`) {
		t.Fatalf("实例页不应继续内联渲染旧创建表单: %s", body)
	}
	if strings.Contains(body, `name="count"`) {
		t.Fatalf("实例页不应继续渲染批量创建数量输入: %s", body)
	}
}

func TestUIHTMXInstanceCreateModalAndActionButtons(t *testing.T) {
	app := newTestUIApp(t)
	st := app.getState()
	st.Settings.ExportHost = "proxy.example.com"
	st.Settings.ProxyAuth = ProxyAuth{}
	st.Subscriptions = []Subscription{
		{
			ID:   "sub-1",
			Name: "订阅一",
			Proxies: []MihomoProxy{
				{"name": "节点-A", "type": "socks5", "server": "1.1.1.1", "port": 1080},
				{"name": "节点-B", "type": "socks5", "server": "1.1.1.2", "port": 1080},
				{"name": "节点-C", "type": "socks5", "server": "1.1.1.3", "port": 1080},
				{"name": "节点-D", "type": "socks5", "server": "1.1.1.4", "port": 1080},
				{"name": "节点-E", "type": "socks5", "server": "1.1.1.5", "port": 1080},
			},
		},
		{
			ID:   "sub-2",
			Name: "订阅二",
			Proxies: []MihomoProxy{
				{"name": "节点-X", "type": "socks5", "server": "2.2.2.2", "port": 1080},
			},
		},
	}
	st.Instances = []Instance{
		{
			ID:             "inst-1",
			Name:           "订阅一 / 节点-A",
			SubscriptionID: "sub-1",
			ProxyName:      "节点-A",
			Proxy:          MihomoProxy{"name": "节点-A", "type": "socks5", "server": "1.1.1.1", "port": 1080},
			MixedPort:      30001,
			ControllerPort: 40001,
			AutoStart:      true,
			AutoSwitch:     false,
			CreatedAt:      "2026-03-17T10:00:00Z",
			UpdatedAt:      "2026-03-17T10:00:00Z",
		},
	}
	if err := app.saveStateDirect(st); err != nil {
		t.Fatalf("saveStateDirect: %v", err)
	}

	rec := httptest.NewRecorder()
	app.router.ServeHTTP(rec, authedRequest(t, http.MethodGet, "/ui/tab/instances/create"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ui/tab/instances/create 状态码异常: %d body=%s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, `class="modal"`) {
		t.Fatalf("创建实例弹窗未使用 modal 容器: %s", body)
	}
	if !strings.Contains(body, `class="instance-proxy-grid"`) {
		t.Fatalf("创建实例弹窗缺少节点卡片网格: %s", body)
	}
	if strings.Count(body, `class="instance-proxy-card"`) < 4 {
		t.Fatalf("创建实例弹窗未按卡片渲染足够节点: %s", body)
	}
	if !strings.Contains(body, `class="ui-select"`) {
		t.Fatalf("创建实例弹窗缺少统一 ui-select 组件: %s", body)
	}
	if strings.Contains(body, `<select name="subscriptionId"`) {
		t.Fatalf("创建实例弹窗不应继续渲染原生订阅下拉: %s", body)
	}
	if strings.Contains(body, `<select name="autoSwitch"`) {
		t.Fatalf("创建实例弹窗不应继续渲染原生自动切换下拉: %s", body)
	}
	if !strings.Contains(body, `value="false" data-ui-select-option`) {
		t.Fatalf("创建实例弹窗自动切换默认值不应为开: %s", body)
	}
	if strings.Contains(body, `name="autoStart"`) {
		t.Fatalf("创建实例弹窗不应暴露自动启动开关: %s", body)
	}
	if strings.Contains(body, `name="count"`) {
		t.Fatalf("创建实例弹窗不应包含批量创建数量: %s", body)
	}

	rec = httptest.NewRecorder()
	app.router.ServeHTTP(rec, authedRequest(t, http.MethodGet, "/ui/tab/instances"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ui/tab/instances 状态码异常: %d body=%s", rec.Code, rec.Body.String())
	}

	listBody := rec.Body.String()
	if !strings.Contains(listBody, `class="instance-actions-col instance-actions-col-compact"`) {
		t.Fatalf("实例列表缺少紧凑固定操作列表头: %s", listBody)
	}
	if !strings.Contains(listBody, `class="instance-actions instance-actions-compact instance-actions-flow"`) {
		t.Fatalf("实例列表缺少自适应按钮操作列容器: %s", listBody)
	}
	if !strings.Contains(listBody, `instance-action-btn-auto`) {
		t.Fatalf("实例列表按钮缺少自适应宽度标记: %s", listBody)
	}
	if !strings.Contains(listBody, "复制 SOCKS5") {
		t.Fatalf("实例列表缺少 SOCKS5 复制按钮: %s", listBody)
	}
	if !strings.Contains(listBody, "复制 HTTP") {
		t.Fatalf("实例列表缺少 HTTP 复制按钮: %s", listBody)
	}
	if strings.Contains(listBody, `hx-confirm=`) {
		t.Fatalf("实例列表删除按钮不应继续使用原生 hx-confirm: %s", listBody)
	}
	if !strings.Contains(listBody, `hx-trigger="confirmed"`) {
		t.Fatalf("实例列表删除按钮缺少自定义确认触发器: %s", listBody)
	}
	if !strings.Contains(listBody, `data-confirm-message="确认删除该实例？"`) {
		t.Fatalf("实例列表删除按钮缺少自定义确认文案: %s", listBody)
	}
}

func TestUIHTMXInstancesTabPagination(t *testing.T) {
	app := newTestUIApp(t)
	st := app.getState()
	for i := 1; i <= 21; i++ {
		st.Instances = append(st.Instances, Instance{
			ID:             fmt.Sprintf("inst-%02d", i),
			Name:           fmt.Sprintf("实例-%02d", i),
			SubscriptionID: "sub-1",
			ProxyName:      fmt.Sprintf("节点-%02d", i),
			Proxy:          MihomoProxy{"name": fmt.Sprintf("节点-%02d", i), "type": "socks5", "server": "1.1.1.1", "port": 1080},
			MixedPort:      30000 + i,
			ControllerPort: 40000 + i,
			AutoStart:      true,
			CreatedAt:      "2026-03-17T10:00:00Z",
			UpdatedAt:      "2026-03-17T10:00:00Z",
		})
	}
	if err := app.saveStateDirect(st); err != nil {
		t.Fatalf("saveStateDirect: %v", err)
	}

	rec := httptest.NewRecorder()
	app.router.ServeHTTP(rec, authedRequest(t, http.MethodGet, "/ui/tab/instances?page=3"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ui/tab/instances?page=3 状态码异常: %d body=%s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if strings.Contains(body, "实例-11") {
		t.Fatalf("第 3 页不应继续渲染前页数据: %s", body)
	}
	if !strings.Contains(body, "实例-21") {
		t.Fatalf("第 3 页应渲染尾页实例: %s", body)
	}
	if !strings.Contains(body, "第 3 / 3 页") {
		t.Fatalf("实例页缺少分页信息: %s", body)
	}
	if !strings.Contains(body, `hx-get="/ui/tab/instances?page=2"`) {
		t.Fatalf("实例页缺少返回上一页入口: %s", body)
	}
}

func TestUIHTMXSettingsIncludesHealthCheckConcurrencyField(t *testing.T) {
	app := newTestUIApp(t)

	rec := httptest.NewRecorder()
	app.router.ServeHTTP(rec, authedRequest(t, http.MethodGet, "/ui/tab/settings"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ui/tab/settings 状态码异常: %d body=%s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, `name="healthCheckConcurrency"`) {
		t.Fatalf("设置页缺少检测并发数输入项: %s", body)
	}
	if !strings.Contains(body, `class="ui-select"`) {
		t.Fatalf("设置页缺少统一 ui-select 组件: %s", body)
	}
	if strings.Contains(body, `<select name="allowLan"`) {
		t.Fatalf("设置页不应继续渲染原生 allowLan 下拉: %s", body)
	}
	if strings.Contains(body, `<select name="logLevel"`) {
		t.Fatalf("设置页不应继续渲染原生 logLevel 下拉: %s", body)
	}
	if strings.Contains(body, `<select name="releaseChannel"`) {
		t.Fatalf("设置页不应继续渲染原生 releaseChannel 下拉: %s", body)
	}
	if strings.Contains(body, `<select name="proxyAuthEnabled"`) {
		t.Fatalf("设置页不应继续渲染原生 proxyAuthEnabled 下拉: %s", body)
	}
}

func TestUIHTMXSubscriptionsDeleteUsesCustomConfirm(t *testing.T) {
	app := newTestUIApp(t)
	st := app.getState()
	subURL := "https://example.com/sub.yaml"
	st.Subscriptions = []Subscription{
		{
			ID:   "sub-1",
			Name: "订阅一",
			URL:  &subURL,
			Proxies: []MihomoProxy{
				{"name": "节点-A", "type": "socks5", "server": "1.1.1.1", "port": 1080},
			},
		},
	}
	if err := app.saveStateDirect(st); err != nil {
		t.Fatalf("saveStateDirect: %v", err)
	}

	rec := httptest.NewRecorder()
	app.router.ServeHTTP(rec, authedRequest(t, http.MethodGet, "/ui/tab/subscriptions"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ui/tab/subscriptions 状态码异常: %d body=%s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if strings.Contains(body, `hx-confirm=`) {
		t.Fatalf("订阅列表删除按钮不应继续使用原生 hx-confirm: %s", body)
	}
	if !strings.Contains(body, `hx-trigger="confirmed"`) {
		t.Fatalf("订阅列表删除按钮缺少自定义确认触发器: %s", body)
	}
	if !strings.Contains(body, `data-confirm-message="确认删除该订阅？"`) {
		t.Fatalf("订阅列表删除按钮缺少自定义确认文案: %s", body)
	}
}

func TestUIHTMXSubscriptionsEditUsesModal(t *testing.T) {
	app := newTestUIApp(t)
	st := app.getState()
	subURL := "https://example.com/sub.yaml"
	st.Subscriptions = []Subscription{
		{
			ID:   "sub-1",
			Name: "订阅一",
			URL:  &subURL,
		},
	}
	if err := app.saveStateDirect(st); err != nil {
		t.Fatalf("saveStateDirect: %v", err)
	}

	rec := httptest.NewRecorder()
	app.router.ServeHTTP(rec, authedRequest(t, http.MethodGet, "/ui/tab/subscriptions/edit/sub-1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ui/tab/subscriptions/edit/:id 状态码异常: %d body=%s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, `class="modal"`) {
		t.Fatalf("编辑订阅未渲染为弹窗容器: %s", body)
	}
	if !strings.Contains(body, `class="panel modal-card"`) {
		t.Fatalf("编辑订阅弹窗未使用统一 modal card: %s", body)
	}
	if !strings.Contains(body, "编辑订阅") {
		t.Fatalf("编辑订阅弹窗标题缺失: %s", body)
	}
	if !strings.Contains(body, "proxyPoolCloseModal") {
		t.Fatalf("编辑订阅弹窗关闭动作缺失: %s", body)
	}
	if strings.Contains(body, `<div class="panel" style="margin-top:14px">`) {
		t.Fatalf("编辑订阅不应继续以内联面板作为顶层结构: %s", body)
	}
}

func TestUIHTMXSubscriptionProxiesModalUsesQuietRefreshAndPreserveScroll(t *testing.T) {
	app := newTestUIApp(t)
	st := app.getState()
	st.Subscriptions = []Subscription{
		{
			ID:   "sub-1",
			Name: "订阅一",
			Proxies: []MihomoProxy{
				{"name": "节点-A", "type": "socks5", "server": "1.1.1.1", "port": 1080},
				{"name": "节点-B", "type": "socks5", "server": "1.1.1.2", "port": 1080},
			},
		},
	}
	if err := app.saveStateDirect(st); err != nil {
		t.Fatalf("saveStateDirect: %v", err)
	}

	rec := httptest.NewRecorder()
	app.router.ServeHTTP(rec, authedRequest(t, http.MethodGet, "/ui/tab/subscriptions/proxies/sub-1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ui/tab/subscriptions/proxies/:id 状态码异常: %d body=%s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, `class="subscription-proxy-actions-col`) {
		t.Fatalf("节点弹窗缺少固定操作列表头 class: %s", body)
	}
	if !strings.Contains(body, `class="subscription-proxy-actions-cell`) {
		t.Fatalf("节点弹窗缺少固定操作列单元格 class: %s", body)
	}
	if strings.Count(body, `data-preserve-scroll`) < 2 {
		t.Fatalf("节点弹窗检测按钮缺少滚动保留标记: %s", body)
	}
	if !strings.Contains(body, `data-subscription-bulk-check="true"`) {
		t.Fatalf("节点弹窗缺少前端串行 bulk 检测标记: %s", body)
	}
	if strings.Contains(body, `hx-post="/ui/action/subscriptions/check-all/sub-1"`) {
		t.Fatalf("节点弹窗的 bulk 检测不应继续直接走整窗 HTMX 请求: %s", body)
	}
	if !strings.Contains(body, `data-subscription-proxy-name="节点-A"`) {
		t.Fatalf("节点弹窗缺少节点行名称标记: %s", body)
	}
	if !strings.Contains(body, `data-subscription-proxy-check-url="/api/subscriptions/sub-1/proxies/check"`) {
		t.Fatalf("节点弹窗缺少单节点 API 检测地址标记: %s", body)
	}
	if !strings.Contains(body, `data-subscription-proxy-health="节点-A"`) {
		t.Fatalf("节点弹窗缺少健康状态单元格标记: %s", body)
	}

	fakeApp := &App{adminToken: "test-admin-token"}
	r := gin.New()
	fakeApp.router = r
	r.GET("/api/subscriptions/:id/proxies", func(c *gin.Context) {
		jsonOK(c, gin.H{
			"proxies": []uiProxyItem{
				{Name: "节点-A", Type: "socks5", Server: "1.1.1.1", Port: 1080},
			},
		})
	})
	r.POST("/api/subscriptions/:id/proxies/check", func(c *gin.Context) {
		jsonOK(c, gin.H{
			"results": gin.H{
				"节点-A": gin.H{"ok": true},
			},
		})
	})
	r.POST("/ui/action/subscriptions/check/:id", fakeApp.handleUIActionSubscriptionsCheckOne)
	r.POST("/ui/action/subscriptions/check-all/:id", fakeApp.handleUIActionSubscriptionsCheckAll)

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/action/subscriptions/check/sub-1?name=节点-A", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /ui/action/subscriptions/check/:id 状态码异常: %d body=%s", rec.Code, rec.Body.String())
	}
	body = rec.Body.String()
	if strings.Contains(body, "节点检测完成") {
		t.Fatalf("单节点检测成功后不应继续弹成功提示: %s", body)
	}
	if !strings.Contains(body, `class="modal"`) {
		t.Fatalf("单节点检测成功后应继续返回节点弹窗: %s", body)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/ui/action/subscriptions/check-all/sub-1", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /ui/action/subscriptions/check-all/:id 状态码异常: %d body=%s", rec.Code, rec.Body.String())
	}
	body = rec.Body.String()
	if strings.Contains(body, "全部检测完成") {
		t.Fatalf("检测全部成功后不应继续弹成功提示: %s", body)
	}
	if !strings.Contains(body, `class="modal"`) {
		t.Fatalf("检测全部成功后应继续返回节点弹窗: %s", body)
	}
}
