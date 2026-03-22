package server

import (
	"encoding/json"
)

// MihomoProxy 保存订阅节点原始字段。
type MihomoProxy map[string]any

func (p MihomoProxy) Name() string {
	v, _ := p["name"].(string)
	return v
}

func (p MihomoProxy) Type() string {
	v, _ := p["type"].(string)
	return v
}

type Subscription struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	URL       *string       `json:"url"`
	CreatedAt string        `json:"createdAt"`
	UpdatedAt string        `json:"updatedAt"`
	LastError *string       `json:"lastError"`
	Proxies   []MihomoProxy `json:"proxies"`
}

type Instance struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	SubscriptionID string      `json:"subscriptionId"`
	ProxyName      string      `json:"proxyName"`
	Proxy          MihomoProxy `json:"proxy"`
	MixedPort      int         `json:"mixedPort"`
	ControllerPort int         `json:"controllerPort"`
	AutoStart      bool        `json:"autoStart"`
	AutoSwitch     bool        `json:"autoSwitch"`
	CreatedAt      string      `json:"createdAt"`
	UpdatedAt      string      `json:"updatedAt"`
}

type ProxyAuth struct {
	Enabled  bool   `json:"enabled"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type Settings struct {
	BindAddress            string    `json:"bindAddress"`
	AllowLan               bool      `json:"allowLan"`
	LogLevel               string    `json:"logLevel"`
	BaseMixedPort          int       `json:"baseMixedPort"`
	BaseControllerPort     int       `json:"baseControllerPort"`
	MaxLogLines            int       `json:"maxLogLines"`
	HealthCheckIntervalSec int       `json:"healthCheckIntervalSec"`
	HealthCheckConcurrency int       `json:"healthCheckConcurrency"`
	SubscriptionRefreshMin int       `json:"subscriptionRefreshIntervalMin"`
	HealthCheckURL         string    `json:"healthCheckUrl"`
	ExportHost             string    `json:"exportHost"`
	ProxyAuth              ProxyAuth `json:"proxyAuth"`
}

type State struct {
	Version       int            `json:"version"`
	Settings      Settings       `json:"settings"`
	Subscriptions []Subscription `json:"subscriptions"`
	Instances     []Instance     `json:"instances"`
}

type HealthStatus struct {
	OK         bool     `json:"ok"`
	CheckedAt  string   `json:"checkedAt"`
	LatencyMs  *float64 `json:"latencyMs"`
	StatusCode *int     `json:"statusCode,omitempty"`
	Error      *string  `json:"error,omitempty"`
	Target     *string  `json:"target,omitempty"`
	ProxyName  *string  `json:"proxyName,omitempty"`
}

type runtimeStatus struct {
	Running   bool    `json:"running"`
	PID       *int    `json:"pid"`
	StartedAt *string `json:"startedAt"`
}

func cloneProxy(p MihomoProxy) MihomoProxy {
	if p == nil {
		return MihomoProxy{}
	}
	b, _ := json.Marshal(p)
	var out MihomoProxy
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = MihomoProxy{}
	}
	return out
}

func cloneProxyList(in []MihomoProxy) []MihomoProxy {
	if len(in) == 0 {
		return []MihomoProxy{}
	}
	out := make([]MihomoProxy, 0, len(in))
	for _, p := range in {
		out = append(out, cloneProxy(p))
	}
	return out
}
