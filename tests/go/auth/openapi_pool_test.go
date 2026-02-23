package auth

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type openapiPoolResponse struct {
	OK      bool             `json:"ok"`
	Proxies []map[string]any `json:"proxies"`
	Error   string           `json:"error"`
}

func TestOpenAPI_DisabledWithoutToken(t *testing.T) {
	baseURL, adminToken, _ := startServer(t)
	client := &http.Client{Timeout: 2 * time.Second}

	req, _ := http.NewRequest("GET", baseURL+"/openapi/pool", nil)
	req.Header.Set("authorization", "Bearer "+adminToken)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /openapi/pool: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("期望未配置 OPENAPI_TOKEN 时返回 503，实际=%d body=%s", resp.StatusCode, string(b))
	}

	var out openapiPoolResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode openapi disabled response: %v", err)
	}
	if out.OK {
		t.Fatalf("期望 ok=false，实际 ok=true")
	}
	if !strings.Contains(out.Error, "OPENAPI_TOKEN") {
		t.Fatalf("期望错误信息包含 OPENAPI_TOKEN，实际=%q", out.Error)
	}
}

func TestOpenAPI_PoolUsesSeparateToken(t *testing.T) {
	root := repoRoot(t)
	port := freePort(t)
	dataDir := t.TempDir()
	adminToken := "test-admin-token-aaaa1111"
	openapiToken := "test-openapi-token-bbbb2222"
	baseURL, _, _ := startServerWithDataDirAndTokens(t, root, port, dataDir, adminToken, openapiToken)
	client := &http.Client{Timeout: 2 * time.Second}

	// 未带 token 应 401
	{
		req, _ := http.NewRequest("GET", baseURL+"/openapi/pool", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /openapi/pool without token: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("期望未授权 401，实际=%d", resp.StatusCode)
		}
	}

	// 管理端 token 不应访问 openapi（必须独立 token）
	{
		req, _ := http.NewRequest("GET", baseURL+"/openapi/pool", nil)
		req.Header.Set("authorization", "Bearer "+adminToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /openapi/pool with admin token: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("期望 admin token 访问 openapi 返回 401，实际=%d", resp.StatusCode)
		}
	}

	// openapi token 可访问实例池列表
	{
		req, _ := http.NewRequest("GET", baseURL+"/openapi/pool", nil)
		req.Header.Set("authorization", "Bearer "+openapiToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /openapi/pool with openapi token: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望 openapi token 访问 200，实际=%d body=%s", resp.StatusCode, string(b))
		}
		var out openapiPoolResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode /openapi/pool response: %v", err)
		}
		if !out.OK {
			t.Fatalf("期望 ok=true，实际 error=%q", out.Error)
		}
		if out.Proxies == nil {
			t.Fatalf("期望 proxies 字段存在，实际为 nil")
		}
	}
}
