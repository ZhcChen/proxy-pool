package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

type settingsResponse struct {
	OK       bool `json:"ok"`
	Settings struct {
		ProxyAuth struct {
			Enabled  bool   `json:"enabled"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"proxyAuth"`
	} `json:"settings"`
	Error string `json:"error"`
}

type resetProxyAuthResponse struct {
	OK        bool `json:"ok"`
	ProxyAuth struct {
		Enabled  bool   `json:"enabled"`
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"proxyAuth"`
	Error string `json:"error"`
}

func loginAndGetToken(t *testing.T, baseURL, adminToken string) string {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
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
	return lr.Token
}

func TestSettings_ProxyAuth_ResetAndToggle(t *testing.T) {
	baseURL, adminToken, _ := startServer(t)
	token := loginAndGetToken(t, baseURL, adminToken)

	client := &http.Client{Timeout: 2 * time.Second}

	getSettings := func() settingsResponse {
		req, _ := http.NewRequest("GET", baseURL+"/api/settings", nil)
		req.Header.Set("authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/settings: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望 settings 200，实际=%d body=%s", resp.StatusCode, string(b))
		}
		var sr settingsResponse
		if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
			t.Fatalf("decode settings response: %v", err)
		}
		if !sr.OK {
			t.Fatalf("settings 返回 ok=false: error=%q", sr.Error)
		}
		return sr
	}

	// 初始应包含 proxyAuth（默认 enabled=false）
	s1 := getSettings()
	if s1.Settings.ProxyAuth.Username == "" || s1.Settings.ProxyAuth.Password == "" {
		t.Fatalf("proxyAuth 凭据缺失：user=%q pass=%q", s1.Settings.ProxyAuth.Username, s1.Settings.ProxyAuth.Password)
	}
	if len(s1.Settings.ProxyAuth.Password) != 24 {
		t.Fatalf("期望 proxyAuth 密码长度为 24，实际=%d（%q）", len(s1.Settings.ProxyAuth.Password), s1.Settings.ProxyAuth.Password)
	}

	// 启用认证
	{
		body, _ := json.Marshal(map[string]any{
			"proxyAuth": map[string]any{
				"enabled": true,
			},
		})
		req, _ := http.NewRequest("PUT", baseURL+"/api/settings", bytes.NewReader(body))
		req.Header.Set("content-type", "application/json")
		req.Header.Set("authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("PUT /api/settings: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("期望启用认证 200，实际=%d", resp.StatusCode)
		}
	}

	// 重置凭据：应保留 enabled=true，同时用户名/密码应变化
	{
		req, _ := http.NewRequest("POST", baseURL+"/api/settings/reset-proxy-auth", bytes.NewReader([]byte("{}")))
		req.Header.Set("content-type", "application/json")
		req.Header.Set("authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/settings/reset-proxy-auth: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望 reset 200，实际=%d body=%s", resp.StatusCode, string(b))
		}
		var rr resetProxyAuthResponse
		if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
			t.Fatalf("decode reset response: %v", err)
		}
		if !rr.OK {
			t.Fatalf("reset 返回 ok=false: error=%q", rr.Error)
		}
		if !rr.ProxyAuth.Enabled {
			t.Fatalf("期望 reset 保留 enabled=true，实际=false")
		}
		if rr.ProxyAuth.Username == "" || rr.ProxyAuth.Password == "" {
			t.Fatalf("reset 返回凭据缺失：user=%q pass=%q", rr.ProxyAuth.Username, rr.ProxyAuth.Password)
		}
		if rr.ProxyAuth.Username == s1.Settings.ProxyAuth.Username || rr.ProxyAuth.Password == s1.Settings.ProxyAuth.Password {
			t.Fatalf("reset 后凭据未变化：before=(%s,%s) after=(%s,%s)", s1.Settings.ProxyAuth.Username, s1.Settings.ProxyAuth.Password, rr.ProxyAuth.Username, rr.ProxyAuth.Password)
		}
		if len(rr.ProxyAuth.Password) != 24 {
			t.Fatalf("期望 reset 后密码长度为 24，实际=%d（%q）", len(rr.ProxyAuth.Password), rr.ProxyAuth.Password)
		}
	}

	// 再次读取 settings，enabled 仍应为 true
	s2 := getSettings()
	if !s2.Settings.ProxyAuth.Enabled {
		t.Fatalf("期望 settings 里 enabled=true，实际=false")
	}
}
