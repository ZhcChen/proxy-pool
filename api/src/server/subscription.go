package server

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	domainLikePattern = regexp.MustCompile(`(?i)([a-z0-9][a-z0-9-]{1,}\.[a-z0-9.-]{2,})`)
	wordLikePattern   = regexp.MustCompile(`(?i)\b([a-z]{3,})\b`)
)

func looksLikeBase64(text string) bool {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) < 64 {
		return false
	}
	if strings.Contains(trimmed, "\n") {
		return false
	}
	if len(trimmed)%4 != 0 {
		return false
	}
	for _, ch := range trimmed {
		ok := (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '+' || ch == '/' || ch == '='
		if !ok {
			return false
		}
	}
	return true
}

func tryDecodeBase64ToYAML(text string) string {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil {
		return ""
	}
	out := string(decoded)
	if strings.Contains(out, "proxies:") || strings.Contains(out, "proxy-groups:") {
		return out
	}
	return ""
}

func parseSubscriptionYAML(input string) ([]MihomoProxy, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return nil, errors.New("订阅内容为空")
	}
	yamlText := raw
	if looksLikeBase64(raw) {
		if d := tryDecodeBase64ToYAML(raw); d != "" {
			yamlText = d
		}
	}

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		return nil, fmt.Errorf("订阅内容不是有效的 YAML 对象")
	}
	if doc == nil {
		return nil, fmt.Errorf("订阅内容不是有效的 YAML 对象")
	}

	rawProxies, ok := doc["proxies"]
	if !ok {
		return nil, fmt.Errorf("订阅中未找到 proxies 列表（暂不支持 proxy-providers 自动展开）")
	}
	list, ok := rawProxies.([]any)
	if !ok {
		return nil, fmt.Errorf("订阅中未找到 proxies 列表（暂不支持 proxy-providers 自动展开）")
	}

	proxies := make([]MihomoProxy, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		proxies = append(proxies, MihomoProxy(m))
	}
	if len(proxies) == 0 {
		return nil, fmt.Errorf("订阅 proxies 为空或无法解析节点 name")
	}
	return proxies, nil
}

func inferSubscriptionName(urlStr, rawText string, proxies []MihomoProxy) string {
	candidates := make([]string, 0, len(proxies)+8)
	for _, p := range proxies {
		name := strings.TrimSpace(p.Name())
		if name != "" {
			candidates = append(candidates, name)
		}
	}
	candidates = append(candidates, extractNamesFromRawSubscriptionText(rawText)...)

	for _, c := range candidates {
		if name := inferSubscriptionNameFromNode(c); name != "" {
			return name
		}
	}
	if name := inferSubscriptionNameFromURL(urlStr); name != "" {
		return name
	}
	return ""
}

func extractNamesFromRawSubscriptionText(rawText string) []string {
	raw := strings.TrimSpace(rawText)
	if raw == "" {
		return nil
	}
	text := raw
	if decoded := tryDecodeBase64Text(raw); decoded != "" {
		text = decoded
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, "#")
		if idx < 0 || idx+1 >= len(line) {
			continue
		}
		name := strings.TrimSpace(line[idx+1:])
		if decoded, err := url.QueryUnescape(name); err == nil {
			name = strings.TrimSpace(decoded)
		}
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func tryDecodeBase64Text(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.Contains(trimmed, "\n") {
		return ""
	}
	decoders := []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
	}
	for _, fn := range decoders {
		b, err := fn(trimmed)
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(b))
		if s != "" {
			return s
		}
	}
	return ""
}

func inferSubscriptionNameFromNode(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	for _, m := range domainLikePattern.FindAllStringSubmatch(name, -1) {
		if len(m) < 2 {
			continue
		}
		if out := normalizeSubscriptionNameFromDomain(m[1]); out != "" {
			return out
		}
	}
	stopWords := map[string]struct{}{
		"http": {}, "https": {}, "udp": {}, "tcp": {}, "tls": {}, "meta": {}, "clash": {},
		"proxy": {}, "node": {}, "test": {}, "com": {}, "net": {}, "org": {}, "xyz": {}, "cc": {},
	}
	for _, m := range wordLikePattern.FindAllStringSubmatch(name, -1) {
		if len(m) < 2 {
			continue
		}
		word := strings.ToLower(strings.TrimSpace(m[1]))
		if len(word) < 3 {
			continue
		}
		if _, ok := stopWords[word]; ok {
			continue
		}
		return strings.ToUpper(word)
	}
	return ""
}

func inferSubscriptionNameFromURL(urlStr string) string {
	raw := strings.TrimSpace(urlStr)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return ""
	}
	return normalizeSubscriptionNameFromDomain(host)
}

func normalizeSubscriptionNameFromDomain(domain string) string {
	domain = strings.ToLower(strings.Trim(strings.TrimSpace(domain), "."))
	if domain == "" {
		return ""
	}
	parts := strings.Split(domain, ".")
	filtered := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) == 0 {
		return ""
	}
	label := filtered[0]
	if len(filtered) >= 2 {
		label = filtered[len(filtered)-2]
		if len(filtered) >= 3 {
			last := filtered[len(filtered)-1]
			second := filtered[len(filtered)-2]
			shortSecondLevel := map[string]struct{}{
				"co": {}, "com": {}, "net": {}, "org": {}, "gov": {}, "edu": {}, "ac": {},
			}
			if len(last) == 2 {
				if _, ok := shortSecondLevel[second]; ok {
					label = filtered[len(filtered)-3]
				}
			}
		}
	}
	label = strings.Trim(label, "-_")
	if len(label) < 3 {
		return ""
	}
	allDigit := true
	for _, ch := range label {
		if ch < '0' || ch > '9' {
			allDigit = false
		}
		if !(ch == '-' || ch == '_' || (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'z')) {
			return ""
		}
	}
	if allDigit {
		return ""
	}
	return strings.ToUpper(label)
}

func isWarningOnlySubscription(proxies []MihomoProxy) bool {
	if len(proxies) == 0 || len(proxies) > 3 {
		return false
	}
	warningTokens := []string{"⚠️", "只能看到", "少数线路", "更新教程", "推荐最新软件"}
	for _, p := range proxies {
		name := strings.TrimSpace(p.Name())
		if name == "" {
			return false
		}
		matched := false
		for _, t := range warningTokens {
			if strings.Contains(name, t) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
