package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type loginResponse struct {
	OK       bool   `json:"ok"`
	Token    string `json:"token"`
	TokenKey string `json:"tokenKey"`
	Error    string `json:"error"`
}

const (
	defaultTestBaseURL      = "http://127.0.0.1:3320"
	defaultTestAdminToken   = "e10344ce0619d09572d1154954996794173b79ede364059757f6153daf7a1dee"
	defaultTestOpenAPIToken = "91f59948e37ebd0e51d54630f94668e3fd0c5beced19c0114c44a3f7676f85e3"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "docker-compose.yml")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("无法定位仓库根目录（未找到 docker-compose.yml）")
	return ""
}

func testBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("TEST_BASE_URL")); v != "" {
		return v
	}
	return defaultTestBaseURL
}

func testAdminToken() string {
	if v := strings.TrimSpace(os.Getenv("TEST_ADMIN_TOKEN")); v != "" {
		return v
	}
	return defaultTestAdminToken
}

func testOpenAPIToken() string {
	if v := strings.TrimSpace(os.Getenv("TEST_OPENAPI_TOKEN")); v != "" {
		return v
	}
	return defaultTestOpenAPIToken
}

func waitForServiceReady(t *testing.T, baseURL string) {
	t.Helper()
	client := &http.Client{Timeout: 800 * time.Millisecond}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", baseURL+"/", nil)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(120 * time.Millisecond)
	}
	t.Fatalf("服务未就绪，请先执行 docker compose up -d --build（baseURL=%s）", baseURL)
}

func startServer(t *testing.T) (baseURL, adminToken string, stop func()) {
	t.Helper()
	baseURL = testBaseURL()
	adminToken = testAdminToken()
	waitForServiceReady(t, baseURL)
	return baseURL, adminToken, func() {}
}

func startServerWithDataDir(t *testing.T, _ string, _ int, _ string, adminToken string) (baseURL, token string, stop func()) {
	t.Helper()
	baseURL, fallbackToken, stop := startServer(t)
	token = strings.TrimSpace(adminToken)
	if token == "" {
		token = fallbackToken
	}
	return baseURL, token, stop
}

func startServerWithDataDirAndTokens(
	t *testing.T,
	_ string,
	_ int,
	_ string,
	adminToken string,
	_ string,
) (baseURL, token string, stop func()) {
	t.Helper()
	return startServerWithDataDir(t, "", 0, "", adminToken)
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
	baseURL, adminToken, _ := startServer(t)

	client := &http.Client{Timeout: 2 * time.Second}
	login := func(token string) int {
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

	if got := login(adminToken); got != 200 {
		t.Fatalf("期望 TEST_ADMIN_TOKEN 登录 200，实际=%d", got)
	}
	if got := login(adminToken + "-wrong"); got != 401 {
		t.Fatalf("期望错误 token 401，实际=%d", got)
	}
}
