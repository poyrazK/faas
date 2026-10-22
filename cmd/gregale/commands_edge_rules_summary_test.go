package main_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRenderEdgeProtection(t *testing.T) {
	var buf bytes.Buffer
	main.RenderEdgeProtection(&buf, "shop", api.EdgeProtectionResponse{
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
	main.RenderEdgeProtection(&buf, "shop", api.EdgeProtectionResponse{Range: "1h", Source: "degraded: prometheus unavailable"})
	if !strings.Contains(buf.String(), "unavailable, not zero") || strings.Contains(buf.String(), "blocked") {
		t.Fatalf("degraded output = %q", buf.String())
	}

	buf.Reset()
	main.RenderEdgeProtection(&buf, "shop", api.EdgeProtectionResponse{Range: "1h", Source: "prometheus"})
	if strings.Count(buf.String(), "none") != 2 || strings.Contains(buf.String(), "waf") {
		t.Fatalf("empty output = %q, want explicit none lines and no waf section", buf.String())
	}

	// adr: 20261022105846 — the penalty box line appears once it has acted.
	buf.Reset()
	main.RenderEdgeProtection(&buf, "shop", api.EdgeProtectionResponse{Range: "1h", Source: "prometheus",
		Penalty: api.EdgeProtectionPenalty{Boxed: 4, Rejected: 31}})
	if !strings.Contains(buf.String(), "4 clients boxed, 31 requests refused while boxed") {
		t.Errorf("penalty output missing:\n%s", buf.String())
	}

	buf.Reset()
	main.RenderEdgeProtection(&buf, "shop", api.EdgeProtectionResponse{Range: "1h", Source: "prometheus",
		WAF: api.EdgeProtectionWAF{Inspected: 96, Detected: 6, NotInspected: 4, Warned: 2, Blocked: 8, InlineSkipped: 1,
			Categories: []api.EdgeProtectionCount{{Name: "sqli", Count: 5}},
			TopRules:   []api.EdgeProtectionCount{{Name: "942100", Count: 5}}}})
	for _, want := range []string{"96 inspected, 6 detected, 4 not inspected", "8 blocked, 2 warned, 1 passed unchecked", "sqli 5", "942100 5"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("waf output missing %q:\n%s", want, buf.String())
		}
	}
}
