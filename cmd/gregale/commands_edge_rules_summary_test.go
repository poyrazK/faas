package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRenderEdgeProtection(t *testing.T) {
	var buf bytes.Buffer
	renderEdgeProtection(&buf, "shop", api.EdgeProtectionResponse{
		Range: "1h", Source: "prometheus",
		PreAuth:            api.EdgeProtectionPreAuth{Blocked: 3, WouldBlock: 7},
		ValidationFailures: []api.EdgeProtectionCount{{Name: "block", Count: 2}, {Name: "observe", Count: 4}},
		Rejections:         []api.EdgeProtectionRejection{{Gate: "throttle", Status: "429", Count: 9}},
	})
	out := buf.String()
	for _, want := range []string{"Edge protection for shop over 1h", "3 blocked, 7 would block", "2 block, 4 observe", "throttle", "429", " 9"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	renderEdgeProtection(&buf, "shop", api.EdgeProtectionResponse{Range: "1h", Source: "degraded: prometheus unavailable"})
	if !strings.Contains(buf.String(), "unavailable, not zero") || strings.Contains(buf.String(), "blocked") {
		t.Fatalf("degraded output = %q", buf.String())
	}

	buf.Reset()
	renderEdgeProtection(&buf, "shop", api.EdgeProtectionResponse{Range: "1h", Source: "prometheus"})
	if strings.Count(buf.String(), "none") != 2 {
		t.Fatalf("empty output = %q, want explicit none lines", buf.String())
	}
}
