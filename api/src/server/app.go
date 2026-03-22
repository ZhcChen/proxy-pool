package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"proxy-pool/api/src/web"
)

const (
	authTokenKey         = "proxy-pool-token"
	proxyHealthKeyPrefix = "proxy_health:"
	adminTokenEnvName    = "ADMIN_TOKEN"
	openapiTokenEnvName  = "OPENAPI_TOKEN"
	defaultMihomoRepo    = "MetaCubeX/mihomo"
)

var (
	errSubscriptionNotFound = errors.New("subscription not found")
	errSubscriptionNoURL    = errors.New("subscription has no url")
	staticAssetVersion      = buildStaticAssetVersion()
)

func buildStaticAssetVersion() string {
	h := sha256.New()
	for _, assetPath := range []string{
		"public/style.css",
		"public/htmx.js",
		"public/vendor/htmx.min.js",
	} {
		body, err := fs.ReadFile(web.PublicFS, assetPath)
		if err != nil {
			continue
		}
		_, _ = h.Write([]byte(assetPath))
		_, _ = h.Write(body)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if len(sum) > 12 {
		return sum[:12]
	}
	if sum == "" {
		return "dev"
	}
	return sum
}

func uiAssetURL(rawPath string) string {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return rawPath
	}
	sep := "?"
	if strings.Contains(rawPath, "?") {
		sep = "&"
	}
	return rawPath + sep + "v=" + url.QueryEscape(staticAssetVersion)
}

func setNoStore(c *gin.Context) {
	if c == nil {
		return
	}
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
}

type App struct {
	dataDir string
	host    string
	port    int

	adminToken   string
	openapiToken string

	router *gin.Engine

	storage   *Storage
	mihomo    *MihomoManager
	installer *MihomoInstaller

	mu    sync.RWMutex
	state State

	healthMu          sync.Mutex
	healthTicker      *time.Ticker
	healthStop        chan struct{}
	healthAutoRunning bool

	subRefreshMu          sync.Mutex
	subRefreshTicker      *time.Ticker
	subRefreshStop        chan struct{}
	subRefreshAutoRunning bool
}

func envOrDefault(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func atoiOrDefault(s string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func Run() error {
	repoRoot, _ := os.Getwd()
	dataDir := envOrDefault("DATA_DIR", filepath.Join(repoRoot, "data"))
	host := envOrDefault("HOST", "127.0.0.1")
	port := atoiOrDefault(os.Getenv("PORT"), 3320)
	adminToken := strings.TrimSpace(os.Getenv(adminTokenEnvName))
	if adminToken == "" {
		return fmt.Errorf("缺少环境变量 %s，请设置后再启动", adminTokenEnvName)
	}
	openapiToken := strings.TrimSpace(os.Getenv(openapiTokenEnvName))

	storage := NewStorage(dataDir)
	state, err := storage.LoadState()
	if err != nil {
		return err
	}
	mihomo := NewMihomoManager(dataDir)
	installer := NewMihomoInstaller(dataDir, storage, envOrDefault("MIHOMO_REPO", defaultMihomoRepo))

	app := &App{
		dataDir:      dataDir,
		host:         host,
		port:         port,
		adminToken:   adminToken,
		openapiToken: openapiToken,
		storage:      storage,
		mihomo:       mihomo,
		installer:    installer,
		state:        state,
	}

	if err := app.bootstrapExportHost(); err != nil {
		fmt.Printf("bootstrap export host warning: %v\n", err)
	}
	app.applyHealthSchedule()
	app.applySubscriptionRefreshSchedule()
	go app.bootstrapAutoStart()

	r := gin.New()
	r.Use(gin.Recovery())
	app.registerRoutes(r)

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", host, port),
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		fmt.Println("正在停止所有实例...")
		app.shutdown()
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	fmt.Printf("proxy-pool 已启动：http://%s:%d\n", host, port)
	fmt.Printf("登录方式：Bearer Token（环境变量 %s）\n", adminTokenEnvName)
	if openapiToken != "" {
		fmt.Printf("OpenAPI 已启用：GET /openapi/pool（Bearer %s）\n", openapiTokenEnvName)
	} else {
		fmt.Printf("OpenAPI 未启用：设置环境变量 %s 后可开放实例池列表\n", openapiTokenEnvName)
	}

	err = srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (a *App) shutdown() {
	a.healthMu.Lock()
	if a.healthTicker != nil {
		a.healthTicker.Stop()
		a.healthTicker = nil
	}
	if a.healthStop != nil {
		close(a.healthStop)
		a.healthStop = nil
	}
	a.healthMu.Unlock()

	a.subRefreshMu.Lock()
	if a.subRefreshTicker != nil {
		a.subRefreshTicker.Stop()
		a.subRefreshTicker = nil
	}
	if a.subRefreshStop != nil {
		close(a.subRefreshStop)
		a.subRefreshStop = nil
	}
	a.subRefreshMu.Unlock()

	a.mihomo.stopAll()
	_ = a.storage.Close()
}

func getBearerToken(c *gin.Context) string {
	h := strings.TrimSpace(c.GetHeader("authorization"))
	if h != "" {
		parts := strings.SplitN(h, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
	}
	if ck, err := c.Cookie(authTokenKey); err == nil {
		return strings.TrimSpace(ck)
	}
	return ""
}

func sameToken(a, b string) bool {
	ab := []byte(a)
	bb := []byte(b)
	if len(ab) != len(bb) {
		return false
	}
	return subtle.ConstantTimeCompare(ab, bb) == 1
}

func (a *App) isAdminAuthorized(c *gin.Context) bool {
	token := getBearerToken(c)
	if token == "" {
		return false
	}
	return sameToken(token, a.adminToken)
}

func (a *App) isOpenAPIAuthorized(c *gin.Context) bool {
	if a.openapiToken == "" {
		return false
	}
	token := getBearerToken(c)
	if token == "" {
		return false
	}
	return sameToken(token, a.openapiToken)
}

func jsonOK(c *gin.Context, payload gin.H) {
	payload["ok"] = true
	c.JSON(http.StatusOK, payload)
}

func badRequest(c *gin.Context, message string, details any) {
	out := gin.H{"ok": false, "error": message}
	if details != nil {
		out["details"] = details
	}
	c.JSON(http.StatusBadRequest, out)
}

func unauthorized(c *gin.Context) {
	c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "unauthorized"})
}

func notFound(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "not found"})
}

func decodeBody(c *gin.Context) map[string]any {
	var m map[string]any
	if err := c.ShouldBindJSON(&m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func cloneState(st State) State {
	b, _ := json.Marshal(st)
	var out State
	_ = json.Unmarshal(b, &out)
	if out.Subscriptions == nil {
		out.Subscriptions = []Subscription{}
	}
	if out.Instances == nil {
		out.Instances = []Instance{}
	}
	return out
}

func (a *App) getState() State {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneState(a.state)
}

func (a *App) updateState(fn func(*State) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := cloneState(a.state)
	if err := fn(&st); err != nil {
		return err
	}
	if err := a.storage.SaveState(st); err != nil {
		return err
	}
	a.state = st
	return nil
}

func (a *App) saveStateDirect(st State) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.storage.SaveState(st); err != nil {
		return err
	}
	a.state = st
	return nil
}

func (a *App) loadProxyHealth(subscriptionID string) map[string]HealthStatus {
	var out map[string]HealthStatus
	if err := a.storage.GetJSON(proxyHealthKeyPrefix+subscriptionID, &out); err != nil {
		return map[string]HealthStatus{}
	}
	if out == nil {
		out = map[string]HealthStatus{}
	}
	return out
}

func (a *App) saveProxyHealth(subscriptionID string, value map[string]HealthStatus) {
	_ = a.storage.SetJSON(proxyHealthKeyPrefix+subscriptionID, value)
}

func listHostIPs() ([]string, *string) {
	ifaces, _ := net.Interfaces()
	ips := make([]string, 0)
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ips = append(ips, ip.String())
		}
	}
	seen := map[string]struct{}{}
	uniq := make([]string, 0, len(ips))
	for _, ip := range ips {
		if _, ok := seen[ip]; ok {
			continue
		}
		seen[ip] = struct{}{}
		uniq = append(uniq, ip)
	}
	isPrivateIPv4 := func(ip string) bool {
		parts := strings.Split(ip, ".")
		if len(parts) != 4 {
			return false
		}
		n := make([]int, 4)
		for i, p := range parts {
			v, err := strconv.Atoi(p)
			if err != nil || v < 0 || v > 255 {
				return false
			}
			n[i] = v
		}
		a, b := n[0], n[1]
		if a == 10 || (a == 172 && b >= 16 && b <= 31) || (a == 192 && b == 168) || (a == 100 && b >= 64 && b <= 127) {
			return true
		}
		return false
	}
	ipv4 := make([]string, 0)
	for _, ip := range uniq {
		if strings.Count(ip, ".") == 3 {
			ipv4 = append(ipv4, ip)
		}
	}
	var best *string
	for _, ip := range ipv4 {
		if isPrivateIPv4(ip) {
			v := ip
			best = &v
			break
		}
	}
	if best == nil && len(ipv4) > 0 {
		v := ipv4[0]
		best = &v
	}
	if best == nil && len(uniq) > 0 {
		v := uniq[0]
		best = &v
	}
	return uniq, best
}

func fetchTextWithTimeout(urlStr string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func detectPublicIP() (string, error) {
	if override := normalizeIP(envOrDefault("PUBLIC_IP_OVERRIDE", "")); override != "" {
		return override, nil
	}
	timeout := 2500 * time.Millisecond
	providers := []func() (string, error){
		func() (string, error) {
			txt, err := fetchTextWithTimeout("https://api.ipify.org?format=json", timeout)
			if err != nil {
				return "", err
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(txt), &m); err != nil {
				return "", err
			}
			ip, _ := m["ip"].(string)
			ip = normalizeIP(ip)
			if ip == "" {
				return "", fmt.Errorf("invalid ip")
			}
			return ip, nil
		},
		func() (string, error) {
			ip, err := fetchTextWithTimeout("https://checkip.amazonaws.com", timeout)
			if err != nil {
				return "", err
			}
			ip = normalizeIP(ip)
			if ip == "" {
				return "", fmt.Errorf("invalid ip")
			}
			return ip, nil
		},
		func() (string, error) {
			ip, err := fetchTextWithTimeout("https://ifconfig.me/ip", timeout)
			if err != nil {
				return "", err
			}
			ip = normalizeIP(ip)
			if ip == "" {
				return "", fmt.Errorf("invalid ip")
			}
			return ip, nil
		},
		func() (string, error) {
			txt, err := fetchTextWithTimeout("https://1.1.1.1/cdn-cgi/trace", timeout)
			if err != nil {
				return "", err
			}
			for _, line := range strings.Split(txt, "\n") {
				if strings.HasPrefix(line, "ip=") {
					ip := normalizeIP(strings.TrimSpace(strings.TrimPrefix(line, "ip=")))
					if ip != "" {
						return ip, nil
					}
				}
			}
			return "", fmt.Errorf("invalid ip")
		},
	}
	lastErr := ""
	for _, p := range providers {
		ip, err := p()
		if err == nil && ip != "" {
			return ip, nil
		}
		if err != nil {
			lastErr = err.Error()
		}
	}
	if lastErr == "" {
		lastErr = "未解析到合法 IP"
	}
	return "", fmt.Errorf("获取公网 IP 失败：%s", lastErr)
}

func (a *App) bootstrapExportHost() error {
	st := a.getState()
	if strings.TrimSpace(st.Settings.ExportHost) != "" {
		return nil
	}
	if v, ok := normalizeHostInput(envOrDefault("PROXY_HOST", "")); ok && strings.TrimSpace(v) != "" && v != "127.0.0.1" && strings.ToLower(v) != "localhost" {
		st.Settings.ExportHost = v
		return a.saveStateDirect(st)
	}
	go func() {
		ip, err := detectPublicIP()
		if err != nil {
			fmt.Printf("自动获取公网 IP 失败（可在设置里手动填写导出 Host）：%v\n", err)
			return
		}
		a.mu.Lock()
		if strings.TrimSpace(a.state.Settings.ExportHost) == "" {
			a.state.Settings.ExportHost = ip
			_ = a.storage.SaveState(a.state)
			fmt.Printf("已自动获取公网 IP 并保存到设置：%s\n", ip)
		}
		a.mu.Unlock()
	}()
	return nil
}

func (a *App) getInstalledMihomoPath() (string, error) {
	path := a.installer.getBinPath()
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("mihomo 内核未安装，请先在「设置」中点击安装")
	}
	return path, nil
}

func (a *App) getSubscriptionForInstance(inst Instance) *Subscription {
	st := a.getState()
	for _, s := range st.Subscriptions {
		if s.ID == inst.SubscriptionID {
			sub := s
			return &sub
		}
	}
	return nil
}

func (a *App) getSubscriptionProxiesForInstance(inst Instance) []MihomoProxy {
	sub := a.getSubscriptionForInstance(inst)
	if sub != nil && len(sub.Proxies) > 0 {
		return cloneProxyList(sub.Proxies)
	}
	return []MihomoProxy{cloneProxy(inst.Proxy)}
}

func (a *App) checkAndSaveProxyHealth(sub Subscription, proxyName, binPath string) HealthStatus {
	st := a.getState()
	res := a.mihomo.checkSubscriptionProxyDelay(sub.ID, sub.UpdatedAt, sub.Proxies, proxyName, st.Settings, binPath)
	current := a.loadProxyHealth(sub.ID)
	current[proxyName] = res
	a.saveProxyHealth(sub.ID, current)
	return res
}

func rankHealth(h *HealthStatus) int {
	if h == nil {
		return 1
	}
	if h.OK {
		return 0
	}
	return 2
}

func latencyValue(h *HealthStatus) float64 {
	if h == nil || h.LatencyMs == nil {
		return 1e18
	}
	return *h.LatencyMs
}

func (a *App) startInstanceWithPreflight(inst Instance) error {
	sub := a.getSubscriptionForInstance(inst)
	if sub == nil {
		return fmt.Errorf("实例所属订阅不存在（可能已删除），无法启动")
	}
	binPath, err := a.getInstalledMihomoPath()
	if err != nil {
		return err
	}
	primary := a.checkAndSaveProxyHealth(*sub, inst.ProxyName, binPath)
	st := a.getState()
	if primary.OK {
		return a.mihomo.start(inst, st.Settings, binPath, sub.Proxies, "")
	}
	if !inst.AutoSwitch {
		msg := "检测失败"
		if primary.Error != nil {
			msg = *primary.Error
		}
		return fmt.Errorf("节点不可用，启动已取消：%s", msg)
	}
	health := a.loadProxyHealth(sub.ID)
	candidates := make([]string, 0)
	for _, p := range sub.Proxies {
		n := strings.TrimSpace(p.Name())
		if n == "" || n == inst.ProxyName {
			continue
		}
		candidates = append(candidates, n)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		ha := health[candidates[i]]
		hb := health[candidates[j]]
		ga := rankHealth(&ha)
		gb := rankHealth(&hb)
		if ga != gb {
			return ga < gb
		}
		la := latencyValue(&ha)
		lb := latencyValue(&hb)
		if la != lb {
			return la < lb
		}
		return strings.Compare(candidates[i], candidates[j]) < 0
	})
	preferred := ""
	for _, n := range candidates {
		res := a.checkAndSaveProxyHealth(*sub, n, binPath)
		if res.OK {
			preferred = n
			break
		}
	}
	if preferred == "" {
		msg := "检测失败"
		if primary.Error != nil {
			msg = *primary.Error
		}
		return fmt.Errorf("订阅内没有可用节点，启动已取消：%s", msg)
	}
	return a.mihomo.start(inst, st.Settings, binPath, sub.Proxies, preferred)
}

func (a *App) collectReservedPorts() map[int]struct{} {
	st := a.getState()
	reserved := map[int]struct{}{}
	for _, i := range st.Instances {
		reserved[i.MixedPort] = struct{}{}
		reserved[i.ControllerPort] = struct{}{}
	}
	return reserved
}

func isAllSubscriptionValue(v string) bool {
	s := strings.ToLower(strings.TrimSpace(v))
	return s == "" || s == "all" || s == "__all__"
}

func isAutoProxyValue(v string) bool {
	s := strings.ToLower(strings.TrimSpace(v))
	return s == "" || s == "all" || s == "__auto__"
}

type pickedProxy struct {
	SubscriptionID string
	Subscription   Subscription
	ProxyName      string
	Proxy          MihomoProxy
	Health         *HealthStatus
}

func (a *App) listUnusedProxyCandidates(scopeSubscriptionID string) []pickedProxy {
	st := a.getState()
	wantAll := isAllSubscriptionValue(scopeSubscriptionID)
	subs := make([]Subscription, 0)
	if wantAll {
		subs = append(subs, st.Subscriptions...)
	} else {
		for _, s := range st.Subscriptions {
			if s.ID == scopeSubscriptionID {
				subs = append(subs, s)
				break
			}
		}
	}
	if len(subs) == 0 {
		return []pickedProxy{}
	}
	usedBySub := map[string]map[string]struct{}{}
	for _, inst := range st.Instances {
		if usedBySub[inst.SubscriptionID] == nil {
			usedBySub[inst.SubscriptionID] = map[string]struct{}{}
		}
		usedBySub[inst.SubscriptionID][inst.ProxyName] = struct{}{}
	}
	candidates := make([]pickedProxy, 0)
	for _, sub := range subs {
		used := usedBySub[sub.ID]
		if used == nil {
			used = map[string]struct{}{}
		}
		health := a.loadProxyHealth(sub.ID)
		for _, p := range sub.Proxies {
			n := strings.TrimSpace(p.Name())
			if n == "" {
				continue
			}
			if _, ok := used[n]; ok {
				continue
			}
			var hp *HealthStatus
			if h, ok := health[n]; ok {
				hcopy := h
				hp = &hcopy
			}
			candidates = append(candidates, pickedProxy{SubscriptionID: sub.ID, Subscription: sub, ProxyName: n, Proxy: p, Health: hp})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		gi := rankHealth(candidates[i].Health)
		gj := rankHealth(candidates[j].Health)
		if gi != gj {
			return gi < gj
		}
		li := latencyValue(candidates[i].Health)
		lj := latencyValue(candidates[j].Health)
		if li != lj {
			return li < lj
		}
		if candidates[i].Subscription.Name != candidates[j].Subscription.Name {
			return strings.Compare(candidates[i].Subscription.Name, candidates[j].Subscription.Name) < 0
		}
		return strings.Compare(candidates[i].ProxyName, candidates[j].ProxyName) < 0
	})
	return candidates
}

func (a *App) bootstrapAutoStart() {
	st := a.getState()
	toStart := make([]Instance, 0)
	for _, i := range st.Instances {
		if i.AutoStart {
			toStart = append(toStart, i)
		}
	}
	if len(toStart) == 0 {
		return
	}
	binPath := a.installer.getBinPath()
	if _, err := os.Stat(binPath); err != nil {
		fmt.Println("检测到存在 autoStart 实例，但 mihomo 内核尚未安装，已跳过自动启动。")
		return
	}
	for _, inst := range toStart {
		if err := a.startInstanceWithPreflight(inst); err != nil {
			fmt.Printf("autoStart: 启动失败 %s: %v\n", inst.ID, err)
		} else {
			fmt.Printf("autoStart: 已启动 %s (%d)\n", inst.ID, inst.MixedPort)
		}
	}
}

func runWithConcurrency[T any](items []T, limit int, fn func(T)) {
	if len(items) == 0 {
		return
	}
	if limit < 1 {
		limit = 1
	}
	if limit > len(items) {
		limit = len(items)
	}
	var wg sync.WaitGroup
	ch := make(chan T)
	for i := 0; i < limit; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range ch {
				fn(it)
			}
		}()
	}
	for _, item := range items {
		ch <- item
	}
	close(ch)
	wg.Wait()
}

func healthCheckConcurrencyLimit(settings Settings, total int) int {
	limit := settings.HealthCheckConcurrency
	if limit < 1 {
		limit = 2
	}
	if total > 0 && limit > total {
		limit = total
	}
	if limit < 1 {
		return 1
	}
	return limit
}

func subscriptionProxyCheckConcurrencyLimit(_ Settings, total int) int {
	limit := 1
	if total > 0 && limit > total {
		limit = total
	}
	if limit < 1 {
		return 1
	}
	return limit
}

func (a *App) checkAllInstances(onlyRunning bool) {
	st := a.getState()
	list := make([]Instance, 0)
	for _, inst := range st.Instances {
		if onlyRunning {
			rt := a.mihomo.getRuntimeStatus(inst.ID)
			if !rt.Running {
				continue
			}
		}
		list = append(list, inst)
	}
	settings := a.getState().Settings
	type checkResult struct {
		subscriptionID string
		key            string
		status         HealthStatus
	}
	results := make([]checkResult, 0, len(list))
	var resultsMu sync.Mutex
	runWithConcurrency(list, healthCheckConcurrencyLimit(settings, len(list)), func(inst Instance) {
		res := a.mihomo.checkInstance(inst, settings)
		key := inst.ProxyName
		if res.ProxyName != nil && strings.TrimSpace(*res.ProxyName) != "" {
			key = strings.TrimSpace(*res.ProxyName)
		}
		resultsMu.Lock()
		results = append(results, checkResult{
			subscriptionID: inst.SubscriptionID,
			key:            key,
			status:         res,
		})
		resultsMu.Unlock()
	})
	merged := map[string]map[string]HealthStatus{}
	for _, r := range results {
		if merged[r.subscriptionID] == nil {
			merged[r.subscriptionID] = map[string]HealthStatus{}
		}
		merged[r.subscriptionID][r.key] = r.status
	}
	for subID, statusMap := range merged {
		current := a.loadProxyHealth(subID)
		for k, v := range statusMap {
			current[k] = v
		}
		a.saveProxyHealth(subID, current)
	}
}

func (a *App) autoHealthTick() {
	a.healthMu.Lock()
	if a.healthAutoRunning {
		a.healthMu.Unlock()
		return
	}
	a.healthAutoRunning = true
	a.healthMu.Unlock()
	defer func() {
		a.healthMu.Lock()
		a.healthAutoRunning = false
		a.healthMu.Unlock()
	}()
	a.checkAllInstances(true)
}

func (a *App) applyHealthSchedule() {
	a.healthMu.Lock()
	if a.healthTicker != nil {
		a.healthTicker.Stop()
		a.healthTicker = nil
	}
	if a.healthStop != nil {
		close(a.healthStop)
		a.healthStop = nil
	}
	sec := a.getState().Settings.HealthCheckIntervalSec
	if sec <= 0 {
		a.healthMu.Unlock()
		return
	}
	ticker := time.NewTicker(time.Duration(sec) * time.Second)
	stopCh := make(chan struct{})
	a.healthTicker = ticker
	a.healthStop = stopCh
	a.healthMu.Unlock()

	go func() {
		time.Sleep(800 * time.Millisecond)
		a.autoHealthTick()
		for {
			select {
			case <-ticker.C:
				a.autoHealthTick()
			case <-stopCh:
				return
			}
		}
	}()
}

func findSubscriptionIndex(subs []Subscription, id string) int {
	for i := range subs {
		if subs[i].ID == id {
			return i
		}
	}
	return -1
}

func (a *App) refreshSubscriptionByID(id string) (Subscription, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Subscription{}, errSubscriptionNotFound
	}
	st := a.getState()
	idx := findSubscriptionIndex(st.Subscriptions, id)
	if idx < 0 {
		return Subscription{}, errSubscriptionNotFound
	}
	sub := st.Subscriptions[idx]
	if sub.URL == nil || strings.TrimSpace(*sub.URL) == "" {
		return Subscription{}, errSubscriptionNoURL
	}
	txt, px, eff, err := fetchAndParseSubscriptionFromURL(*sub.URL)
	if err != nil {
		msg := err.Error()
		if saveErr := a.updateState(func(st *State) error {
			i := findSubscriptionIndex(st.Subscriptions, id)
			if i < 0 {
				return nil
			}
			cur := st.Subscriptions[i]
			cur.LastError = &msg
			cur.UpdatedAt = nowISO()
			st.Subscriptions[i] = cur
			return nil
		}); saveErr != nil {
			return Subscription{}, saveErr
		}
		return sub, err
	}
	var updated Subscription
	if saveErr := a.updateState(func(st *State) error {
		i := findSubscriptionIndex(st.Subscriptions, id)
		if i < 0 {
			return errSubscriptionNotFound
		}
		cur := st.Subscriptions[i]
		cur.URL = nilIfEmpty(eff)
		cur.UpdatedAt = nowISO()
		cur.LastError = nil
		cur.Proxies = px
		st.Subscriptions[i] = cur
		updated = cur
		return nil
	}); saveErr != nil {
		return Subscription{}, saveErr
	}
	a.writeSubscriptionSnapshot(id, txt)
	return updated, nil
}

func (a *App) refreshAllSubscriptionsAuto() {
	st := a.getState()
	for _, sub := range st.Subscriptions {
		if sub.URL == nil || strings.TrimSpace(*sub.URL) == "" {
			continue
		}
		if _, err := a.refreshSubscriptionByID(sub.ID); err != nil {
			fmt.Printf("订阅自动更新失败 id=%s name=%s err=%v\n", sub.ID, sub.Name, err)
		}
	}
}

func (a *App) autoSubscriptionRefreshTick() {
	a.subRefreshMu.Lock()
	if a.subRefreshAutoRunning {
		a.subRefreshMu.Unlock()
		return
	}
	a.subRefreshAutoRunning = true
	a.subRefreshMu.Unlock()
	defer func() {
		a.subRefreshMu.Lock()
		a.subRefreshAutoRunning = false
		a.subRefreshMu.Unlock()
	}()
	a.refreshAllSubscriptionsAuto()
}

func (a *App) applySubscriptionRefreshSchedule() {
	a.subRefreshMu.Lock()
	if a.subRefreshTicker != nil {
		a.subRefreshTicker.Stop()
		a.subRefreshTicker = nil
	}
	if a.subRefreshStop != nil {
		close(a.subRefreshStop)
		a.subRefreshStop = nil
	}
	min := a.getState().Settings.SubscriptionRefreshMin
	if min <= 0 {
		a.subRefreshMu.Unlock()
		return
	}
	ticker := time.NewTicker(time.Duration(min) * time.Minute)
	stopCh := make(chan struct{})
	a.subRefreshTicker = ticker
	a.subRefreshStop = stopCh
	a.subRefreshMu.Unlock()

	go func() {
		for {
			select {
			case <-ticker.C:
				a.autoSubscriptionRefreshTick()
			case <-stopCh:
				return
			}
		}
	}()
}

func tryParseSubscriptionText(text string) ([]MihomoProxy, error) {
	return parseSubscriptionYAML(text)
}

func fetchText(urlStr string) (string, error) {
	resp, err := http.Get(urlStr)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func fetchAndParseSubscriptionFromURL(urlStr string) (yamlText string, proxies []MihomoProxy, effectiveURL string, err error) {
	candidates := []struct {
		Label string
		URL   string
	}{}
	seen := map[string]struct{}{}
	addCandidate := func(label, u string) {
		u = strings.TrimSpace(u)
		if u == "" {
			return
		}
		if _, ok := seen[u]; ok {
			return
		}
		seen[u] = struct{}{}
		candidates = append(candidates, struct {
			Label string
			URL   string
		}{Label: label, URL: u})
	}
	addCandidate("原始链接", urlStr)
	if u := withSubscriptionFlag(urlStr, "clash-meta"); u != "" {
		addCandidate("flag=clash-meta", u)
	}
	if u := withSubscriptionFlag(urlStr, "meta"); u != "" {
		addCandidate("flag=meta", u)
	}
	if u := withSubscriptionFlag(urlStr, "clash"); u != "" {
		addCandidate("flag=clash", u)
	}
	errorsList := make([]string, 0)
	bestCount := -1
	bestText := ""
	bestProxies := []MihomoProxy(nil)
	for _, c := range candidates {
		text, ferr := fetchText(c.URL)
		if ferr != nil {
			errorsList = append(errorsList, fmt.Sprintf("%s 拉取失败：%v", c.Label, ferr))
			continue
		}
		parsed, perr := tryParseSubscriptionText(text)
		if perr != nil {
			errorsList = append(errorsList, fmt.Sprintf("%s 解析失败：%v", c.Label, perr))
			continue
		}
		if isWarningOnlySubscription(parsed) {
			errorsList = append(errorsList, fmt.Sprintf("%s 仅返回提示节点，继续尝试其它格式", c.Label))
			continue
		}
		if len(parsed) > bestCount {
			bestCount = len(parsed)
			bestText = text
			bestProxies = parsed
			effectiveURL = c.URL
		}
	}
	if bestCount >= 0 {
		return bestText, bestProxies, effectiveURL, nil
	}
	if len(errorsList) == 0 {
		return "", nil, "", fmt.Errorf("拉取订阅失败：无法解析订阅内容")
	}
	return "", nil, "", errors.New(strings.Join(errorsList, "；"))
}

func (a *App) withRuntime(inst Instance) gin.H {
	rt := a.mihomo.getRuntimeStatus(inst.ID)
	health := a.mihomo.getHealthStatus(inst.ID)
	return gin.H{
		"id":             inst.ID,
		"name":           inst.Name,
		"subscriptionId": inst.SubscriptionID,
		"proxyName":      inst.ProxyName,
		"proxy":          inst.Proxy,
		"mixedPort":      inst.MixedPort,
		"controllerPort": inst.ControllerPort,
		"autoStart":      inst.AutoStart,
		"autoSwitch":     inst.AutoSwitch,
		"createdAt":      inst.CreatedAt,
		"updatedAt":      inst.UpdatedAt,
		"runtime":        rt,
		"health":         health,
	}
}

func (a *App) buildPoolList() []gin.H {
	st := a.getState()
	rawHost := strings.TrimSpace(st.Settings.ExportHost)
	if rawHost == "" {
		rawHost = strings.TrimSpace(os.Getenv("PROXY_HOST"))
	}
	if rawHost == "" {
		rawHost = "127.0.0.1"
	}
	host := hostWithIPv6Bracket(rawHost)
	out := make([]gin.H, 0, len(st.Instances))
	for _, i := range st.Instances {
		rt := a.mihomo.getRuntimeStatus(i.ID)
		out = append(out, gin.H{
			"id":        i.ID,
			"name":      i.Name,
			"mixedPort": i.MixedPort,
			"proxy":     fmt.Sprintf("%s:%d", host, i.MixedPort),
			"running":   rt.Running,
		})
	}
	return out
}

func (a *App) parseStaticPath(reqPath string) string {
	if reqPath == "/" {
		return "public/index.html"
	}
	safe := path.Clean(strings.TrimPrefix(reqPath, "/"))
	if strings.HasPrefix(safe, "../") || safe == ".." {
		return ""
	}
	return "public/" + safe
}

func (a *App) serveStatic(c *gin.Context) {
	p := a.parseStaticPath(c.Request.URL.Path)
	if p == "" {
		notFound(c)
		return
	}
	b, err := fs.ReadFile(web.PublicFS, p)
	if err != nil {
		notFound(c)
		return
	}
	setNoStore(c)
	ext := strings.ToLower(filepath.Ext(p))
	switch ext {
	case ".html":
		c.Data(http.StatusOK, "text/html; charset=utf-8", b)
	case ".js":
		c.Data(http.StatusOK, "application/javascript; charset=utf-8", b)
	case ".css":
		c.Data(http.StatusOK, "text/css; charset=utf-8", b)
	case ".svg":
		c.Data(http.StatusOK, "image/svg+xml", b)
	case ".ico":
		c.Data(http.StatusOK, "image/x-icon", b)
	default:
		c.Data(http.StatusOK, http.DetectContentType(b), b)
	}
}

func (a *App) adminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodPost && c.Request.URL.Path == "/api/login" {
			c.Next()
			return
		}
		if !a.isAdminAuthorized(c) {
			unauthorized(c)
			c.Abort()
			return
		}
		c.Next()
	}
}

func (a *App) openAPIMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.TrimSpace(a.openapiToken) == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": fmt.Sprintf("openapi disabled: missing %s", openapiTokenEnvName)})
			c.Abort()
			return
		}
		if !a.isOpenAPIAuthorized(c) {
			unauthorized(c)
			c.Abort()
			return
		}
		c.Next()
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}

func (a *App) registerRoutes(r *gin.Engine) {
	a.router = r
	r.GET("/", a.handleHTMXRoot)
	r.GET("/htmx", a.handleHTMXRoot)

	r.GET("/ui", a.handleUIRootRedirect)
	r.GET("/ui/page", a.handleUIPage)
	r.POST("/ui/login", a.handleUILogin)
	r.POST("/ui/logout", a.handleUILogout)
	a.registerUIRoutes(r)

	r.NoRoute(a.serveStatic)

	api := r.Group("/api")
	api.Use(a.adminMiddleware())
	{
		api.POST("/login", a.handleLogin)
		api.GET("/system/ips", a.handleSystemIPs)
		api.POST("/settings/detect-public-ip", a.handleDetectPublicIP)
		api.GET("/mihomo/status", a.handleMihomoStatus)
		api.POST("/mihomo/latest", a.handleMihomoLatest)
		api.POST("/mihomo/install", a.handleMihomoInstall)
		api.GET("/state", a.handleState)
		api.GET("/settings", a.handleSettingsGet)
		api.POST("/settings/reset-proxy-auth", a.handleSettingsResetProxyAuth)
		api.PUT("/settings", a.handleSettingsPut)
		api.GET("/subscriptions", a.handleSubscriptionsGet)
		api.POST("/subscriptions", a.handleSubscriptionsPost)
		api.PUT("/subscriptions/:id", a.handleSubscriptionsPut)
		api.POST("/subscriptions/:id/refresh", a.handleSubscriptionsRefresh)
		api.DELETE("/subscriptions/:id", a.handleSubscriptionsDelete)
		api.GET("/subscriptions/:id/proxies", a.handleSubscriptionsProxies)
		api.GET("/subscriptions/availability", a.handleSubscriptionsAvailabilityAll)
		api.GET("/subscriptions/:id/availability", a.handleSubscriptionsAvailabilityByID)
		api.POST("/subscriptions/:id/proxies/check", a.handleSubscriptionsProxiesCheck)
		api.GET("/instances", a.handleInstancesGet)
		api.PUT("/instances/:id", a.handleInstancesPut)
		api.POST("/instances/batch", a.handleInstancesBatch)
		api.POST("/instances/check-all", a.handleInstancesCheckAll)
		api.POST("/instances", a.handleInstancesPost)
		api.POST("/instances/:id/start", a.handleInstancesStart)
		api.POST("/instances/:id/stop", a.handleInstancesStop)
		api.GET("/instances/:id/logs", a.handleInstancesLogs)
		api.POST("/instances/:id/check", a.handleInstancesCheck)
		api.DELETE("/instances/:id", a.handleInstancesDelete)
		api.GET("/pool", a.handlePool)
	}

	openapi := r.Group("/openapi")
	openapi.Use(a.openAPIMiddleware())
	{
		openapi.GET("/pool", a.handleOpenAPIPool)
	}
}

func (a *App) handleLogin(c *gin.Context) {
	body := decodeBody(c)
	token := strings.TrimSpace(asString(body["token"]))
	if token == "" {
		badRequest(c, "token 不能为空", nil)
		return
	}
	if sameToken(token, a.adminToken) {
		jsonOK(c, gin.H{"token": token, "tokenKey": authTokenKey})
		return
	}
	c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "token 无效"})
}

func (a *App) handleSystemIPs(c *gin.Context) {
	ips, best := listHostIPs()
	jsonOK(c, gin.H{"ips": ips, "best": best})
}

func (a *App) handleDetectPublicIP(c *gin.Context) {
	body := decodeBody(c)
	force := asBool(body["force"])
	ip, err := detectPublicIP()
	if err != nil {
		badRequest(c, err.Error(), nil)
		return
	}
	st := a.getState()
	current := strings.TrimSpace(st.Settings.ExportHost)
	shouldSave := force || current == ""
	if shouldSave {
		st.Settings.ExportHost = ip
		if err := a.saveStateDirect(st); err != nil {
			badRequest(c, err.Error(), nil)
			return
		}
	}
	exportHost := current
	if shouldSave {
		exportHost = ip
	}
	jsonOK(c, gin.H{"ip": ip, "saved": shouldSave, "exportHost": exportHost})
}

func (a *App) handleMihomoStatus(c *gin.Context) {
	jsonOK(c, gin.H{"status": a.installer.getStatus()})
}

func (a *App) handleMihomoLatest(c *gin.Context) {
	body := decodeBody(c)
	includePrerelease := asBool(body["includePrerelease"])
	latest, err := a.installer.getLatestInfo(includePrerelease)
	if err != nil {
		badRequest(c, err.Error(), nil)
		return
	}
	jsonOK(c, gin.H{"latest": latest})
}

func (a *App) handleMihomoInstall(c *gin.Context) {
	body := decodeBody(c)
	includePrerelease := asBool(body["includePrerelease"])
	force := asBool(body["force"])
	installed, err := a.installer.installLatest(includePrerelease, force)
	if err != nil {
		badRequest(c, err.Error(), nil)
		return
	}
	jsonOK(c, gin.H{"installed": installed})
}

func (a *App) handleState(c *gin.Context) {
	st := a.getState()
	instances := make([]gin.H, 0, len(st.Instances))
	for _, inst := range st.Instances {
		instances = append(instances, a.withRuntime(inst))
	}
	jsonOK(c, gin.H{"state": gin.H{"version": st.Version, "settings": st.Settings, "subscriptions": st.Subscriptions, "instances": instances}})
}

func (a *App) handleSettingsGet(c *gin.Context) {
	jsonOK(c, gin.H{"settings": a.getState().Settings})
}

func (a *App) handleSettingsResetProxyAuth(c *gin.Context) {
	st := a.getState()
	enabled := st.Settings.ProxyAuth.Enabled
	nextAuth := generateProxyAuth()
	nextAuth.Enabled = enabled
	st.Settings.ProxyAuth = nextAuth
	if err := a.saveStateDirect(st); err != nil {
		badRequest(c, err.Error(), nil)
		return
	}
	jsonOK(c, gin.H{"proxyAuth": nextAuth})
}

func settingsAffectRunningInstances(prev, next Settings) bool {
	if prev.BindAddress != next.BindAddress {
		return true
	}
	if prev.AllowLan != next.AllowLan {
		return true
	}
	if prev.LogLevel != next.LogLevel {
		return true
	}
	if prev.HealthCheckIntervalSec != next.HealthCheckIntervalSec {
		return true
	}
	if prev.HealthCheckURL != next.HealthCheckURL {
		return true
	}
	if prev.ProxyAuth.Enabled != next.ProxyAuth.Enabled {
		return true
	}
	if prev.ProxyAuth.Username != next.ProxyAuth.Username {
		return true
	}
	if prev.ProxyAuth.Password != next.ProxyAuth.Password {
		return true
	}
	return false
}

func (a *App) restartRunningInstancesWithSettings(settings Settings) ([]string, []string) {
	st := a.getState()
	running := make([]Instance, 0, len(st.Instances))
	for _, inst := range st.Instances {
		if a.mihomo.getRuntimeStatus(inst.ID).Running {
			running = append(running, inst)
		}
	}
	if len(running) == 0 {
		return []string{}, []string{}
	}

	binPath, err := a.getInstalledMihomoPath()
	if err != nil {
		return []string{}, []string{err.Error()}
	}

	restarted := make([]string, 0, len(running))
	errs := make([]string, 0)
	for _, inst := range running {
		a.mihomo.stopInstance(inst, 5*time.Second)
		if err := a.mihomo.start(inst, settings, binPath, a.getSubscriptionProxiesForInstance(inst), ""); err != nil {
			errs = append(errs, fmt.Sprintf("%s(%s): %v", inst.Name, inst.ID, err))
			continue
		}
		restarted = append(restarted, inst.ID)
	}
	return restarted, errs
}

func (a *App) handleSettingsPut(c *gin.Context) {
	body := decodeBody(c)
	st := a.getState()
	prev := st.Settings
	next := prev
	if _, ok := body["bindAddress"]; ok {
		v := strings.TrimSpace(asString(body["bindAddress"]))
		if v == "" {
			v = "127.0.0.1"
		}
		next.BindAddress = v
	}
	if _, ok := body["allowLan"]; ok {
		next.AllowLan = asBool(body["allowLan"])
	}
	if _, ok := body["logLevel"]; ok {
		next.LogLevel = strings.TrimSpace(asString(body["logLevel"]))
	}
	if _, ok := body["baseMixedPort"]; ok {
		if v, ok := asInt(body["baseMixedPort"]); ok {
			next.BaseMixedPort = v
		}
	}
	if _, ok := body["baseControllerPort"]; ok {
		if v, ok := asInt(body["baseControllerPort"]); ok {
			next.BaseControllerPort = v
		}
	}
	if _, ok := body["maxLogLines"]; ok {
		if v, ok := asInt(body["maxLogLines"]); ok {
			next.MaxLogLines = v
		}
	}
	if _, ok := body["healthCheckIntervalSec"]; ok {
		v, ok := asInt(body["healthCheckIntervalSec"])
		if !ok || v < 0 {
			badRequest(c, "自动检测间隔必须为非负数字（秒）", nil)
			return
		}
		next.HealthCheckIntervalSec = v
	}
	if _, ok := body["healthCheckConcurrency"]; ok {
		v, ok := asInt(body["healthCheckConcurrency"])
		if !ok || v <= 0 {
			badRequest(c, "检测并发数必须为正整数", nil)
			return
		}
		next.HealthCheckConcurrency = v
	}
	if _, ok := body["subscriptionRefreshIntervalMin"]; ok {
		v, ok := asInt(body["subscriptionRefreshIntervalMin"])
		if !ok || v < 0 {
			badRequest(c, "订阅自动更新间隔必须为非负数字（分钟）", nil)
			return
		}
		next.SubscriptionRefreshMin = v
	}
	if _, ok := body["healthCheckUrl"]; ok {
		v := strings.TrimSpace(asString(body["healthCheckUrl"]))
		if v == "" {
			badRequest(c, "检测链接不能为空", nil)
			return
		}
		u, err := url.Parse(v)
		if err != nil || u.Scheme == "" || u.Host == "" {
			badRequest(c, "检测链接不是合法 URL", nil)
			return
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			badRequest(c, "检测链接只支持 http/https", nil)
			return
		}
		next.HealthCheckURL = v
	}
	if _, ok := body["exportHost"]; ok {
		v, valid := normalizeHostInput(asString(body["exportHost"]))
		if !valid {
			badRequest(c, "导出 Host 格式不正确：只允许填写 IP/域名（不要带 http(s):// 或路径）", nil)
			return
		}
		next.ExportHost = v
	}
	if pv, ok := body["proxyAuth"]; ok {
		if pm, ok := pv.(map[string]any); ok {
			if ev, exists := pm["enabled"]; exists {
				enabled, ok := ev.(bool)
				if !ok {
					badRequest(c, "proxyAuth.enabled 必须为 boolean", nil)
					return
				}
				next.ProxyAuth.Enabled = enabled
			}
		}
	}
	st.Settings = next
	if err := a.saveStateDirect(st); err != nil {
		badRequest(c, err.Error(), nil)
		return
	}
	a.applyHealthSchedule()
	a.applySubscriptionRefreshSchedule()

	reload := gin.H{
		"triggered": false,
		"restarted": []string{},
		"errors":    []string{},
	}
	if settingsAffectRunningInstances(prev, next) {
		restarted, errs := a.restartRunningInstancesWithSettings(next)
		reload["triggered"] = true
		reload["restarted"] = restarted
		reload["errors"] = errs
	}
	jsonOK(c, gin.H{"settings": st.Settings, "instanceReload": reload})
}

func (a *App) handleSubscriptionsGet(c *gin.Context) {
	jsonOK(c, gin.H{"subscriptions": a.getState().Subscriptions})
}

func nilIfEmpty(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func (a *App) writeSubscriptionSnapshot(id, yamlText string) {
	_ = os.MkdirAll(filepath.Join(a.dataDir, "subscriptions"), 0o755)
	_ = os.WriteFile(filepath.Join(a.dataDir, "subscriptions", id+".yaml"), []byte(yamlText), 0o644)
}

func (a *App) handleSubscriptionsPost(c *gin.Context) {
	body := decodeBody(c)
	name := strings.TrimSpace(asString(body["name"]))
	urlStr := strings.TrimSpace(asString(body["url"]))
	rawYAML := asString(body["yaml"])
	if strings.TrimSpace(urlStr) == "" && strings.TrimSpace(rawYAML) == "" {
		badRequest(c, "url 或 yaml 需要至少提供一个", nil)
		return
	}
	yamlText := strings.TrimSpace(rawYAML)
	effectiveURL := strings.TrimSpace(urlStr)
	var proxies []MihomoProxy
	var lastErr *string
	if yamlText == "" && effectiveURL != "" {
		txt, px, eff, err := fetchAndParseSubscriptionFromURL(effectiveURL)
		if err != nil {
			e := err.Error()
			lastErr = &e
		} else {
			yamlText = txt
			proxies = px
			effectiveURL = eff
		}
	}
	if yamlText != "" && len(proxies) == 0 {
		px, err := parseSubscriptionYAML(yamlText)
		if err != nil {
			e := err.Error()
			lastErr = &e
		} else {
			proxies = px
			lastErr = nil
		}
	}
	if name == "" {
		name = inferSubscriptionName(effectiveURL, yamlText, proxies)
	}
	if name == "" {
		name = "未命名订阅"
	}
	id := uuid.NewString()
	createdAt := nowISO()
	sub := Subscription{ID: id, Name: name, URL: nilIfEmpty(effectiveURL), CreatedAt: createdAt, UpdatedAt: createdAt, LastError: lastErr, Proxies: proxies}
	st := a.getState()
	st.Subscriptions = append(st.Subscriptions, sub)
	if err := a.saveStateDirect(st); err != nil {
		badRequest(c, err.Error(), nil)
		return
	}
	if strings.TrimSpace(yamlText) != "" {
		a.writeSubscriptionSnapshot(id, yamlText)
	}
	jsonOK(c, gin.H{"subscription": sub})
}

func (a *App) findSubscription(id string) (*Subscription, int) {
	st := a.getState()
	for idx, s := range st.Subscriptions {
		if s.ID == id {
			sub := s
			return &sub, idx
		}
	}
	return nil, -1
}

func (a *App) handleSubscriptionsPut(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		notFound(c)
		return
	}
	st := a.getState()
	idx := -1
	for i, s := range st.Subscriptions {
		if s.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		notFound(c)
		return
	}
	sub := st.Subscriptions[idx]
	body := decodeBody(c)
	_, hasName := body["name"]
	_, hasURL := body["url"]
	_, hasYAML := body["yaml"]
	if !hasName && !hasURL && !hasYAML {
		badRequest(c, "至少需要提供一个可更新字段（name/url/yaml）", nil)
		return
	}
	nextName := sub.Name
	if hasName {
		nextName = strings.TrimSpace(asString(body["name"]))
	}
	if nextName == "" {
		badRequest(c, "name 不能为空", nil)
		return
	}
	nextURL := ""
	if sub.URL != nil {
		nextURL = *sub.URL
	}
	if hasURL {
		nextURL = strings.TrimSpace(asString(body["url"]))
	}
	nextProxies := cloneProxyList(sub.Proxies)
	nextLastErr := sub.LastError
	yamlSnapshot := ""
	yamlRaw := ""
	if hasYAML {
		yamlRaw = strings.TrimSpace(asString(body["yaml"]))
	}
	if yamlRaw != "" {
		px, err := parseSubscriptionYAML(yamlRaw)
		if err != nil {
			badRequest(c, fmt.Sprintf("更新失败：%v", err), nil)
			return
		}
		nextProxies = px
		nextLastErr = nil
		yamlSnapshot = yamlRaw
	} else if hasURL {
		if strings.TrimSpace(nextURL) == "" {
			badRequest(c, "url 不能为空（若希望改为手动 YAML，请同时提供 yaml）", nil)
			return
		}
		txt, px, eff, err := fetchAndParseSubscriptionFromURL(nextURL)
		if err != nil {
			badRequest(c, fmt.Sprintf("更新失败：%v", err), nil)
			return
		}
		nextURL = eff
		nextProxies = px
		nextLastErr = nil
		yamlSnapshot = txt
	}
	sub.Name = nextName
	sub.URL = nilIfEmpty(nextURL)
	sub.Proxies = nextProxies
	sub.LastError = nextLastErr
	sub.UpdatedAt = nowISO()
	st.Subscriptions[idx] = sub
	if err := a.saveStateDirect(st); err != nil {
		badRequest(c, err.Error(), nil)
		return
	}
	if yamlSnapshot != "" {
		a.writeSubscriptionSnapshot(id, yamlSnapshot)
	}
	jsonOK(c, gin.H{"subscription": sub})
}

func (a *App) handleSubscriptionsRefresh(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	sub, err := a.refreshSubscriptionByID(id)
	if errors.Is(err, errSubscriptionNotFound) {
		notFound(c)
		return
	}
	if errors.Is(err, errSubscriptionNoURL) {
		badRequest(c, "该订阅没有 url，无法刷新", nil)
		return
	}
	if err != nil {
		badRequest(c, fmt.Sprintf("刷新失败：%v", err), nil)
		return
	}
	jsonOK(c, gin.H{"subscription": sub})
}

func (a *App) handleSubscriptionsDelete(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	st := a.getState()
	idx := -1
	for i, s := range st.Subscriptions {
		if s.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		notFound(c)
		return
	}
	usedCount := 0
	for _, inst := range st.Instances {
		if inst.SubscriptionID == id {
			usedCount++
		}
	}
	if usedCount > 0 {
		badRequest(c, fmt.Sprintf("该订阅仍有 %d 个实例在使用，请先删除实例后再删除订阅", usedCount), nil)
		return
	}
	next := make([]Subscription, 0, len(st.Subscriptions)-1)
	for _, s := range st.Subscriptions {
		if s.ID != id {
			next = append(next, s)
		}
	}
	st.Subscriptions = next
	if err := a.saveStateDirect(st); err != nil {
		badRequest(c, err.Error(), nil)
		return
	}
	_ = os.Remove(filepath.Join(a.dataDir, "subscriptions", id+".yaml"))
	_ = a.storage.DeleteKV(proxyHealthKeyPrefix + id)
	jsonOK(c, gin.H{})
}

func (a *App) handleSubscriptionsProxies(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	st := a.getState()
	var sub *Subscription
	for _, s := range st.Subscriptions {
		if s.ID == id {
			t := s
			sub = &t
			break
		}
	}
	if sub == nil {
		notFound(c)
		return
	}
	health := a.loadProxyHealth(id)
	proxies := make([]gin.H, 0, len(sub.Proxies))
	for _, p := range sub.Proxies {
		name := p.Name()
		h, ok := health[name]
		var hv any = nil
		if ok {
			hv = h
		}
		row := gin.H{}
		for k, v := range cloneProxy(p) {
			row[k] = v
		}
		row["health"] = hv
		if _, ok := row["name"]; !ok {
			row["name"] = name
		}
		if _, ok := row["type"]; !ok {
			row["type"] = p.Type()
		}
		proxies = append(proxies, row)
	}
	jsonOK(c, gin.H{"proxies": proxies})
}

func (a *App) availabilityFor(sub *Subscription, all bool) gin.H {
	st := a.getState()
	usedBySub := map[string]map[string]struct{}{}
	for _, inst := range st.Instances {
		if usedBySub[inst.SubscriptionID] == nil {
			usedBySub[inst.SubscriptionID] = map[string]struct{}{}
		}
		usedBySub[inst.SubscriptionID][inst.ProxyName] = struct{}{}
	}
	total, used, available, untested, unhealthy := 0, 0, 0, 0, 0
	if all {
		for _, s := range st.Subscriptions {
			health := a.loadProxyHealth(s.ID)
			usedSet := usedBySub[s.ID]
			if usedSet == nil {
				usedSet = map[string]struct{}{}
			}
			total += len(s.Proxies)
			for _, p := range s.Proxies {
				n := p.Name()
				if _, ok := usedSet[n]; ok {
					used++
					continue
				}
				h, ok := health[n]
				if !ok {
					untested++
					continue
				}
				if h.OK {
					available++
				} else {
					unhealthy++
				}
			}
		}
		return gin.H{"subscriptionId": "all", "total": total, "used": used, "available": available, "untested": untested, "unhealthy": unhealthy, "target": st.Settings.HealthCheckURL}
	}
	if sub == nil {
		return gin.H{"subscriptionId": "", "total": 0, "used": 0, "available": 0, "untested": 0, "unhealthy": 0, "target": st.Settings.HealthCheckURL}
	}
	health := a.loadProxyHealth(sub.ID)
	usedSet := usedBySub[sub.ID]
	if usedSet == nil {
		usedSet = map[string]struct{}{}
	}
	total = len(sub.Proxies)
	for _, p := range sub.Proxies {
		n := p.Name()
		if _, ok := usedSet[n]; ok {
			used++
			continue
		}
		h, ok := health[n]
		if !ok {
			untested++
			continue
		}
		if h.OK {
			available++
		} else {
			unhealthy++
		}
	}
	return gin.H{"subscriptionId": sub.ID, "total": total, "used": used, "available": available, "untested": untested, "unhealthy": unhealthy, "target": st.Settings.HealthCheckURL}
}

func (a *App) handleSubscriptionsAvailabilityAll(c *gin.Context) {
	jsonOK(c, gin.H{"availability": a.availabilityFor(nil, true)})
}

func (a *App) handleSubscriptionsAvailabilityByID(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	st := a.getState()
	var sub *Subscription
	for _, s := range st.Subscriptions {
		if s.ID == id {
			t := s
			sub = &t
			break
		}
	}
	if sub == nil {
		notFound(c)
		return
	}
	jsonOK(c, gin.H{"availability": a.availabilityFor(sub, false)})
}

func (a *App) handleSubscriptionsProxiesCheck(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	st := a.getState()
	var sub *Subscription
	for _, s := range st.Subscriptions {
		if s.ID == id {
			t := s
			sub = &t
			break
		}
	}
	if sub == nil {
		notFound(c)
		return
	}
	body := decodeBody(c)
	all := asBool(body["all"])
	names := make([]string, 0)
	if all {
		for _, p := range sub.Proxies {
			names = append(names, p.Name())
		}
	} else if rawNames, ok := body["names"].([]any); ok && len(rawNames) > 0 {
		for _, x := range rawNames {
			n := strings.TrimSpace(asString(x))
			if n != "" {
				names = append(names, n)
			}
		}
	} else if proxyName := strings.TrimSpace(asString(body["proxyName"])); proxyName != "" {
		names = append(names, proxyName)
	} else {
		badRequest(c, "需要提供 all / names / proxyName", nil)
		return
	}
	nameSet := map[string]struct{}{}
	for _, p := range sub.Proxies {
		nameSet[p.Name()] = struct{}{}
	}
	invalid := make([]string, 0)
	for _, n := range names {
		if _, ok := nameSet[n]; !ok {
			invalid = append(invalid, n)
		}
	}
	if len(invalid) > 0 {
		badRequest(c, "存在未知节点", gin.H{"invalid": invalid})
		return
	}
	binPath, err := a.getInstalledMihomoPath()
	if err != nil {
		badRequest(c, err.Error(), nil)
		return
	}
	results := map[string]HealthStatus{}
	var resultsMu sync.Mutex
	runWithConcurrency(names, subscriptionProxyCheckConcurrencyLimit(a.getState().Settings, len(names)), func(name string) {
		res := a.mihomo.checkSubscriptionProxyDelay(id, sub.UpdatedAt, sub.Proxies, name, a.getState().Settings, binPath)
		resultsMu.Lock()
		results[name] = res
		resultsMu.Unlock()
	})
	current := a.loadProxyHealth(id)
	for k, v := range results {
		current[k] = v
	}
	a.saveProxyHealth(id, current)
	jsonOK(c, gin.H{"results": results})
}

func (a *App) handleInstancesGet(c *gin.Context) {
	st := a.getState()
	instances := make([]gin.H, 0, len(st.Instances))
	for _, inst := range st.Instances {
		instances = append(instances, a.withRuntime(inst))
	}
	jsonOK(c, gin.H{"instances": instances})
}

func (a *App) findInstanceByID(id string) (*Instance, int, State) {
	st := a.getState()
	for i, inst := range st.Instances {
		if inst.ID == id {
			x := inst
			return &x, i, st
		}
	}
	return nil, -1, st
}

func (a *App) handleInstancesPut(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	inst, idx, st := a.findInstanceByID(id)
	if inst == nil {
		notFound(c)
		return
	}
	body := decodeBody(c)
	v, ok := body["autoSwitch"].(bool)
	if !ok {
		badRequest(c, "autoSwitch 必须为 boolean", nil)
		return
	}
	if v == inst.AutoSwitch {
		jsonOK(c, gin.H{"instance": a.withRuntime(*inst)})
		return
	}
	next := *inst
	next.AutoSwitch = v
	next.UpdatedAt = nowISO()
	running := a.mihomo.getRuntimeStatus(inst.ID).Running
	if running {
		binPath, err := a.getInstalledMihomoPath()
		if err != nil {
			badRequest(c, err.Error(), nil)
			return
		}
		a.mihomo.stopInstance(*inst, 5*time.Second)
		if err := a.startInstanceWithPreflight(next); err != nil {
			_ = a.mihomo.start(*inst, a.getState().Settings, binPath, a.getSubscriptionProxiesForInstance(*inst), "")
			badRequest(c, fmt.Sprintf("更新失败：重启实例失败：%v", err), nil)
			return
		}
	}
	st.Instances[idx] = next
	if err := a.saveStateDirect(st); err != nil {
		badRequest(c, err.Error(), nil)
		return
	}
	jsonOK(c, gin.H{"instance": a.withRuntime(next)})
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (a *App) handleInstancesBatch(c *gin.Context) {
	body := decodeBody(c)
	subscriptionIDRaw := strings.TrimSpace(asString(body["subscriptionId"]))
	count, ok := asInt(body["count"])
	if !ok || count < 1 || count > 200 {
		badRequest(c, "count 非法（1-200）", nil)
		return
	}
	autoStart := asBool(body["autoStart"])
	autoSwitch := true
	if _, exists := body["autoSwitch"]; exists {
		autoSwitch = asBool(body["autoSwitch"])
	}
	wantAll := isAllSubscriptionValue(subscriptionIDRaw)
	if !wantAll && subscriptionIDRaw == "" {
		badRequest(c, "subscriptionId 不能为空", nil)
		return
	}
	st := a.getState()
	type candidate struct {
		SubscriptionID string
		Subscription   Subscription
		Name           string
		Proxy          MihomoProxy
		Health         *HealthStatus
	}
	chosen := make([]candidate, 0)
	if wantAll {
		usedBySub := map[string]map[string]struct{}{}
		for _, inst := range st.Instances {
			if usedBySub[inst.SubscriptionID] == nil {
				usedBySub[inst.SubscriptionID] = map[string]struct{}{}
			}
			usedBySub[inst.SubscriptionID][inst.ProxyName] = struct{}{}
		}
		candidates := make([]candidate, 0)
		for _, sub := range st.Subscriptions {
			health := a.loadProxyHealth(sub.ID)
			usedSet := usedBySub[sub.ID]
			if usedSet == nil {
				usedSet = map[string]struct{}{}
			}
			seen := map[string]struct{}{}
			for _, p := range sub.Proxies {
				n := p.Name()
				if n == "" {
					continue
				}
				if _, ok := seen[n]; ok {
					continue
				}
				seen[n] = struct{}{}
				if _, ok := usedSet[n]; ok {
					continue
				}
				h, ok := health[n]
				if !ok || !h.OK {
					continue
				}
				hcopy := h
				candidates = append(candidates, candidate{SubscriptionID: sub.ID, Subscription: sub, Name: n, Proxy: p, Health: &hcopy})
			}
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			li := latencyValue(candidates[i].Health)
			lj := latencyValue(candidates[j].Health)
			if li != lj {
				return li < lj
			}
			if candidates[i].Subscription.Name != candidates[j].Subscription.Name {
				return strings.Compare(candidates[i].Subscription.Name, candidates[j].Subscription.Name) < 0
			}
			return strings.Compare(candidates[i].Name, candidates[j].Name) < 0
		})
		if len(candidates) < count {
			avail := a.availabilityFor(nil, true)
			badRequest(c, "可用节点不足，请先在「订阅」->「节点」中进行检测", gin.H{"requested": count, "available": len(candidates), "total": avail["total"], "used": avail["used"], "untested": avail["untested"], "unhealthy": avail["unhealthy"], "target": avail["target"]})
			return
		}
		chosen = candidates[:count]
	} else {
		subscriptionID := subscriptionIDRaw
		var sub *Subscription
		for _, s := range st.Subscriptions {
			if s.ID == subscriptionID {
				t := s
				sub = &t
				break
			}
		}
		if sub == nil {
			badRequest(c, "subscriptionId 不存在", nil)
			return
		}
		health := a.loadProxyHealth(subscriptionID)
		used := map[string]struct{}{}
		for _, inst := range st.Instances {
			if inst.SubscriptionID == subscriptionID {
				used[inst.ProxyName] = struct{}{}
			}
		}
		seen := map[string]struct{}{}
		availableCandidates := make([]candidate, 0)
		for _, p := range sub.Proxies {
			n := p.Name()
			if n == "" {
				continue
			}
			if _, ok := seen[n]; ok {
				continue
			}
			seen[n] = struct{}{}
			if _, ok := used[n]; ok {
				continue
			}
			h, ok := health[n]
			if !ok || !h.OK {
				continue
			}
			hcopy := h
			availableCandidates = append(availableCandidates, candidate{SubscriptionID: subscriptionID, Subscription: *sub, Name: n, Proxy: p, Health: &hcopy})
		}
		sort.SliceStable(availableCandidates, func(i, j int) bool {
			return latencyValue(availableCandidates[i].Health) < latencyValue(availableCandidates[j].Health)
		})
		if len(availableCandidates) < count {
			avail := a.availabilityFor(sub, false)
			badRequest(c, "可用节点不足，请先在「订阅」->「节点」中进行检测", gin.H{"requested": count, "available": len(availableCandidates), "total": avail["total"], "used": avail["used"], "untested": avail["untested"], "unhealthy": avail["unhealthy"], "target": avail["target"]})
			return
		}
		chosen = availableCandidates[:count]
	}
	reserved := a.collectReservedPorts()
	bindHost := a.getState().Settings.BindAddress
	if strings.TrimSpace(bindHost) == "" {
		bindHost = "127.0.0.1"
	}
	nextMixedStart := a.getState().Settings.BaseMixedPort
	nextCtrlStart := a.getState().Settings.BaseControllerPort
	createdAt := nowISO()
	createdInstances := make([]Instance, 0, len(chosen))
	for _, cnd := range chosen {
		mixedPort, err := findNextFreePort(nextMixedStart, reserved, bindHost)
		if err != nil {
			badRequest(c, err.Error(), nil)
			return
		}
		reserved[mixedPort] = struct{}{}
		nextMixedStart = mixedPort + 1
		controllerPort, err := findNextFreePort(nextCtrlStart, reserved, "127.0.0.1")
		if err != nil {
			badRequest(c, err.Error(), nil)
			return
		}
		reserved[controllerPort] = struct{}{}
		nextCtrlStart = controllerPort + 1
		inst := Instance{ID: uuid.NewString(), Name: fmt.Sprintf("%s / %s", cnd.Subscription.Name, cnd.Name), SubscriptionID: cnd.SubscriptionID, ProxyName: cnd.Name, Proxy: cnd.Proxy, MixedPort: mixedPort, ControllerPort: controllerPort, AutoStart: autoStart, AutoSwitch: autoSwitch, CreatedAt: createdAt, UpdatedAt: createdAt}
		createdInstances = append(createdInstances, inst)
	}
	st = a.getState()
	st.Instances = append(st.Instances, createdInstances...)
	if err := a.saveStateDirect(st); err != nil {
		badRequest(c, err.Error(), nil)
		return
	}
	startErrors := map[string]string{}
	if autoStart {
		if _, err := a.getInstalledMihomoPath(); err != nil {
			badRequest(c, err.Error(), gin.H{"created": createdInstances})
			return
		}
		for _, inst := range createdInstances {
			if err := a.startInstanceWithPreflight(inst); err != nil {
				startErrors[inst.ID] = err.Error()
			}
		}
	}
	created := make([]gin.H, 0, len(createdInstances))
	for _, i := range createdInstances {
		created = append(created, a.withRuntime(i))
	}
	jsonOK(c, gin.H{"created": created, "startErrors": startErrors})
}

func (a *App) handleInstancesCheckAll(c *gin.Context) {
	a.checkAllInstances(true)
	st := a.getState()
	instances := make([]gin.H, 0, len(st.Instances))
	for _, inst := range st.Instances {
		instances = append(instances, a.withRuntime(inst))
	}
	jsonOK(c, gin.H{"instances": instances})
}

func (a *App) handleInstancesPost(c *gin.Context) {
	body := decodeBody(c)
	subscriptionIDRaw := strings.TrimSpace(asString(body["subscriptionId"]))
	proxyNameRaw := strings.TrimSpace(asString(body["proxyName"]))
	requestedPort := 0
	if v, ok := body["mixedPort"]; ok {
		if n, ok := asInt(v); ok {
			requestedPort = n
		}
	}
	autoStart := asBool(body["autoStart"])
	autoSwitch := true
	if _, ok := body["autoSwitch"]; ok {
		autoSwitch = asBool(body["autoSwitch"])
	}
	wantAuto := isAutoProxyValue(proxyNameRaw)
	scopeSubID := subscriptionIDRaw
	if isAllSubscriptionValue(subscriptionIDRaw) {
		scopeSubID = ""
	}
	binPath, err := a.getInstalledMihomoPath()
	if err != nil {
		badRequest(c, fmt.Sprintf("创建实例前需要先安装 mihomo 内核以进行节点检测：%v", err), nil)
		return
	}
	st := a.getState()
	var sub Subscription
	var proxy MihomoProxy
	var subscriptionID string
	var proxyName string
	if wantAuto {
		candidates := a.listUnusedProxyCandidates(scopeSubID)
		if len(candidates) == 0 {
			badRequest(c, "没有找到未被占用的节点（请先导入订阅，或删除旧实例释放节点）", nil)
			return
		}
		var picked *pickedProxy
		for _, cnd := range candidates {
			res := a.checkAndSaveProxyHealth(cnd.Subscription, cnd.ProxyName, binPath)
			if res.OK {
				x := cnd
				picked = &x
				break
			}
		}
		if picked == nil {
			wantAll := isAllSubscriptionValue(scopeSubID)
			subs := make([]Subscription, 0)
			if wantAll {
				subs = append(subs, st.Subscriptions...)
			} else {
				for _, s := range st.Subscriptions {
					if s.ID == scopeSubID {
						subs = append(subs, s)
					}
				}
			}
			usedBySub := map[string]map[string]struct{}{}
			for _, inst := range st.Instances {
				if usedBySub[inst.SubscriptionID] == nil {
					usedBySub[inst.SubscriptionID] = map[string]struct{}{}
				}
				usedBySub[inst.SubscriptionID][inst.ProxyName] = struct{}{}
			}
			total, used, untested, unhealthy := 0, 0, 0, 0
			for _, s := range subs {
				health := a.loadProxyHealth(s.ID)
				usedSet := usedBySub[s.ID]
				if usedSet == nil {
					usedSet = map[string]struct{}{}
				}
				total += len(s.Proxies)
				for _, p := range s.Proxies {
					n := p.Name()
					if _, ok := usedSet[n]; ok {
						used++
						continue
					}
					h, ok := health[n]
					if !ok {
						untested++
					} else if !h.OK {
						unhealthy++
					}
				}
			}
			badRequest(c, "没有找到可用节点，请先在「订阅」->「节点」中进行检测", gin.H{"total": total, "used": used, "untested": untested, "unhealthy": unhealthy, "target": st.Settings.HealthCheckURL})
			return
		}
		sub = picked.Subscription
		proxy = picked.Proxy
		subscriptionID = picked.SubscriptionID
		proxyName = picked.ProxyName
	} else {
		if isAllSubscriptionValue(subscriptionIDRaw) {
			badRequest(c, "选择了具体节点时，必须同时指定 subscriptionId", nil)
			return
		}
		var foundSub *Subscription
		for _, s := range st.Subscriptions {
			if s.ID == subscriptionIDRaw {
				t := s
				foundSub = &t
				break
			}
		}
		if foundSub == nil {
			badRequest(c, "subscriptionId 不存在", nil)
			return
		}
		var foundProxy MihomoProxy
		for _, p := range foundSub.Proxies {
			if p.Name() == proxyNameRaw {
				foundProxy = p
				break
			}
		}
		if foundProxy == nil {
			badRequest(c, "proxyName 不存在或不在订阅里", nil)
			return
		}
		for _, inst := range st.Instances {
			if inst.SubscriptionID == subscriptionIDRaw && inst.ProxyName == proxyNameRaw {
				badRequest(c, "该节点已被某个实例占用，请先删除旧实例或选择其他节点", nil)
				return
			}
		}
		health := a.checkAndSaveProxyHealth(*foundSub, proxyNameRaw, binPath)
		if !health.OK {
			msg := "检测失败"
			if health.Error != nil {
				msg = *health.Error
			}
			badRequest(c, fmt.Sprintf("节点不可用，创建已取消：%s", msg), gin.H{"health": health})
			return
		}
		sub = *foundSub
		proxy = foundProxy
		subscriptionID = subscriptionIDRaw
		proxyName = proxyNameRaw
	}
	for _, inst := range st.Instances {
		if inst.SubscriptionID == subscriptionID && inst.ProxyName == proxyName {
			badRequest(c, "该节点已被某个实例占用，请先删除旧实例或选择其他节点", nil)
			return
		}
	}
	bindHost := st.Settings.BindAddress
	if strings.TrimSpace(bindHost) == "" {
		bindHost = "127.0.0.1"
	}
	reserved := a.collectReservedPorts()
	mixedPort := requestedPort
	if mixedPort != 0 {
		if mixedPort < 1 || mixedPort > 65535 {
			badRequest(c, "mixedPort 非法（1-65535）", nil)
			return
		}
		if _, exists := reserved[mixedPort]; exists {
			badRequest(c, "mixedPort 已被其他实例占用（配置层面）", nil)
			return
		}
		if !isPortFree(mixedPort, bindHost) {
			badRequest(c, "mixedPort 已被系统占用（端口监听冲突）", nil)
			return
		}
	} else {
		var ferr error
		mixedPort, ferr = findNextFreePort(st.Settings.BaseMixedPort, reserved, bindHost)
		if ferr != nil {
			badRequest(c, ferr.Error(), nil)
			return
		}
	}
	reserved[mixedPort] = struct{}{}
	controllerPort, err := findNextFreePort(st.Settings.BaseControllerPort, reserved, "127.0.0.1")
	if err != nil {
		badRequest(c, err.Error(), nil)
		return
	}
	id := uuid.NewString()
	createdAt := nowISO()
	inst := Instance{ID: id, Name: fmt.Sprintf("%s / %s", sub.Name, proxyName), SubscriptionID: subscriptionID, ProxyName: proxyName, Proxy: proxy, MixedPort: mixedPort, ControllerPort: controllerPort, AutoStart: autoStart, AutoSwitch: autoSwitch, CreatedAt: createdAt, UpdatedAt: createdAt}
	st.Instances = append(st.Instances, inst)
	if err := a.saveStateDirect(st); err != nil {
		badRequest(c, err.Error(), nil)
		return
	}
	if autoStart {
		if err := a.startInstanceWithPreflight(inst); err != nil {
			badRequest(c, fmt.Sprintf("创建成功但启动失败：%v", err), gin.H{"instance": inst})
			return
		}
	}
	jsonOK(c, gin.H{"instance": a.withRuntime(inst)})
}

func (a *App) handleInstancesStart(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	inst, _, _ := a.findInstanceByID(id)
	if inst == nil {
		notFound(c)
		return
	}
	if err := a.startInstanceWithPreflight(*inst); err != nil {
		badRequest(c, err.Error(), nil)
		return
	}
	jsonOK(c, gin.H{"instance": a.withRuntime(*inst)})
}

func (a *App) handleInstancesStop(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	inst, _, _ := a.findInstanceByID(id)
	if inst == nil {
		notFound(c)
		return
	}
	a.mihomo.stopInstance(*inst, 5*time.Second)
	jsonOK(c, gin.H{"instance": a.withRuntime(*inst)})
}

func (a *App) handleInstancesLogs(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	inst, _, _ := a.findInstanceByID(id)
	if inst == nil {
		notFound(c)
		return
	}
	jsonOK(c, gin.H{"lines": a.mihomo.getLogs(id)})
}

func (a *App) handleInstancesCheck(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	inst, _, _ := a.findInstanceByID(id)
	if inst == nil {
		notFound(c)
		return
	}
	health := a.mihomo.checkInstance(*inst, a.getState().Settings)
	m := a.loadProxyHealth(inst.SubscriptionID)
	key := inst.ProxyName
	if health.ProxyName != nil && strings.TrimSpace(*health.ProxyName) != "" {
		key = strings.TrimSpace(*health.ProxyName)
	}
	m[key] = health
	a.saveProxyHealth(inst.SubscriptionID, m)
	jsonOK(c, gin.H{"health": health})
}

func (a *App) handleInstancesDelete(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	inst, _, st := a.findInstanceByID(id)
	if inst == nil {
		notFound(c)
		return
	}
	a.mihomo.stopInstance(*inst, 5*time.Second)
	next := make([]Instance, 0, len(st.Instances)-1)
	for _, it := range st.Instances {
		if it.ID != id {
			next = append(next, it)
		}
	}
	st.Instances = next
	if err := a.saveStateDirect(st); err != nil {
		badRequest(c, err.Error(), nil)
		return
	}
	jsonOK(c, gin.H{})
}

func (a *App) handlePool(c *gin.Context) {
	jsonOK(c, gin.H{"proxies": a.buildPoolList()})
}

func (a *App) handleOpenAPIPool(c *gin.Context) {
	jsonOK(c, gin.H{"proxies": a.buildPoolList()})
}
