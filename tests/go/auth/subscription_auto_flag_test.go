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

func TestSubscriptions_AutoAppendFlagAndPersist(t *testing.T) {
	var noFlagCount int32
	var clashMetaCount int32
	var metaCount int32

	mockSubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		flag := r.URL.Query().Get("flag")
		if flag == "meta" {
			atomic.AddInt32(&metaCount, 1)
			_, _ = w.Write([]byte(`
proxies:
  - name: mock-node-1
    type: ss
    server: 1.1.1.1
    port: 8888
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
		uriLines := "ss://YWVzLTEyOC1nY206cGFzc0AxLjEuMS4xOjg4ODg=#mock1\nss://YWVzLTEyOC1nY206cGFzczJAMS4xLjEuMjo4ODg4#mock2\n"
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(uriLines))))
	}))
	defer mockSubServer.Close()

	baseURL, adminToken, _ := startServer(t)
	token := loginAndGetToken(t, baseURL, adminToken)
	client := &http.Client{Timeout: 2 * time.Second}

	subURL := mockSubServer.URL + "/sub?token=t123"

	var subID string
	{
		body, _ := json.Marshal(map[string]any{
			"name": "auto-flag-sub",
			"url":  subURL,
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
			if strings.Contains(strings.ToLower(string(b)), "connection refused") {
				t.Skipf("当前测试环境服务无法回连本地 mock 订阅服务，跳过该用例：%s", string(b))
			}
			t.Fatalf("期望创建订阅 200，实际=%d body=%s", resp.StatusCode, string(b))
		}

		var out struct {
			OK           bool `json:"ok"`
			Subscription struct {
				ID      string           `json:"id"`
				URL     string           `json:"url"`
				LastErr *string          `json:"lastError"`
				Proxies []map[string]any `json:"proxies"`
			} `json:"subscription"`
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode create response: %v", err)
		}
		if !out.OK {
			t.Fatalf("创建订阅返回 ok=false: error=%q", out.Error)
		}
		if out.Subscription.ID == "" {
			t.Fatal("创建订阅返回空 id")
		}
		if out.Subscription.LastErr != nil && *out.Subscription.LastErr != "" {
			if strings.Contains(strings.ToLower(*out.Subscription.LastErr), "connection refused") {
				t.Skipf("当前测试环境服务无法回连本地 mock 订阅服务，跳过该用例：%s", *out.Subscription.LastErr)
			}
			t.Fatalf("期望自动附加 flag 后无错误，实际 lastError=%q", *out.Subscription.LastErr)
		}
		if len(out.Subscription.Proxies) == 0 {
			t.Fatal("期望自动附加 flag 后解析到 proxies，实际为空")
		}

		parsedURL, err := url.Parse(out.Subscription.URL)
		if err != nil {
			t.Fatalf("订阅 URL 不是合法 URL: %v", err)
		}
		if parsedURL.Query().Get("flag") != "meta" {
			t.Fatalf("期望订阅 URL 自动补充 flag=meta，实际 url=%q", out.Subscription.URL)
		}
		subID = out.Subscription.ID
	}

	{
		req, _ := http.NewRequest("POST", baseURL+"/api/subscriptions/"+subID+"/refresh", bytes.NewReader([]byte("{}")))
		req.Header.Set("content-type", "application/json")
		req.Header.Set("authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/subscriptions/:id/refresh: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			if strings.Contains(strings.ToLower(string(b)), "connection refused") {
				t.Skipf("当前测试环境服务无法回连本地 mock 订阅服务，跳过该用例：%s", string(b))
			}
			t.Fatalf("期望刷新订阅 200，实际=%d body=%s", resp.StatusCode, string(b))
		}

		var out struct {
			OK           bool `json:"ok"`
			Subscription struct {
				URL     string           `json:"url"`
				Proxies []map[string]any `json:"proxies"`
			} `json:"subscription"`
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode refresh response: %v", err)
		}
		if !out.OK {
			t.Fatalf("刷新订阅返回 ok=false: error=%q", out.Error)
		}
		if len(out.Subscription.Proxies) == 0 {
			t.Fatal("刷新后 proxies 为空")
		}
		parsedURL, err := url.Parse(out.Subscription.URL)
		if err != nil {
			t.Fatalf("刷新返回的订阅 URL 非法: %v", err)
		}
		if parsedURL.Query().Get("flag") != "meta" {
			t.Fatalf("刷新后订阅 URL 的 flag 不是 meta: %q", out.Subscription.URL)
		}
	}

	if got := atomic.LoadInt32(&noFlagCount); got != 1 {
		t.Fatalf("期望未带 flag 的请求只发生 1 次（首次探测），实际=%d", got)
	}
	if got := atomic.LoadInt32(&clashMetaCount); got != 4 {
		t.Fatalf("期望 clash/clash-meta 探测请求共发生 4 次（创建 2 次 + 刷新 2 次），实际=%d", got)
	}
	if got := atomic.LoadInt32(&metaCount); got != 2 {
		t.Fatalf("期望 flag=meta 的请求发生 2 次（创建1次+刷新1次），实际=%d", got)
	}
}
