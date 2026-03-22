package server

import (
	"encoding/base64"
	"fmt"
	"testing"
)

func TestParseSubscriptionYAML_Base64URIList(t *testing.T) {
	ssCredential := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:password123"))
	uriList := fmt.Sprintf(
		"vless://11111111-1111-1111-1111-111111111111@example.com:443?type=grpc&security=reality&pbk=pub-key-1&sid=abcd1234&sni=www.microsoft.com&serviceName=update&fp=chrome#US-1\n"+
			"hysteria2://hy2pass@example.net:8443/?sni=example.net&obfs=salamander&obfs-password=obf123#HY2-1\n"+
			"tuic://22222222-2222-2222-2222-222222222222:tuicpass@tuic.example.org:443?congestion_control=bbr&udp_relay_mode=native&alpn=h3&sni=tuic.example.org&insecure=1#TUIC-1\n"+
			"ss://%s@1.2.3.4:8388#SS-1\n",
		ssCredential,
	)
	raw := base64.StdEncoding.EncodeToString([]byte(uriList))

	proxies, err := parseSubscriptionYAML(raw)
	if err != nil {
		t.Fatalf("期望可解析 base64 URI 列表，实际报错: %v", err)
	}
	if len(proxies) != 4 {
		t.Fatalf("期望解析出 4 个节点，实际=%d", len(proxies))
	}

	byName := map[string]MihomoProxy{}
	for _, p := range proxies {
		name, _ := p["name"].(string)
		byName[name] = p
	}

	{
		p := byName["US-1"]
		if p == nil {
			t.Fatal("缺少节点 US-1")
		}
		if got, _ := p["type"].(string); got != "vless" {
			t.Fatalf("US-1 type 期望=vless 实际=%q", got)
		}
		if got, _ := p["network"].(string); got != "grpc" {
			t.Fatalf("US-1 network 期望=grpc 实际=%q", got)
		}
		if got, _ := p["server"].(string); got != "example.com" {
			t.Fatalf("US-1 server 期望=example.com 实际=%q", got)
		}
		if got := anyToInt(p["port"]); got != 443 {
			t.Fatalf("US-1 port 期望=443 实际=%d", got)
		}
		if got, _ := p["tls"].(bool); !got {
			t.Fatal("US-1 期望 tls=true")
		}
		reality, _ := p["reality-opts"].(map[string]any)
		if got, _ := reality["public-key"].(string); got != "pub-key-1" {
			t.Fatalf("US-1 reality public-key 期望=pub-key-1 实际=%q", got)
		}
	}

	{
		p := byName["HY2-1"]
		if p == nil {
			t.Fatal("缺少节点 HY2-1")
		}
		if got, _ := p["type"].(string); got != "hysteria2" {
			t.Fatalf("HY2-1 type 期望=hysteria2 实际=%q", got)
		}
		if got, _ := p["obfs"].(string); got != "salamander" {
			t.Fatalf("HY2-1 obfs 期望=salamander 实际=%q", got)
		}
	}

	{
		p := byName["TUIC-1"]
		if p == nil {
			t.Fatal("缺少节点 TUIC-1")
		}
		if got, _ := p["type"].(string); got != "tuic" {
			t.Fatalf("TUIC-1 type 期望=tuic 实际=%q", got)
		}
		if got, _ := p["congestion-controller"].(string); got != "bbr" {
			t.Fatalf("TUIC-1 congestion-controller 期望=bbr 实际=%q", got)
		}
		if got, _ := p["skip-cert-verify"].(bool); !got {
			t.Fatal("TUIC-1 期望 skip-cert-verify=true")
		}
	}

	{
		p := byName["SS-1"]
		if p == nil {
			t.Fatal("缺少节点 SS-1")
		}
		if got, _ := p["type"].(string); got != "ss" {
			t.Fatalf("SS-1 type 期望=ss 实际=%q", got)
		}
		if got, _ := p["cipher"].(string); got != "aes-256-gcm" {
			t.Fatalf("SS-1 cipher 期望=aes-256-gcm 实际=%q", got)
		}
		if got, _ := p["password"].(string); got != "password123" {
			t.Fatalf("SS-1 password 期望=password123 实际=%q", got)
		}
	}
}

func TestParseSubscriptionYAML_PlainURIList(t *testing.T) {
	raw := "vless://11111111-1111-1111-1111-111111111111@example.com:443?type=grpc&security=tls&sni=example.com#PLAIN-1\n" +
		"hysteria2://hy2pass@example.net:8443/?sni=example.net#PLAIN-2\n"
	proxies, err := parseSubscriptionYAML(raw)
	if err != nil {
		t.Fatalf("期望可解析 plain URI 列表，实际报错: %v", err)
	}
	if len(proxies) != 2 {
		t.Fatalf("期望解析出 2 个节点，实际=%d", len(proxies))
	}
}

func TestParseSubscriptionYAML_URIListDuplicateNames(t *testing.T) {
	raw := "hysteria2://p1@example.net:8443/?sni=example.net#NODE\n" +
		"hysteria2://p2@example.net:8444/?sni=example.net#NODE\n"
	proxies, err := parseSubscriptionYAML(raw)
	if err != nil {
		t.Fatalf("期望可解析 URI 列表，实际报错: %v", err)
	}
	if len(proxies) != 2 {
		t.Fatalf("期望解析出 2 个节点，实际=%d", len(proxies))
	}
	name1, _ := proxies[0]["name"].(string)
	name2, _ := proxies[1]["name"].(string)
	if name1 == name2 {
		t.Fatalf("期望重复节点名自动去重，实际 name1=%q name2=%q", name1, name2)
	}
	if name1 != "NODE" || name2 != "NODE-2" {
		t.Fatalf("期望名称去重规则 NODE/NODE-2，实际=%q/%q", name1, name2)
	}
}

func TestParseSubscriptionYAML_PlainURIListAnyTLS(t *testing.T) {
	raw := "anytls://passwd-1@edge.example.com:443/?insecure=1&sni=cdn.example.com&alpn=h2,h3&fp=chrome#ANYTLS-1\n"
	proxies, err := parseSubscriptionYAML(raw)
	if err != nil {
		t.Fatalf("期望可解析 anytls URI 列表，实际报错: %v", err)
	}
	if len(proxies) != 1 {
		t.Fatalf("期望解析出 1 个节点，实际=%d", len(proxies))
	}
	p := proxies[0]
	if got, _ := p["name"].(string); got != "ANYTLS-1" {
		t.Fatalf("name 期望=ANYTLS-1 实际=%q", got)
	}
	if got, _ := p["type"].(string); got != "anytls" {
		t.Fatalf("type 期望=anytls 实际=%q", got)
	}
	if got, _ := p["server"].(string); got != "edge.example.com" {
		t.Fatalf("server 期望=edge.example.com 实际=%q", got)
	}
	if got := anyToInt(p["port"]); got != 443 {
		t.Fatalf("port 期望=443 实际=%d", got)
	}
	if got, _ := p["password"].(string); got != "passwd-1" {
		t.Fatalf("password 期望=passwd-1 实际=%q", got)
	}
	if got, _ := p["sni"].(string); got != "cdn.example.com" {
		t.Fatalf("sni 期望=cdn.example.com 实际=%q", got)
	}
	if got, _ := p["skip-cert-verify"].(bool); !got {
		t.Fatalf("skip-cert-verify 期望=true 实际=%v", got)
	}
	if got, _ := p["client-fingerprint"].(string); got != "chrome" {
		t.Fatalf("client-fingerprint 期望=chrome 实际=%q", got)
	}
	alpn, _ := p["alpn"].([]string)
	if len(alpn) != 2 || alpn[0] != "h2" || alpn[1] != "h3" {
		t.Fatalf("alpn 期望=[h2 h3] 实际=%v", alpn)
	}
}

func anyToInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}
