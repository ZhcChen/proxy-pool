package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
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

	candidates := []string{raw}
	if looksLikeBase64(raw) {
		if d := tryDecodeBase64ToYAML(raw); d != "" {
			candidates = append(candidates, d)
		} else if d := tryDecodeBase64Text(raw); d != "" {
			candidates = append(candidates, d)
		}
	}

	var parseErr error
	for _, text := range candidates {
		proxies, err := parseProxiesFromYAMLObject(text)
		if err == nil {
			return proxies, nil
		}
		if parseErr == nil {
			parseErr = err
		}
	}

	for _, text := range candidates {
		if proxies, ok := parseProxiesFromURIList(text); ok {
			return proxies, nil
		}
	}

	if parseErr != nil {
		return nil, parseErr
	}
	return nil, fmt.Errorf("订阅内容不是有效的 YAML 对象")
}

func parseProxiesFromYAMLObject(text string) ([]MihomoProxy, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
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

func parseProxiesFromURIList(text string) ([]MihomoProxy, bool) {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return nil, false
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 {
		return nil, false
	}

	proxies := make([]MihomoProxy, 0, len(lines))
	nameCounter := map[string]int{}
	for idx, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		proxy, ok := parseProxyFromURI(line, idx+1)
		if !ok {
			continue
		}
		name, _ := proxy["name"].(string)
		proxy["name"] = ensureUniqueProxyName(name, nameCounter)
		proxies = append(proxies, proxy)
	}
	if len(proxies) == 0 {
		return nil, false
	}
	return proxies, true
}

func ensureUniqueProxyName(name string, used map[string]int) string {
	base := strings.TrimSpace(name)
	if base == "" {
		base = "NODE"
	}
	n := used[base]
	used[base] = n + 1
	if n == 0 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, n+1)
}

func parseProxyFromURI(raw string, lineNo int) (MihomoProxy, bool) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || strings.TrimSpace(u.Scheme) == "" {
		return nil, false
	}
	switch strings.ToLower(strings.TrimSpace(u.Scheme)) {
	case "vless":
		return parseVLESSURI(u, lineNo)
	case "hysteria2", "hy2":
		return parseHysteria2URI(u, lineNo)
	case "anytls":
		return parseAnyTLSURI(u, lineNo)
	case "tuic":
		return parseTuicURI(u, lineNo)
	case "ss":
		return parseSSURI(u, raw, lineNo)
	case "trojan":
		return parseTrojanURI(u, lineNo)
	case "vmess":
		return parseVMessURI(raw, lineNo)
	default:
		return nil, false
	}
}

func parseAnyTLSURI(u *url.URL, lineNo int) (MihomoProxy, bool) {
	host, port, ok := parseHostPort(u)
	if !ok {
		return nil, false
	}
	if u.User == nil {
		return nil, false
	}
	password := strings.TrimSpace(u.User.Username())
	if password == "" {
		return nil, false
	}
	name := parseProxyName(u, fmt.Sprintf("ANYTLS-%d", lineNo))
	q := u.Query()
	proxy := MihomoProxy{
		"name":     name,
		"type":     "anytls",
		"server":   host,
		"port":     port,
		"password": password,
		"udp":      true,
	}
	if sni := strings.TrimSpace(q.Get("sni")); sni != "" {
		proxy["sni"] = sni
	}
	if insecure := parseBoolQuery(firstNonEmpty(q.Get("insecure"), q.Get("skip-cert-verify"))); insecure {
		proxy["skip-cert-verify"] = true
	}
	if fp := strings.TrimSpace(firstNonEmpty(q.Get("fp"), q.Get("client-fingerprint"))); fp != "" {
		proxy["client-fingerprint"] = fp
	}
	if alpn := splitCommaValues(q.Get("alpn")); len(alpn) > 0 {
		proxy["alpn"] = alpn
	}
	return proxy, true
}

func parseVLESSURI(u *url.URL, lineNo int) (MihomoProxy, bool) {
	host, port, ok := parseHostPort(u)
	if !ok {
		return nil, false
	}
	if u.User == nil {
		return nil, false
	}
	uuid := strings.TrimSpace(u.User.Username())
	if uuid == "" {
		return nil, false
	}

	name := parseProxyName(u, fmt.Sprintf("VLESS-%d", lineNo))
	q := u.Query()
	network := strings.ToLower(strings.TrimSpace(firstNonEmpty(q.Get("type"), q.Get("network"))))
	security := strings.ToLower(strings.TrimSpace(q.Get("security")))
	flow := strings.TrimSpace(q.Get("flow"))
	sni := strings.TrimSpace(q.Get("sni"))
	clientFP := strings.TrimSpace(q.Get("fp"))

	proxy := MihomoProxy{
		"name":   name,
		"type":   "vless",
		"server": host,
		"port":   port,
		"uuid":   uuid,
		"udp":    true,
	}
	if network != "" {
		proxy["network"] = network
	}
	if security == "tls" || security == "reality" {
		proxy["tls"] = true
	}
	if flow != "" {
		proxy["flow"] = flow
	}
	if sni != "" {
		proxy["servername"] = sni
	}
	if clientFP != "" {
		proxy["client-fingerprint"] = clientFP
	}
	if alpn := splitCommaValues(q.Get("alpn")); len(alpn) > 0 {
		proxy["alpn"] = alpn
	}

	if security == "reality" {
		realityOpts := map[string]any{}
		if pbk := strings.TrimSpace(q.Get("pbk")); pbk != "" {
			realityOpts["public-key"] = pbk
		}
		if sid := strings.TrimSpace(q.Get("sid")); sid != "" {
			realityOpts["short-id"] = sid
		}
		if spiderX := strings.TrimSpace(firstNonEmpty(q.Get("spx"), q.Get("spiderX"))); spiderX != "" {
			realityOpts["spider-x"] = spiderX
		}
		if len(realityOpts) > 0 {
			proxy["reality-opts"] = realityOpts
		}
	}

	if network == "grpc" {
		grpcOpts := map[string]any{}
		if service := strings.TrimSpace(firstNonEmpty(q.Get("serviceName"), q.Get("service-name"))); service != "" {
			grpcOpts["grpc-service-name"] = service
		}
		if mode := strings.TrimSpace(q.Get("mode")); mode != "" {
			grpcOpts["grpc-mode"] = mode
		}
		if len(grpcOpts) > 0 {
			proxy["grpc-opts"] = grpcOpts
		}
	}
	if network == "ws" {
		wsOpts := map[string]any{}
		if path := strings.TrimSpace(q.Get("path")); path != "" {
			wsOpts["path"] = path
		}
		if hostHeader := strings.TrimSpace(q.Get("host")); hostHeader != "" {
			wsOpts["headers"] = map[string]any{"Host": hostHeader}
		}
		if len(wsOpts) > 0 {
			proxy["ws-opts"] = wsOpts
		}
	}
	return proxy, true
}

func parseHysteria2URI(u *url.URL, lineNo int) (MihomoProxy, bool) {
	host, port, ok := parseHostPort(u)
	if !ok {
		return nil, false
	}
	if u.User == nil {
		return nil, false
	}
	password := strings.TrimSpace(u.User.Username())
	if password == "" {
		return nil, false
	}

	name := parseProxyName(u, fmt.Sprintf("HY2-%d", lineNo))
	q := u.Query()
	proxy := MihomoProxy{
		"name":     name,
		"type":     "hysteria2",
		"server":   host,
		"port":     port,
		"password": password,
		"udp":      true,
	}
	if sni := strings.TrimSpace(q.Get("sni")); sni != "" {
		proxy["sni"] = sni
	}
	if insecure := parseBoolQuery(firstNonEmpty(q.Get("insecure"), q.Get("skip-cert-verify"))); insecure {
		proxy["skip-cert-verify"] = true
	}
	if obfs := strings.TrimSpace(q.Get("obfs")); obfs != "" {
		proxy["obfs"] = obfs
	}
	if obfsPass := strings.TrimSpace(q.Get("obfs-password")); obfsPass != "" {
		proxy["obfs-password"] = obfsPass
	}
	if alpn := splitCommaValues(q.Get("alpn")); len(alpn) > 0 {
		proxy["alpn"] = alpn
	}
	if up := strings.TrimSpace(q.Get("up")); up != "" {
		proxy["up"] = up
	}
	if down := strings.TrimSpace(q.Get("down")); down != "" {
		proxy["down"] = down
	}
	return proxy, true
}

func parseTuicURI(u *url.URL, lineNo int) (MihomoProxy, bool) {
	host, port, ok := parseHostPort(u)
	if !ok {
		return nil, false
	}
	if u.User == nil {
		return nil, false
	}
	uuid := strings.TrimSpace(u.User.Username())
	password, hasPassword := u.User.Password()
	password = strings.TrimSpace(password)
	if uuid == "" || !hasPassword || password == "" {
		return nil, false
	}

	name := parseProxyName(u, fmt.Sprintf("TUIC-%d", lineNo))
	q := u.Query()
	proxy := MihomoProxy{
		"name":     name,
		"type":     "tuic",
		"server":   host,
		"port":     port,
		"uuid":     uuid,
		"password": password,
		"udp":      true,
	}
	if sni := strings.TrimSpace(q.Get("sni")); sni != "" {
		proxy["sni"] = sni
	}
	if insecure := parseBoolQuery(firstNonEmpty(q.Get("insecure"), q.Get("skip-cert-verify"))); insecure {
		proxy["skip-cert-verify"] = true
	}
	if cc := strings.TrimSpace(firstNonEmpty(q.Get("congestion_control"), q.Get("congestion-controller"))); cc != "" {
		proxy["congestion-controller"] = cc
	}
	if mode := strings.TrimSpace(firstNonEmpty(q.Get("udp_relay_mode"), q.Get("udp-relay-mode"))); mode != "" {
		proxy["udp-relay-mode"] = mode
	}
	if alpn := splitCommaValues(q.Get("alpn")); len(alpn) > 0 {
		proxy["alpn"] = alpn
	}
	return proxy, true
}

func parseSSURI(u *url.URL, raw string, lineNo int) (MihomoProxy, bool) {
	host, port, ok := parseHostPort(u)
	if !ok {
		return nil, false
	}
	credential := ""
	if u.User != nil {
		credential = strings.TrimSpace(u.User.Username())
		if pw, has := u.User.Password(); has {
			credential = credential + ":" + strings.TrimSpace(pw)
		}
	}
	if credential == "" {
		rawBody := strings.TrimSpace(strings.TrimPrefix(raw, "ss://"))
		if at := strings.Index(rawBody, "@"); at > 0 {
			credential = rawBody[:at]
		} else {
			credential = rawBody
		}
		if sep := strings.IndexAny(credential, "/?#"); sep > 0 {
			credential = credential[:sep]
		}
	}
	cipher, password := parseSSCredential(credential)
	if cipher == "" || password == "" {
		return nil, false
	}

	name := parseProxyName(u, fmt.Sprintf("SS-%d", lineNo))
	proxy := MihomoProxy{
		"name":     name,
		"type":     "ss",
		"server":   host,
		"port":     port,
		"cipher":   cipher,
		"password": password,
		"udp":      true,
	}
	return proxy, true
}

func parseTrojanURI(u *url.URL, lineNo int) (MihomoProxy, bool) {
	host, port, ok := parseHostPort(u)
	if !ok {
		return nil, false
	}
	if u.User == nil {
		return nil, false
	}
	password := strings.TrimSpace(u.User.Username())
	if password == "" {
		return nil, false
	}
	name := parseProxyName(u, fmt.Sprintf("TROJAN-%d", lineNo))
	q := u.Query()
	proxy := MihomoProxy{
		"name":     name,
		"type":     "trojan",
		"server":   host,
		"port":     port,
		"password": password,
		"udp":      true,
	}
	if sni := strings.TrimSpace(q.Get("sni")); sni != "" {
		proxy["sni"] = sni
	}
	if insecure := parseBoolQuery(firstNonEmpty(q.Get("insecure"), q.Get("skip-cert-verify"))); insecure {
		proxy["skip-cert-verify"] = true
	}
	return proxy, true
}

func parseVMessURI(raw string, lineNo int) (MihomoProxy, bool) {
	payload := strings.TrimSpace(strings.TrimPrefix(raw, "vmess://"))
	decoded := tryDecodeBase64Text(payload)
	if decoded == "" {
		return nil, false
	}
	var item map[string]any
	if err := json.Unmarshal([]byte(decoded), &item); err != nil {
		return nil, false
	}
	server := strings.TrimSpace(asStringAny(item["add"]))
	uuid := strings.TrimSpace(asStringAny(item["id"]))
	port, ok := parseIntPort(asStringAny(item["port"]))
	if !ok || server == "" || uuid == "" {
		return nil, false
	}
	name := strings.TrimSpace(asStringAny(item["ps"]))
	if name == "" {
		name = fmt.Sprintf("VMESS-%d", lineNo)
	}
	proxy := MihomoProxy{
		"name":   name,
		"type":   "vmess",
		"server": server,
		"port":   port,
		"uuid":   uuid,
		"udp":    true,
	}
	if cipher := strings.TrimSpace(asStringAny(item["scy"])); cipher != "" {
		proxy["cipher"] = cipher
	}
	if network := strings.ToLower(strings.TrimSpace(asStringAny(item["net"]))); network != "" {
		proxy["network"] = network
	}
	if tls := strings.ToLower(strings.TrimSpace(asStringAny(item["tls"]))); tls != "" && tls != "none" {
		proxy["tls"] = true
	}
	if sni := strings.TrimSpace(asStringAny(item["sni"])); sni != "" {
		proxy["servername"] = sni
	}
	if fp := strings.TrimSpace(asStringAny(item["fp"])); fp != "" {
		proxy["client-fingerprint"] = fp
	}
	if alpn := splitCommaValues(asStringAny(item["alpn"])); len(alpn) > 0 {
		proxy["alpn"] = alpn
	}
	if aid, ok := parseNonNegativeInt(asStringAny(item["aid"])); ok {
		proxy["alterId"] = aid
	}

	if network := strings.ToLower(strings.TrimSpace(asStringAny(item["net"]))); network == "ws" {
		wsOpts := map[string]any{}
		if path := strings.TrimSpace(asStringAny(item["path"])); path != "" {
			wsOpts["path"] = path
		}
		if host := strings.TrimSpace(asStringAny(item["host"])); host != "" {
			wsOpts["headers"] = map[string]any{"Host": host}
		}
		if len(wsOpts) > 0 {
			proxy["ws-opts"] = wsOpts
		}
	}
	if network := strings.ToLower(strings.TrimSpace(asStringAny(item["net"]))); network == "grpc" {
		if service := strings.TrimSpace(asStringAny(item["path"])); service != "" {
			proxy["grpc-opts"] = map[string]any{"grpc-service-name": service}
		}
	}
	return proxy, true
}

func parseProxyName(u *url.URL, fallback string) string {
	name := strings.TrimSpace(u.Fragment)
	if decoded, err := url.QueryUnescape(name); err == nil {
		name = strings.TrimSpace(decoded)
	}
	if name == "" {
		name = fallback
	}
	return name
}

func parseHostPort(u *url.URL) (string, int, bool) {
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return "", 0, false
	}
	port, ok := parseIntPort(u.Port())
	if !ok {
		return "", 0, false
	}
	return host, port, true
}

func parseIntPort(raw string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 || n > 65535 {
		return 0, false
	}
	return n, true
}

func parseSSCredential(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if strings.Contains(raw, ":") {
		parts := strings.SplitN(raw, ":", 2)
		cipher := strings.TrimSpace(parts[0])
		password := strings.TrimSpace(parts[1])
		if cipher != "" && password != "" {
			return cipher, password
		}
	}
	if decoded := tryDecodeBase64Text(raw); decoded != "" {
		parts := strings.SplitN(decoded, ":", 2)
		if len(parts) == 2 {
			cipher := strings.TrimSpace(parts[0])
			password := strings.TrimSpace(parts[1])
			if cipher != "" && password != "" {
				return cipher, password
			}
		}
	}
	return "", ""
}

func parseBoolQuery(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func splitCommaValues(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	items := strings.Split(raw, ",")
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func asStringAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case json.Number:
		return x.String()
	default:
		return ""
	}
}

func parseNonNegativeInt(raw string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
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
