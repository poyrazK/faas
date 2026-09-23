package gateway

// adr: 084 — keyed affinity refines the existing traffic-split routing contract.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestVersionAffinityKeyFromRequest(t *testing.T) {
	tests := []struct {
		name        string
		values      []string
		wantKey     string
		wantOutcome string
	}{
		{name: "missing", wantOutcome: versionAffinityKeyMissing},
		{name: "valid and trimmed", values: []string{"  customer-42  "}, wantKey: "customer-42", wantOutcome: versionAffinityKeyValid},
		{name: "empty", values: []string{"  "}, wantOutcome: versionAffinityKeyInvalid},
		{name: "oversized", values: []string{strings.Repeat("x", VersionAffinityKeyMaxBytes+1)}, wantOutcome: versionAffinityKeyInvalid},
		{name: "duplicate", values: []string{"a", "b"}, wantOutcome: versionAffinityKeyInvalid},
		{name: "nul", values: []string{"a\x00b"}, wantOutcome: versionAffinityKeyInvalid},
		{name: "control", values: []string{"a\x1fb"}, wantOutcome: versionAffinityKeyInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://app.example/", nil)
			if tc.values != nil {
				req.Header[api.VersionKeyHeader] = tc.values
			}
			key, outcome := versionAffinityKeyFromRequest(req)
			if key != tc.wantKey || outcome != tc.wantOutcome {
				t.Fatalf("versionAffinityKeyFromRequest = %q/%q, want %q/%q", key, outcome, tc.wantKey, tc.wantOutcome)
			}
		})
	}
}

func TestVersionAffinityMetricUsesOnlyBoundedOutcomes(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObserveVersionAffinityKey(versionAffinitySurfacePublic, versionAffinityKeyValid)
	metrics.ObserveVersionAffinityKey(versionAffinitySurfacePublic, versionAffinityKeyInvalid)
	metrics.ObserveVersionAffinityKey("customer-controlled-value", versionAffinityKeyValid)
	if got := readCounterLabels(t, metrics, "gateway_version_affinity_key_total", map[string]string{"surface": versionAffinitySurfacePublic, "outcome": versionAffinityKeyValid}); got != 1 {
		t.Fatalf("valid version-key outcomes = %v, want 1", got)
	}
	if got := readCounterLabels(t, metrics, "gateway_version_affinity_key_total", map[string]string{"surface": versionAffinitySurfacePublic, "outcome": versionAffinityKeyInvalid}); got != 1 {
		t.Fatalf("invalid version-key outcomes = %v, want 1", got)
	}
}
