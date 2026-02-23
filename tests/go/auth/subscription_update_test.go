package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubscriptions_UpdateFromListStyle(t *testing.T) {
	baseURL, adminToken, _ := startServer(t)
	token := loginAndGetToken(t, baseURL, adminToken)
	client := &http.Client{Timeout: 2 * time.Second}

	// 先创建一个手动 YAML 订阅
	var subID string
	{
		yaml := "proxies:\n  - name: init-node\n    type: socks5\n    server: 1.1.1.1\n    port: 1080\n"
		body, _ := json.Marshal(map[string]any{
			"name": "sub-to-edit",
			"yaml": yaml,
		})
		req, _ := http.NewRequest("POST", baseURL+"/api/subscriptions", bytes.NewReader(body))
		req.Header.Set("content-type", "application/json")
		req.Header.Set("authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/subscriptions: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("期望创建订阅 200，实际=%d body=%s", resp.StatusCode, string(b))
		}
		var out struct {
			OK           bool `json:"ok"`
			Subscription struct {
				ID string `json:"id"`
			} `json:"subscription"`
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode create response: %v", err)
		}
		if !out.OK || out.Subscription.ID == "" {
			t.Fatalf("创建订阅失败：ok=%v id=%q error=%q", out.OK, out.Subscription.ID, out.Error)
		}
		subID = out.Subscription.ID
	}

	// 模拟机场：默认返回 base64 URI，flag=clash-meta 返回提示节点，flag=meta 返回真实 YAML
	var noFlagCount int32
	var clashMetaCount int32
	var metaCount int32
	mockSub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		flag := r.URL.Query().Get("flag")
		if flag == "meta" {
			atomic.AddInt32(&metaCount, 1)
			_, _ = w.Write([]byte(`
proxies:
  - name: edited-node
    type: ss
    server: 2.2.2.2
    port: 443
    cipher: aes-128-gcm
    password: pass
`))
			return
		}
		if flag == "clash-meta" || flag == "clash" {
			atomic.AddInt32(&clashMetaCount, 1)
			_, _ = w.Write([]byte(`
proxies:
  - name: '⚠️ 如果现在只能看到少数线路'
    type: ss
    server: 1.1.1.1
    port: 8888
    cipher: aes-128-gcm
    password: pass
  - name: '⚠️ 立即更新教程推荐最新软件'
    type: ss
    server: 1.1.1.1
    port: 8888
    cipher: aes-128-gcm
    password: pass
`))
			return
		}
		atomic.AddInt32(&noFlagCount, 1)
		uriLines := "ss://YWVzLTEyOC1nY206cGFzc0AyLjIuMi4yOjQ0Mw==#edited\n"
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(uriLines))))
	}))
	defer mockSub.Close()

	// 模拟前端在订阅列表里直接更新（改名称+URL）
	{
		body, _ := json.Marshal(map[string]any{
			"name": "sub-edited",
			"url":  mockSub.URL + "/sub?token=t1",
		})
		req, _ := http.NewRequest("PUT", baseURL+"/api/subscriptions/"+subID, bytes.NewReader(body))
		req.Header.Set("content-type", "application/json")
		req.Header.Set("authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("PUT /api/subscriptions/:id: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			if strings.Contains(strings.ToLower(string(b)), "connection refused") {
				t.Skipf("当前测试环境服务无法回连本地 mock 订阅服务，跳过该用例：%s", string(b))
			}
			t.Fatalf("期望更新订阅 200，实际=%d body=%s", resp.StatusCode, string(b))
		}
		var out struct {
			OK           bool `json:"ok"`
			Subscription struct {
				Name    string           `json:"name"`
				URL     string           `json:"url"`
				LastErr *string          `json:"lastError"`
				Proxies []map[string]any `json:"proxies"`
			} `json:"subscription"`
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode update response: %v", err)
		}
		if !out.OK {
			t.Fatalf("更新订阅返回 ok=false: error=%q", out.Error)
		}
		if out.Subscription.Name != "sub-edited" {
			t.Fatalf("名称未更新：got=%q", out.Subscription.Name)
		}
		if len(out.Subscription.Proxies) == 0 {
			t.Fatalf("更新后 proxies 为空")
		}
		if out.Subscription.LastErr != nil && *out.Subscription.LastErr != "" {
			t.Fatalf("更新后 lastError 不为空：%q", *out.Subscription.LastErr)
		}

		parsedURL, err := url.Parse(out.Subscription.URL)
		if err != nil {
			t.Fatalf("更新后的 URL 不是合法 URL: %v", err)
		}
		if parsedURL.Query().Get("flag") != "meta" {
			t.Fatalf("期望更新后 URL 自动补 flag=meta，实际=%q", out.Subscription.URL)
		}
	}

	if got := atomic.LoadInt32(&noFlagCount); got != 1 {
		t.Fatalf("期望未带 flag 的探测请求 1 次，实际=%d", got)
	}
	if got := atomic.LoadInt32(&clashMetaCount); got != 1 {
		t.Fatalf("期望 flag=clash-meta 的探测请求 1 次，实际=%d", got)
	}
	if got := atomic.LoadInt32(&metaCount); got != 1 {
		t.Fatalf("期望 flag=meta 的请求 1 次，实际=%d", got)
	}
}
