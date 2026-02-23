package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSubscriptions_AutoInferNameFromContent(t *testing.T) {
	baseURL, adminToken, _ := startServer(t)
	token := loginAndGetToken(t, baseURL, adminToken)
	client := &http.Client{Timeout: 2 * time.Second}

	yaml := `
proxies:
  - name: "🇺🇸 SKYLUMO.CC"
    type: ss
    server: 1.1.1.1
    port: 8888
    cipher: aes-128-gcm
    password: pass
`
	body, _ := json.Marshal(map[string]any{
		"name": "",
		"yaml": yaml,
	})
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/subscriptions", bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/subscriptions: %v", err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("期望自动命名创建订阅 200，实际=%d body=%s", resp.StatusCode, string(b))
	}

	var out struct {
		OK           bool `json:"ok"`
		Subscription struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"subscription"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !out.OK {
		t.Fatalf("创建订阅返回 ok=false: error=%q", out.Error)
	}
	if strings.TrimSpace(out.Subscription.Name) != "SKYLUMO" {
		t.Fatalf("期望自动识别订阅名为 SKYLUMO，实际=%q（url=%q）", out.Subscription.Name, out.Subscription.URL)
	}
}
