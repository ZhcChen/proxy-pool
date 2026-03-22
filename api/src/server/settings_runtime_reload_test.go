package server

import "testing"

func TestSettingsAffectRunningInstances(t *testing.T) {
	base := Settings{
		BindAddress:            "127.0.0.1",
		AllowLan:               false,
		LogLevel:               "info",
		HealthCheckIntervalSec: 60,
		HealthCheckURL:         "http://www.gstatic.com/generate_204",
		ExportHost:             "1.2.3.4",
		ProxyAuth: ProxyAuth{
			Enabled:  true,
			Username: "u1",
			Password: "p1",
		},
	}

	cases := []struct {
		name string
		mut  func(s Settings) Settings
		want bool
	}{
		{
			name: "bindAddress 变更需要重载",
			mut: func(s Settings) Settings {
				s.BindAddress = "0.0.0.0"
				return s
			},
			want: true,
		},
		{
			name: "allowLan 变更需要重载",
			mut: func(s Settings) Settings {
				s.AllowLan = true
				return s
			},
			want: true,
		},
		{
			name: "proxyAuth 变更需要重载",
			mut: func(s Settings) Settings {
				s.ProxyAuth.Password = "p2"
				return s
			},
			want: true,
		},
		{
			name: "healthCheckURL 变更需要重载",
			mut: func(s Settings) Settings {
				s.HealthCheckURL = "https://example.com/204"
				return s
			},
			want: true,
		},
		{
			name: "仅 exportHost 变更不需要重载",
			mut: func(s Settings) Settings {
				s.ExportHost = "example.com"
				return s
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := tc.mut(base)
			got := settingsAffectRunningInstances(base, next)
			if got != tc.want {
				t.Fatalf("settingsAffectRunningInstances()=%v, want=%v", got, tc.want)
			}
		})
	}
}
