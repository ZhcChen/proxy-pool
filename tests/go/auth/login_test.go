package auth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type loginResponse struct {
	OK       bool   `json:"ok"`
	Token    string `json:"token"`
	TokenKey string `json:"tokenKey"`
	Error    string `json:"error"`
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file) // tests/go/auth
	for i := 0; i < 10; i++ {
		// root should contain api/package.json
		if _, err := os.Stat(filepath.Join(dir, "api", "package.json")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("无法定位仓库根目录（未找到 api/package.json）")
	return ""
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

type procOutput struct {
	mu    sync.Mutex
	lines []string
	limit int
}

func newProcOutput(limit int) *procOutput {
	return &procOutput{limit: limit}
}

func (p *procOutput) add(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lines = append(p.lines, line)
	if p.limit > 0 && len(p.lines) > p.limit {
		p.lines = p.lines[len(p.lines)-p.limit:]
	}
}

func (p *procOutput) snapshot() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.lines, "\n")
}

func startServer(t *testing.T) (baseURL, username, password string, stop func()) {
	t.Helper()

	root := repoRoot(t)
	port := freePort(t)
	dataDir := t.TempDir()

	return startServerWithDataDir(t, root, port, dataDir)
}

func startServerWithDataDir(t *testing.T, root string, port int, dataDir string) (baseURL, username, password string, stop func()) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	cmd := exec.CommandContext(ctx, "bun", "--cwd=api", "src/index.ts")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"HOST=127.0.0.1",
		fmt.Sprintf("PORT=%d", port),
		fmt.Sprintf("DATA_DIR=%s", dataDir),
		fmt.Sprintf("WEB_DIR=%s", filepath.Join(root, "web", "public")),
		// 避免测试时依赖外网 IP 探测（加快速度并减少不稳定因素）
		"PUBLIC_IP_OVERRIDE=203.0.113.10",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatalf("StdoutPipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		t.Fatalf("StderrPipe: %v", err)
	}

	out := newProcOutput(300)

	var mu sync.Mutex
	foundCred := make(chan struct{})
	maybeCloseCred := func() {
		mu.Lock()
		defer mu.Unlock()
		if username != "" && password != "" {
			select {
			case <-foundCred:
			default:
				close(foundCred)
			}
		}
	}
	consume := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			out.add(line)

			if strings.HasPrefix(line, "账号:") {
				mu.Lock()
				username = strings.TrimSpace(strings.TrimPrefix(line, "账号:"))
				mu.Unlock()
				maybeCloseCred()
			}
			if strings.HasPrefix(line, "密码:") {
				mu.Lock()
				password = strings.TrimSpace(strings.TrimPrefix(line, "密码:"))
				mu.Unlock()
				maybeCloseCred()
			}
		}
	}

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("启动 API 失败: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	go consume(stdout)
	go consume(stderr)

	var stopOnce sync.Once
	stop = func() {
		stopOnce.Do(func() {
			cancel()
			if cmd.Process != nil {
				_ = cmd.Process.Signal(syscall.SIGTERM)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
			}
		})
	}
	t.Cleanup(stop)

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("server output:\n%s", out.snapshot())
		}
	})

	// 等待账号/密码输出
	select {
	case <-foundCred:
	case err := <-done:
		t.Fatalf("服务提前退出: %v\n输出:\n%s", err, out.snapshot())
	case <-ctx.Done():
		t.Fatalf("等待登录信息超时\n输出:\n%s", out.snapshot())
	}

	// 等待服务就绪：静态页无需鉴权
	baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 800 * time.Millisecond}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", baseURL+"/", nil)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return baseURL, username, password, stop
			}
		}
		time.Sleep(120 * time.Millisecond)
	}

	t.Fatalf("服务未就绪\n输出:\n%s", out.snapshot())
	return "", "", "", stop
}

func TestAuth_LoginFlow(t *testing.T) {
	baseURL, username, password, _ := startServer(t)
	if len(password) != 20 {
		t.Fatalf("期望密码长度为 20，实际=%d（%q）", len(password), password)
	}
	if username == "" {
		t.Fatal("账号为空")
	}

	client := &http.Client{Timeout: 2 * time.Second}

	// 未登录访问应 401
	{
		req, _ := http.NewRequest("GET", baseURL+"/api/settings", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/settings: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("期望未登录 401，实际=%d", resp.StatusCode)
		}
	}

	// 错误密码登录应失败
	{
		body, _ := json.Marshal(map[string]string{
			"username": username,
			"password": password + "x",
		})
		req, _ := http.NewRequest("POST", baseURL+"/api/login", bytes.NewReader(body))
		req.Header.Set("content-type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/login: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("期望错误密码 401，实际=%d", resp.StatusCode)
		}
	}

	// 正确账号密码登录拿 token
	var token string
	{
		body, _ := json.Marshal(map[string]string{
			"username": username,
			"password": password,
		})
		req, _ := http.NewRequest("POST", baseURL+"/api/login", bytes.NewReader(body))
		req.Header.Set("content-type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/login: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望登录 200，实际=%d body=%s", resp.StatusCode, string(b))
		}
		var lr loginResponse
		if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
			t.Fatalf("decode login response: %v", err)
		}
		if !lr.OK || lr.Token == "" {
			t.Fatalf("登录返回异常: ok=%v token=%q error=%q", lr.OK, lr.Token, lr.Error)
		}
		token = lr.Token
	}

	// 带 token 访问应成功
	{
		req, _ := http.NewRequest("GET", baseURL+"/api/settings", nil)
		req.Header.Set("authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/settings authed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望已登录 200，实际=%d body=%s", resp.StatusCode, string(b))
		}
		var m map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
			t.Fatalf("decode settings response: %v", err)
		}
		if ok, _ := m["ok"].(bool); !ok {
			t.Fatalf("期望 ok=true，实际=%v", m["ok"])
		}
	}

	// 无效 token 应 401
	{
		req, _ := http.NewRequest("GET", baseURL+"/api/settings", nil)
		req.Header.Set("authorization", "Bearer "+"invalid-token")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/settings invalid token: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("期望无效 token 401，实际=%d", resp.StatusCode)
		}
	}
}

func TestAuth_CredentialsPersist(t *testing.T) {
	root := repoRoot(t)
	dataDir := t.TempDir()

	_, user1, pass1, stop1 := startServerWithDataDir(t, root, freePort(t), dataDir)
	stop1()

	_, user2, pass2, _ := startServerWithDataDir(t, root, freePort(t), dataDir)

	if user1 == "" || pass1 == "" {
		t.Fatalf("首次启动未拿到账号密码：user=%q pass=%q", user1, pass1)
	}
	if user1 != user2 || pass1 != pass2 {
		t.Fatalf("账号/密码未持久化：first=(%s,%s) second=(%s,%s)", user1, pass1, user2, pass2)
	}
}
