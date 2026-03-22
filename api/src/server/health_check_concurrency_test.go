package server

import "testing"

func TestHealthCheckConcurrencyLimit(t *testing.T) {
	cases := []struct {
		name     string
		settings Settings
		total    int
		want     int
	}{
		{
			name:     "默认值回退到 2",
			settings: Settings{HealthCheckConcurrency: 0},
			total:    8,
			want:     2,
		},
		{
			name:     "配置值受总数限制",
			settings: Settings{HealthCheckConcurrency: 5},
			total:    3,
			want:     3,
		},
		{
			name:     "最小并发为 1",
			settings: Settings{HealthCheckConcurrency: -1},
			total:    1,
			want:     1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := healthCheckConcurrencyLimit(tc.settings, tc.total); got != tc.want {
				t.Fatalf("healthCheckConcurrencyLimit()=%d, want=%d", got, tc.want)
			}
		})
	}
}

func TestSubscriptionProxyCheckConcurrencyLimit(t *testing.T) {
	cases := []struct {
		name     string
		settings Settings
		total    int
		want     int
	}{
		{
			name:     "默认值回退到串行",
			settings: Settings{HealthCheckConcurrency: 0},
			total:    8,
			want:     1,
		},
		{
			name:     "显式配置 1 时保持串行",
			settings: Settings{HealthCheckConcurrency: 1},
			total:    8,
			want:     1,
		},
		{
			name:     "批量检测固定为串行",
			settings: Settings{HealthCheckConcurrency: 6},
			total:    8,
			want:     1,
		},
		{
			name:     "总数更小时受总数限制",
			settings: Settings{HealthCheckConcurrency: 6},
			total:    1,
			want:     1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := subscriptionProxyCheckConcurrencyLimit(tc.settings, tc.total); got != tc.want {
				t.Fatalf("subscriptionProxyCheckConcurrencyLimit()=%d, want=%d", got, tc.want)
			}
		})
	}
}
