package auth

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestUI_HTMX_EntryAndLoginFlow(t *testing.T) {
	baseURL, adminToken, _ := startServer(t)
	client := &http.Client{Timeout: 3 * time.Second}

	{
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("期望 GET / 返回 200，实际=%d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		text := string(body)
		if !strings.Contains(text, `id="htmx-root"`) {
			t.Fatalf("首页未返回 HTMX 根节点: %s", text)
		}
		if !strings.Contains(text, `/vendor/htmx.min.js`) || !strings.Contains(text, `/htmx.js`) {
			t.Fatalf("首页未返回 HTMX 脚本依赖: %s", text)
		}
	}

	{
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/ui/page", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /ui/page: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("期望 GET /ui/page 返回 200，实际=%d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), `name="token"`) {
			t.Fatalf("未登录页面未返回 token 登录表单: %s", string(body))
		}
	}

	var sessionCookie *http.Cookie
	{
		form := url.Values{}
		form.Set("token", adminToken)
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/ui/login", strings.NewReader(form.Encode()))
		req.Header.Set("content-type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /ui/login: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望 POST /ui/login 返回 200，实际=%d body=%s", resp.StatusCode, string(body))
		}
		for _, ck := range resp.Cookies() {
			if strings.TrimSpace(ck.Name) == "proxy-pool-token" {
				sessionCookie = ck
				break
			}
		}
		body, _ := io.ReadAll(resp.Body)
		text := string(body)
		if !strings.Contains(text, "退出登录") {
			t.Fatalf("登录后页面未返回管理壳: %s", text)
		}
	}

	if sessionCookie == nil || strings.TrimSpace(sessionCookie.Value) == "" {
		t.Fatal("登录后未写入 proxy-pool-token cookie")
	}

	{
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/ui/tab/instances", nil)
		req.AddCookie(sessionCookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /ui/tab/instances: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望带 cookie 访问 /ui/tab/instances 返回 200，实际=%d body=%s", resp.StatusCode, string(body))
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "创建实例") {
			t.Fatalf("实例页关键文案缺失: %s", string(body))
		}
	}
}

func TestUI_HTMX_TabNeedAuth(t *testing.T) {
	baseURL, _, _ := startServer(t)
	client := &http.Client{Timeout: 3 * time.Second}

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/ui/tab/instances", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /ui/tab/instances: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("期望未授权访问 /ui/tab/instances 返回 401，实际=%d body=%s", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "未登录") {
		t.Fatalf("未授权页面文案不符合预期: %s", string(body))
	}
}
