package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestSettings_HealthCheckConcurrency(t *testing.T) {
	baseURL, adminToken, _ := startServer(t)
	token := loginAndGetToken(t, baseURL, adminToken)
	client := &http.Client{Timeout: 3 * time.Second}

	putSettings := func(payload map[string]any) (int, []byte) {
		body, _ := json.Marshal(payload)
		req, _ := http.NewRequest(http.MethodPut, baseURL+"/api/settings", bytes.NewReader(body))
		req.Header.Set("content-type", "application/json")
		req.Header.Set("authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("PUT /api/settings: %v", err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}

	getConcurrency := func() int {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/settings", nil)
		req.Header.Set("authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/settings: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望 GET /api/settings 200，实际=%d body=%s", resp.StatusCode, string(b))
		}
		var out struct {
			OK       bool `json:"ok"`
			Settings struct {
				HealthCheckConcurrency int `json:"healthCheckConcurrency"`
			} `json:"settings"`
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode settings response: %v", err)
		}
		if !out.OK {
			t.Fatalf("settings 返回 ok=false: %q", out.Error)
		}
		return out.Settings.HealthCheckConcurrency
	}

	defer func() {
		_, _ = putSettings(map[string]any{"healthCheckConcurrency": 2})
	}()

	{
		status, b := putSettings(map[string]any{"healthCheckConcurrency": 3})
		if status != 200 {
			t.Fatalf("期望设置 healthCheckConcurrency=3 成功，实际=%d body=%s", status, string(b))
		}
	}
	if got := getConcurrency(); got != 3 {
		t.Fatalf("期望 settings 持久化 healthCheckConcurrency=3，实际=%d", got)
	}

	{
		status, b := putSettings(map[string]any{"healthCheckConcurrency": 0})
		if status != 400 {
			t.Fatalf("期望 healthCheckConcurrency=0 返回 400，实际=%d body=%s", status, string(b))
		}
	}
	if got := getConcurrency(); got != 3 {
		t.Fatalf("非法值更新后不应改写现值，期望=3 实际=%d", got)
	}
}
