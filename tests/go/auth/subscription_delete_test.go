package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type subscriptionDTO struct {
	ID string `json:"id"`
}

func TestSubscriptions_Delete(t *testing.T) {
	root := repoRoot(t)
	port := freePort(t)
	dataDir := t.TempDir()

	baseURL, adminToken, _ := startServerWithDataDir(t, root, port, dataDir, defaultTestAdminToken)
	token := loginAndGetToken(t, baseURL, adminToken)

	client := &http.Client{Timeout: 2 * time.Second}

	// 添加订阅（YAML 快照）
	var subID string
	{
		yaml := "proxies:\n  - name: test-1\n    type: socks5\n    server: 1.1.1.1\n    port: 1080\n"
		body, _ := json.Marshal(map[string]any{
			"name": "test-sub",
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
			t.Fatalf("期望添加订阅 200，实际=%d body=%s", resp.StatusCode, string(b))
		}
		var out struct {
			OK           bool            `json:"ok"`
			Subscription subscriptionDTO `json:"subscription"`
			Error        string          `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode subscriptions response: %v", err)
		}
		if !out.OK || out.Subscription.ID == "" {
			t.Fatalf("添加订阅返回异常: ok=%v id=%q error=%q", out.OK, out.Subscription.ID, out.Error)
		}
		subID = out.Subscription.ID
	}

	// 订阅 YAML 文件应存在
	yamlPath := filepath.Join(dataDir, "subscriptions", subID+".yaml")
	if _, err := os.Stat(yamlPath); err != nil {
		t.Fatalf("期望订阅 yaml 文件存在: %s err=%v", yamlPath, err)
	}

	// 删除订阅
	{
		req, _ := http.NewRequest("DELETE", baseURL+"/api/subscriptions/"+subID, nil)
		req.Header.Set("authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("DELETE /api/subscriptions/:id: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("期望删除订阅 200，实际=%d", resp.StatusCode)
		}
	}

	// 文件应被清理
	if _, err := os.Stat(yamlPath); err == nil {
		t.Fatalf("期望订阅 yaml 文件被删除，但仍存在: %s", yamlPath)
	} else if !os.IsNotExist(err) {
		t.Fatalf("检查订阅 yaml 文件失败: %v", err)
	}
}
