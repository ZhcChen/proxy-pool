package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

type runningProcess struct {
	ID        string
	PID       int
	StartedAt string
	Cmd       *exec.Cmd
	LogLines  []string
	LogMu     sync.Mutex
}

type proxyCheckerProc struct {
	SubID          string
	Version        string
	Dir            string
	ControllerPort int
	Cmd            *exec.Cmd
	LastUsedAt     time.Time
	StopTimer      *time.Timer
	Ready          chan error
}

type MihomoManager struct {
	dataDir string

	mu                sync.Mutex
	running           map[string]*runningProcess
	health            map[string]HealthStatus
	proxyCheckers     map[string]*proxyCheckerProc
	proxyCheckerStart map[string]chan struct{}
}

func NewMihomoManager(dataDir string) *MihomoManager {
	return &MihomoManager{
		dataDir:           dataDir,
		running:           map[string]*runningProcess{},
		health:            map[string]HealthStatus{},
		proxyCheckers:     map[string]*proxyCheckerProc{},
		proxyCheckerStart: map[string]chan struct{}{},
	}
}

func (m *MihomoManager) getRuntimeStatus(instanceID string) runtimeStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	rp := m.running[instanceID]
	if rp == nil {
		return runtimeStatus{Running: false, PID: nil, StartedAt: nil}
	}
	pid := rp.PID
	startedAt := rp.StartedAt
	return runtimeStatus{Running: true, PID: &pid, StartedAt: &startedAt}
}

func (m *MihomoManager) getLogs(instanceID string) []string {
	m.mu.Lock()
	rp := m.running[instanceID]
	m.mu.Unlock()
	if rp == nil {
		return []string{}
	}
	rp.LogMu.Lock()
	defer rp.LogMu.Unlock()
	out := make([]string, len(rp.LogLines))
	copy(out, rp.LogLines)
	return out
}

func (m *MihomoManager) getHealthStatus(instanceID string) *HealthStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.health[instanceID]
	if !ok {
		return nil
	}
	cp := h
	return &cp
}

func (m *MihomoManager) setHealthStatus(instanceID string, status HealthStatus) {
	m.mu.Lock()
	m.health[instanceID] = status
	m.mu.Unlock()
}

func (m *MihomoManager) verifyMihomoBinary(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("找不到 mihomo 可执行文件：%s", path)
	}
	if st.IsDir() {
		return fmt.Errorf("mihomo 路径不是可执行文件：%s", path)
	}
	return nil
}

func (m *MihomoManager) instanceDir(instanceID string) string {
	return filepath.Join(m.dataDir, "instances", instanceID)
}

func (m *MihomoManager) configPath(instanceID string) string {
	return filepath.Join(m.instanceDir(instanceID), "config.yaml")
}

func orderedProxyNames(instance Instance, subscriptionProxies []MihomoProxy, preferred string, autoSwitch bool) []string {
	proxyByName := map[string]MihomoProxy{}
	for _, p := range subscriptionProxies {
		name := strings.TrimSpace(p.Name())
		if name == "" {
			continue
		}
		if _, ok := proxyByName[name]; !ok {
			proxyByName[name] = cloneProxy(p)
		}
	}
	if instance.ProxyName != "" {
		proxyByName[instance.ProxyName] = cloneProxy(instance.Proxy)
	}
	primary := instance.ProxyName
	if preferred != "" {
		if _, ok := proxyByName[preferred]; ok {
			primary = preferred
		}
	}

	names := make([]string, 0, len(proxyByName))
	if primary != "" {
		names = append(names, primary)
	}
	for name := range proxyByName {
		if name == primary {
			continue
		}
		names = append(names, name)
	}
	if !autoSwitch {
		return []string{instance.ProxyName}
	}
	return names
}

func (m *MihomoManager) writeConfig(instance Instance, settings Settings, subscriptionProxies []MihomoProxy, preferred string) error {
	dir := m.instanceDir(instance.ID)
	if err := ensureDir(dir); err != nil {
		return err
	}
	autoSwitch := instance.AutoSwitch
	ordered := orderedProxyNames(instance, subscriptionProxies, preferred, autoSwitch)
	proxyByName := map[string]MihomoProxy{}
	for _, p := range subscriptionProxies {
		name := strings.TrimSpace(p.Name())
		if name == "" {
			continue
		}
		if _, ok := proxyByName[name]; !ok {
			proxyByName[name] = cloneProxy(p)
		}
	}
	if instance.ProxyName != "" {
		proxyByName[instance.ProxyName] = cloneProxy(instance.Proxy)
	}
	proxies := make([]MihomoProxy, 0)
	for _, n := range ordered {
		if p, ok := proxyByName[n]; ok {
			proxies = append(proxies, p)
		}
	}
	if len(proxies) == 0 {
		proxies = append(proxies, cloneProxy(instance.Proxy))
	}

	group := map[string]any{}
	if autoSwitch {
		interval := settings.HealthCheckIntervalSec
		if interval <= 0 {
			interval = 60
		}
		group = map[string]any{
			"name":     "PROXY",
			"type":     "fallback",
			"proxies":  ordered,
			"url":      settings.HealthCheckURL,
			"interval": interval,
		}
	} else {
		group = map[string]any{
			"name":    "PROXY",
			"type":    "select",
			"proxies": []string{instance.ProxyName},
		}
	}

	cfg := map[string]any{
		"mixed-port":          instance.MixedPort,
		"allow-lan":           settings.AllowLan,
		"bind-address":        settings.BindAddress,
		"mode":                "rule",
		"log-level":           settings.LogLevel,
		"external-controller": fmt.Sprintf("127.0.0.1:%d", instance.ControllerPort),
		"proxies":             proxies,
		"proxy-groups":        []map[string]any{group},
		"rules":               []string{"MATCH,PROXY"},
	}
	if settings.ProxyAuth.Enabled {
		cfg["authentication"] = []string{fmt.Sprintf("%s:%s", settings.ProxyAuth.Username, settings.ProxyAuth.Password)}
	}

	b, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(m.configPath(instance.ID), b, 0o644)
}

func (m *MihomoManager) start(instance Instance, settings Settings, mihomoPath string, subscriptionProxies []MihomoProxy, preferred string) error {
	m.mu.Lock()
	if m.running[instance.ID] != nil {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	if strings.TrimSpace(mihomoPath) == "" {
		return fmt.Errorf("mihomo 内核未安装")
	}
	if err := m.verifyMihomoBinary(mihomoPath); err != nil {
		return err
	}
	dir := m.instanceDir(instance.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := m.writeConfig(instance, settings, subscriptionProxies, preferred); err != nil {
		return err
	}
	cfgPath := m.configPath(instance.ID)
	cmd := exec.Command(mihomoPath, "-d", dir, "-f", cfgPath)
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	rp := &runningProcess{ID: instance.ID, PID: cmd.Process.Pid, StartedAt: nowISO(), Cmd: cmd, LogLines: []string{}}
	m.mu.Lock()
	m.running[instance.ID] = rp
	m.mu.Unlock()

	go m.pumpLogs(instance.ID, stdout, settings.MaxLogLines, "[stdout]")
	go m.pumpLogs(instance.ID, stderr, settings.MaxLogLines, "[stderr]")
	go func() {
		err := cmd.Wait()
		rp.LogMu.Lock()
		if err != nil {
			rp.LogLines = append(rp.LogLines, fmt.Sprintf("%s [exit] err=%v", nowISO(), err))
		} else {
			rp.LogLines = append(rp.LogLines, fmt.Sprintf("%s [exit] code=0", nowISO()))
		}
		rp.LogMu.Unlock()
		m.mu.Lock()
		delete(m.running, instance.ID)
		m.mu.Unlock()
	}()
	return nil
}

func readJSONResponse(resp *http.Response, out any) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

func (m *MihomoManager) checkInstance(instance Instance, settings Settings) HealthStatus {
	checkedAt := nowISO()
	target := strings.TrimSpace(settings.HealthCheckURL)
	if target == "" {
		err := "未配置检测链接"
		s := HealthStatus{OK: false, CheckedAt: checkedAt, LatencyMs: nil, Error: &err, Target: &target, ProxyName: &instance.ProxyName}
		m.setHealthStatus(instance.ID, s)
		return s
	}
	m.mu.Lock()
	_, running := m.running[instance.ID]
	m.mu.Unlock()
	if !running {
		err := "实例未运行"
		s := HealthStatus{OK: false, CheckedAt: checkedAt, LatencyMs: nil, Error: &err, Target: &target, ProxyName: &instance.ProxyName}
		m.setHealthStatus(instance.ID, s)
		return s
	}

	probeProxyName := instance.ProxyName
	if instance.AutoSwitch {
		urlProxies := fmt.Sprintf("http://127.0.0.1:%d/proxies", instance.ControllerPort)
		client := &http.Client{Timeout: 3 * time.Second}
		if resp, err := client.Get(urlProxies); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				var data struct {
					Proxies map[string]struct {
						Now string `json:"now"`
					} `json:"proxies"`
				}
				if err := readJSONResponse(resp, &data); err == nil {
					if p, ok := data.Proxies["PROXY"]; ok {
						if strings.TrimSpace(p.Now) != "" {
							probeProxyName = strings.TrimSpace(p.Now)
						}
					}
				}
			}
		}
	}

	timeoutMs := 5000
	u := fmt.Sprintf("http://127.0.0.1:%d/proxies/%s/delay?timeout=%d&url=%s", instance.ControllerPort, url.PathEscape(probeProxyName), timeoutMs, url.QueryEscape(target))
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs+2500)*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e := err.Error()
		s := HealthStatus{OK: false, CheckedAt: checkedAt, LatencyMs: nil, Error: &e, Target: &target, ProxyName: &probeProxyName}
		m.setHealthStatus(instance.ID, s)
		return s
	}
	defer resp.Body.Close()
	var data struct {
		Delay   *float64 `json:"delay"`
		Message string   `json:"message"`
	}
	_ = readJSONResponse(resp, &data)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errMsg := data.Message
		if strings.TrimSpace(errMsg) == "" {
			errMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		s := HealthStatus{OK: false, CheckedAt: checkedAt, LatencyMs: nil, Error: &errMsg, Target: &target, ProxyName: &probeProxyName}
		m.setHealthStatus(instance.ID, s)
		return s
	}
	if data.Delay != nil {
		d := *data.Delay
		if d <= 0 {
			errMsg := "不可用（delay=0）"
			s := HealthStatus{OK: false, CheckedAt: checkedAt, LatencyMs: &d, Error: &errMsg, Target: &target, ProxyName: &probeProxyName}
			m.setHealthStatus(instance.ID, s)
			return s
		}
		s := HealthStatus{OK: true, CheckedAt: checkedAt, LatencyMs: &d, Target: &target, ProxyName: &probeProxyName}
		m.setHealthStatus(instance.ID, s)
		return s
	}
	errMsg := "delay 响应缺少数值"
	s := HealthStatus{OK: false, CheckedAt: checkedAt, LatencyMs: nil, Error: &errMsg, Target: &target, ProxyName: &probeProxyName}
	m.setHealthStatus(instance.ID, s)
	return s
}

func (m *MihomoManager) stop(instanceID string, timeout time.Duration) {
	m.mu.Lock()
	rp := m.running[instanceID]
	m.mu.Unlock()
	if rp == nil {
		return
	}
	if rp.Cmd.Process != nil {
		_ = rp.Cmd.Process.Signal(syscall.SIGTERM)
	}
	done := make(chan struct{})
	go func() {
		_, _ = rp.Cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		if rp.Cmd.Process != nil {
			_ = rp.Cmd.Process.Kill()
		}
	}
}

func listListeningPids(port int) []int {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("powershell", "-NoProfile", "-Command", fmt.Sprintf(`Get-NetTCPConnection -State Listen -LocalPort %d | Select-Object -ExpandProperty OwningProcess`, port))
		out, err := cmd.Output()
		if err != nil {
			return nil
		}
		lines := strings.Fields(string(out))
		res := make([]int, 0, len(lines))
		seen := map[int]struct{}{}
		for _, line := range lines {
			n, err := strconv.Atoi(strings.TrimSpace(line))
			if err == nil && n > 0 {
				if _, ok := seen[n]; !ok {
					seen[n] = struct{}{}
					res = append(res, n)
				}
			}
		}
		return res
	}
	cmd := exec.Command("lsof", "-nP", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN", "-t")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	fields := strings.Fields(string(out))
	res := make([]int, 0, len(fields))
	seen := map[int]struct{}{}
	for _, f := range fields {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err == nil && n > 0 {
			if _, ok := seen[n]; !ok {
				seen[n] = struct{}{}
				res = append(res, n)
			}
		}
	}
	return res
}

func killPID(pid int, sig os.Signal) {
	if pid <= 0 || pid == os.Getpid() {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if sig == nil {
		_ = proc.Kill()
		return
	}
	if err := proc.Signal(sig); err != nil {
		_ = proc.Kill()
	}
}

func (m *MihomoManager) stopInstance(instance Instance, timeout time.Duration) {
	m.stop(instance.ID, timeout)
	ports := []int{instance.MixedPort, instance.ControllerPort}
	seen := map[int]struct{}{}
	for _, p := range ports {
		if p <= 0 {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		pids := listListeningPids(p)
		for _, pid := range pids {
			killPID(pid, os.Kill)
		}
		deadline := time.Now().Add(1600 * time.Millisecond)
		for time.Now().Before(deadline) {
			left := listListeningPids(p)
			if len(left) == 0 {
				break
			}
			time.Sleep(120 * time.Millisecond)
		}
	}
}

func (m *MihomoManager) stopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.running))
	for id := range m.running {
		ids = append(ids, id)
	}
	subIDs := make([]string, 0, len(m.proxyCheckers))
	for id := range m.proxyCheckers {
		subIDs = append(subIDs, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.stop(id, 5*time.Second)
	}
	for _, subID := range subIDs {
		m.stopProxyChecker(subID)
	}
}

func pickEphemeralPort(host string) (int, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("invalid tcp addr")
	}
	return addr.Port, nil
}

func waitForControllerReady(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	u := fmt.Sprintf("http://127.0.0.1:%d/proxies", port)
	for time.Now().Before(deadline) {
		resp, err := http.Get(u)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
		time.Sleep(120 * time.Millisecond)
	}
	return fmt.Errorf("检测器启动超时（controller 未就绪）")
}

func (m *MihomoManager) scheduleStopProxyChecker(subID string) {
	m.mu.Lock()
	checker := m.proxyCheckers[subID]
	if checker == nil {
		m.mu.Unlock()
		return
	}
	checker.LastUsedAt = time.Now()
	if checker.StopTimer != nil {
		checker.StopTimer.Stop()
	}
	checker.StopTimer = time.AfterFunc(2*time.Minute, func() {
		m.stopProxyChecker(subID)
	})
	m.mu.Unlock()
}

func (m *MihomoManager) stopProxyChecker(subID string) {
	m.mu.Lock()
	checker := m.proxyCheckers[subID]
	if checker == nil {
		m.mu.Unlock()
		return
	}
	delete(m.proxyCheckers, subID)
	m.mu.Unlock()

	if checker.StopTimer != nil {
		checker.StopTimer.Stop()
	}
	if checker.Cmd.Process != nil {
		_ = checker.Cmd.Process.Signal(syscall.SIGTERM)
	}
	done := make(chan struct{})
	go func() {
		if checker.Cmd.Process != nil {
			_, _ = checker.Cmd.Process.Wait()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2500 * time.Millisecond):
		if checker.Cmd.Process != nil {
			_ = checker.Cmd.Process.Kill()
		}
	}
}

func (m *MihomoManager) ensureProxyChecker(subID string, proxies []MihomoProxy, version string, mihomoPath string) (*proxyCheckerProc, error) {
	m.mu.Lock()
	existing := m.proxyCheckers[subID]
	if existing != nil && existing.Version == version {
		m.mu.Unlock()
		m.scheduleStopProxyChecker(subID)
		return existing, nil
	}
	if existing != nil {
		m.mu.Unlock()
		m.stopProxyChecker(subID)
		m.mu.Lock()
	}
	if ch, ok := m.proxyCheckerStart[subID]; ok {
		m.mu.Unlock()
		<-ch
		m.mu.Lock()
		ck := m.proxyCheckers[subID]
		m.mu.Unlock()
		if ck == nil {
			return nil, fmt.Errorf("检测器启动失败")
		}
		return ck, nil
	}
	waitCh := make(chan struct{})
	m.proxyCheckerStart[subID] = waitCh
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.proxyCheckerStart, subID)
		close(waitCh)
		m.mu.Unlock()
	}()

	if err := m.verifyMihomoBinary(mihomoPath); err != nil {
		return nil, err
	}
	dir := filepath.Join(m.dataDir, "proxy-checkers", subID)
	if err := ensureDir(dir); err != nil {
		return nil, err
	}
	controllerPort, err := pickEphemeralPort("127.0.0.1")
	if err != nil {
		return nil, err
	}
	mixedPort, err := pickEphemeralPort("127.0.0.1")
	if err != nil {
		return nil, err
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := map[string]any{
		"mixed-port":          mixedPort,
		"allow-lan":           false,
		"bind-address":        "127.0.0.1",
		"mode":                "rule",
		"log-level":           "warning",
		"external-controller": fmt.Sprintf("127.0.0.1:%d", controllerPort),
		"secret":              "",
		"proxies":             proxies,
		"rules":               []string{"MATCH,DIRECT"},
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(cfgPath, b, 0o644); err != nil {
		return nil, err
	}

	cmd := exec.Command(mihomoPath, "-d", dir, "-f", cfgPath)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	checker := &proxyCheckerProc{
		SubID:          subID,
		Version:        version,
		Dir:            dir,
		ControllerPort: controllerPort,
		Cmd:            cmd,
		LastUsedAt:     time.Now(),
	}
	if err := waitForControllerReady(controllerPort, 8*time.Second); err != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil, err
	}
	go func() {
		_ = cmd.Wait()
		m.mu.Lock()
		delete(m.proxyCheckers, subID)
		m.mu.Unlock()
	}()
	m.mu.Lock()
	m.proxyCheckers[subID] = checker
	m.mu.Unlock()
	m.scheduleStopProxyChecker(subID)
	return checker, nil
}

func (m *MihomoManager) checkSubscriptionProxyDelay(subscriptionID, version string, proxies []MihomoProxy, proxyName string, settings Settings, mihomoPath string) HealthStatus {
	checkedAt := nowISO()
	target := strings.TrimSpace(settings.HealthCheckURL)
	if target == "" {
		errMsg := "未配置检测链接"
		return HealthStatus{OK: false, CheckedAt: checkedAt, LatencyMs: nil, Error: &errMsg, Target: &target}
	}
	checker, err := m.ensureProxyChecker(subscriptionID, proxies, version, mihomoPath)
	if err != nil {
		errMsg := err.Error()
		return HealthStatus{OK: false, CheckedAt: checkedAt, LatencyMs: nil, Error: &errMsg, Target: &target}
	}
	urlCheck := fmt.Sprintf("http://127.0.0.1:%d/proxies/%s/delay?timeout=%d&url=%s", checker.ControllerPort, url.PathEscape(proxyName), 5000, url.QueryEscape(target))
	ctx, cancel := context.WithTimeout(context.Background(), 7500*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, urlCheck, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		msg := err.Error()
		m.scheduleStopProxyChecker(subscriptionID)
		return HealthStatus{OK: false, CheckedAt: checkedAt, LatencyMs: nil, Error: &msg, Target: &target}
	}
	defer resp.Body.Close()
	var data struct {
		Delay   *float64 `json:"delay"`
		Message string   `json:"message"`
	}
	_ = readJSONResponse(resp, &data)
	m.scheduleStopProxyChecker(subscriptionID)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := data.Message
		if strings.TrimSpace(msg) == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return HealthStatus{OK: false, CheckedAt: checkedAt, LatencyMs: nil, Error: &msg, Target: &target}
	}
	if data.Delay == nil {
		msg := "delay 响应缺少数值"
		return HealthStatus{OK: false, CheckedAt: checkedAt, LatencyMs: nil, Error: &msg, Target: &target}
	}
	d := *data.Delay
	if d <= 0 {
		msg := "不可用（delay=0）"
		return HealthStatus{OK: false, CheckedAt: checkedAt, LatencyMs: &d, Error: &msg, Target: &target}
	}
	return HealthStatus{OK: true, CheckedAt: checkedAt, LatencyMs: &d, Target: &target}
}

func (m *MihomoManager) pumpLogs(instanceID string, r io.ReadCloser, maxLines int, tag string) {
	defer r.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	flush := func(lines []string) {
		m.mu.Lock()
		rp := m.running[instanceID]
		m.mu.Unlock()
		if rp == nil {
			return
		}
		rp.LogMu.Lock()
		defer rp.LogMu.Unlock()
		for _, line := range lines {
			rp.LogLines = append(rp.LogLines, fmt.Sprintf("%s %s %s", nowISO(), tag, line))
		}
		if maxLines > 0 && len(rp.LogLines) > maxLines {
			rp.LogLines = rp.LogLines[len(rp.LogLines)-maxLines:]
		}
	}
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			parts := bytes.Split(buf, []byte("\n"))
			if len(parts) > 1 {
				lines := make([]string, 0, len(parts)-1)
				for _, part := range parts[:len(parts)-1] {
					line := strings.TrimRight(string(part), "\r")
					lines = append(lines, line)
				}
				flush(lines)
				buf = parts[len(parts)-1]
			}
		}
		if err != nil {
			if err != io.EOF {
				flush([]string{fmt.Sprintf("log read error: %v", err)})
			}
			if len(buf) > 0 {
				flush([]string{strings.TrimRight(string(buf), "\r")})
			}
			return
		}
	}
}
