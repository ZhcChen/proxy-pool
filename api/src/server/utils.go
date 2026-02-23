package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func boolPtr(v bool) *bool {
	return &v
}

func strPtr(v string) *string {
	return &v
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func isPortFree(port int, host string) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func findNextFreePort(start int, reserved map[int]struct{}, host string) (int, error) {
	p := start
	for i := 0; i < 2000; i++ {
		if _, ok := reserved[p]; ok {
			p++
			continue
		}
		if isPortFree(p, host) {
			return p, nil
		}
		p++
	}
	return 0, fmt.Errorf("找不到可用端口（扫描范围过大或端口被占用）")
}

func randomString(chars string, n int) string {
	if n <= 0 {
		return ""
	}
	if chars == "" {
		chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = chars[i%len(chars)]
		}
	} else {
		for i := range b {
			b[i] = chars[int(b[i])%len(chars)]
		}
	}
	return string(b)
}

func generateProxyAuth() ProxyAuth {
	lenBytes := make([]byte, 1)
	_, _ = rand.Read(lenBytes)
	usernameLen := 8 + int(lenBytes[0]%5)
	usernameChars := "abcdefghijklmnopqrstuvwxyz0123456789"
	passwordChars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	return ProxyAuth{
		Enabled:  false,
		Username: randomString(usernameChars, usernameLen),
		Password: randomString(passwordChars, 24),
	}
}

func normalizeIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if ip := net.ParseIP(raw); ip != nil {
		return raw
	}
	return ""
}

func normalizeHostInput(raw string) (string, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", true
	}
	if strings.Contains(v, "://") || strings.ContainsAny(v, "/ ") {
		return "", false
	}
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		v = strings.TrimSpace(v[1 : len(v)-1])
	}
	if ip := net.ParseIP(v); ip != nil {
		return v, true
	}
	if strings.Contains(v, ":") {
		return "", false
	}
	host := strings.ToLower(v)
	if host == "localhost" {
		return host, true
	}
	if len(host) > 253 {
		return "", false
	}
	labels := strings.Split(host, ".")
	for _, l := range labels {
		if l == "" {
			return "", false
		}
		if len(l) > 63 {
			return "", false
		}
		for i, ch := range l {
			if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
				return "", false
			}
			if ch == '-' && (i == 0 || i == len(l)-1) {
				return "", false
			}
		}
	}
	return host, true
}

func withSubscriptionFlag(urlStr, flag string) string {
	u, err := http.NewRequest(http.MethodGet, urlStr, nil)
	if err != nil || u.URL == nil {
		return ""
	}
	q := u.URL.Query()
	q.Set("flag", flag)
	u.URL.RawQuery = q.Encode()
	return u.URL.String()
}

func hostWithIPv6Bracket(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return host
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]"
	}
	return host
}

func readEmbedFallback(path string) ([]byte, error) {
	return os.ReadFile(filepath.Clean(path))
}

func decodeBase64Text(raw string) string {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return string(b)
}
