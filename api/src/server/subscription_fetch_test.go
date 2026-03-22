package server

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchAndParseSubscriptionFromURL_PrefersCandidateWithMoreProxies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		flag := strings.TrimSpace(r.URL.Query().Get("flag"))
		switch flag {
		case "meta":
			_, _ = w.Write([]byte(`proxies:
  - name: node-meta-1
    type: ss
    server: 1.1.1.1
    port: 8388
    cipher: aes-128-gcm
    password: p1
  - name: node-meta-2
    type: ss
    server: 2.2.2.2
    port: 8388
    cipher: aes-128-gcm
    password: p2
`))
		default:
			raw := "foo://not-supported\nss://YWVzLTEyOC1nY206cDE=@1.1.1.1:8388#node-raw-1\n"
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(raw))))
		}
	}))
	defer srv.Close()

	yamlText, proxies, effectiveURL, err := fetchAndParseSubscriptionFromURL(srv.URL + "/sub?token=t1")
	if err != nil {
		t.Fatalf("fetchAndParseSubscriptionFromURL 期望成功，实际错误: %v", err)
	}
	if len(proxies) != 2 {
		t.Fatalf("期望选择节点更多的候选（2个），实际=%d", len(proxies))
	}
	if !strings.Contains(effectiveURL, "flag=meta") {
		t.Fatalf("期望优先采用 flag=meta 结果，实际 effectiveURL=%q", effectiveURL)
	}
	if !strings.Contains(yamlText, "node-meta-1") {
		t.Fatalf("期望返回的订阅文本来自 meta 候选，实际内容不匹配")
	}
}
