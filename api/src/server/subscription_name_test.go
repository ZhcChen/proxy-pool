package server

import (
	"encoding/base64"
	"testing"
)

func TestInferSubscriptionName_FromBase64URIList(t *testing.T) {
	list := "anytls://example:1#%F0%9F%87%BA%F0%9F%87%B8%20SKYLUMO.CC\nss://x#香港-01\n"
	raw := base64.StdEncoding.EncodeToString([]byte(list))

	got := inferSubscriptionName("https://skylumo.com/api/v1/client/subscribe?token=t1", raw, nil)
	if got != "SKYLUMO" {
		t.Fatalf("期望识别为 SKYLUMO，实际=%q", got)
	}
}

func TestInferSubscriptionName_FallbackToURLHost(t *testing.T) {
	got := inferSubscriptionName("https://sub.skylumo.com/api/v1/client/subscribe?token=t1", "", nil)
	if got != "SKYLUMO" {
		t.Fatalf("期望从 URL 回退识别为 SKYLUMO，实际=%q", got)
	}
}
