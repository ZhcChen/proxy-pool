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

const defaultTestAdminToken = "test-admin-token-1234567890"

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

func startServer(t *testing.T) (baseURL, adminToken string, stop func()) {
	t.Helper()

	root := repoRoot(t)
	port := freePort(t)
	dataDir := t.TempDir()

	return startServerWithDataDir(t, root, port, dataDir, defaultTestAdminToken)
}

func startServerWithDataDir(t *testing.T, root string, port int, dataDir string, adminToken string) (baseURL, token string, stop func()) {
	t.Helper()
	return startServerWithDataDirAndTokens(t, root, port, dataDir, adminToken, "")
}

func startServerWithDataDirAndTokens(
	t *testing.T,
	root string,
	port int,
	dataDir string,
	adminToken string,
	openapiToken string,
) (baseURL, token string, stop func()) {
	t.Helper()
	if strings.TrimSpace(adminToken) == "" {
		t.Fatal("adminToken 不能为空")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	cmd := exec.CommandContext(ctx, "bun", "--cwd=api", "src/index.ts")
	cmd.Dir = root
	env := append(os.Environ(),
		"HOST=127.0.0.1",
		fmt.Sprintf("PORT=%d", port),
		fmt.Sprintf("DATA_DIR=%s", dataDir),
		fmt.Sprintf("WEB_DIR=%s", filepath.Join(root, "web", "public")),
		fmt.Sprintf("ADMIN_TOKEN=%s", adminToken),
		// 避免测试时依赖外网 IP 探测（加快速度并减少不稳定因素）
		"PUBLIC_IP_OVERRIDE=203.0.113.10",
	)
	if strings.TrimSpace(openapiToken) != "" {
		env = append(env, fmt.Sprintf("OPENAPI_TOKEN=%s", openapiToken))
	}
	cmd.Env = env

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

	consume := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			out.add(line)
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

	// 等待服务就绪：静态页无需鉴权
	baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 800 * time.Millisecond}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("服务提前退出: %v\n输出:\n%s", err, out.snapshot())
		default:
		}
		req, _ := http.NewRequest("GET", baseURL+"/", nil)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return baseURL, adminToken, stop
			}
		}
		time.Sleep(120 * time.Millisecond)
	}

	t.Fatalf("服务未就绪\n输出:\n%s", out.snapshot())
	return "", "", stop
}

func TestAuth_LoginFlow(t *testing.T) {
	baseURL, adminToken, _ := startServer(t)

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

	// 错误 token 登录应失败
	{
		body, _ := json.Marshal(map[string]string{
			"token": adminToken + "x",
		})
		req, _ := http.NewRequest("POST", baseURL+"/api/login", bytes.NewReader(body))
		req.Header.Set("content-type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/login: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("期望错误 token 401，实际=%d", resp.StatusCode)
		}
	}

	// 正确 token 登录拿 token
	var token string
	{
		body, _ := json.Marshal(map[string]string{
			"token": adminToken,
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
		if lr.Token != adminToken {
			t.Fatalf("期望返回 token 与 ADMIN_TOKEN 一致，got=%q want=%q", lr.Token, adminToken)
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

func TestAuth_TokenControlledByEnv(t *testing.T) {
	root := repoRoot(t)
	dataDir := t.TempDir()

	tokenA := "test-admin-token-A-123456"
	baseURL1, _, stop1 := startServerWithDataDir(t, root, freePort(t), dataDir, tokenA)

	client := &http.Client{Timeout: 2 * time.Second}
	login := func(baseURL, token string) int {
		body, _ := json.Marshal(map[string]string{"token": token})
		req, _ := http.NewRequest("POST", baseURL+"/api/login", bytes.NewReader(body))
		req.Header.Set("content-type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/login: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := login(baseURL1, tokenA); got != 200 {
		t.Fatalf("期望 tokenA 登录 200，实际=%d", got)
	}
	stop1()

	tokenB := "test-admin-token-B-654321"
	baseURL2, _, _ := startServerWithDataDir(t, root, freePort(t), dataDir, tokenB)

	if got := login(baseURL2, tokenA); got != 401 {
		t.Fatalf("期望旧 tokenA 在重启后 401，实际=%d", got)
	}
	if got := login(baseURL2, tokenB); got != 200 {
		t.Fatalf("期望新 tokenB 登录 200，实际=%d", got)
	}
}
