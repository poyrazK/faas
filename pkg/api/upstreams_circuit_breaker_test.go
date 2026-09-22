// adr: 201
package api_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func f64(v float64) *float64 { return &v }
func ip(v int) *int          { return &v }
func bp(v bool) *bool        { return &v }

func TestUpdateUpstreamCircuitBreakerRequestAcceptsValidValues(t *testing.T) {
	cases := []api.UpdateUpstreamCircuitBreakerRequest{
		{Enabled: bp(true)},
		{Enabled: bp(false)},
		{FailureThreshold: f64(0.25)},
		{FailureThreshold: f64(1)},
		{MinSamples: ip(1)},
		{MinSamples: ip(1000)},
		{OpenSeconds: ip(3600)},
		{Enabled: bp(true), FailureThreshold: f64(0.9), MinSamples: ip(10), OpenSeconds: ip(60)},
	}
	for i, tc := range cases {
		if p := tc.Validate(); p != nil {
			t.Fatalf("case %d (%+v): rejected: %v", i, tc, p)
		}
	}
}

// An empty body is a no-op that reads like a change; rejecting it means a
// customer who mistypes a field name finds out immediately.
func TestUpdateUpstreamCircuitBreakerRequestRejectsEmptyBody(t *testing.T) {
	var req api.UpdateUpstreamCircuitBreakerRequest
	p := req.Validate()
	if p == nil {
		t.Fatal("empty body accepted; a request that changes nothing must be rejected")
	}
	if !strings.Contains(p.Detail, "at least one field") {
		t.Fatalf("detail = %q, want it to name the problem", p.Detail)
	}
}

func TestUpdateUpstreamCircuitBreakerRequestRejectsOutOfRange(t *testing.T) {
	cases := []struct {
		name string
		req  api.UpdateUpstreamCircuitBreakerRequest
		want string
	}{
		{"threshold is a ratio not a count", api.UpdateUpstreamCircuitBreakerRequest{FailureThreshold: f64(5)}, "failure_threshold"},
		// A pointer to 0 IS a set field, so this must fail the RANGE check
		// rather than the "nothing set" check — 0 is a meaningful mistake
		// (a breaker that opens on any failure), not an omission.
		{"threshold zero", api.UpdateUpstreamCircuitBreakerRequest{FailureThreshold: f64(0)}, "failure_threshold"},
		{"threshold negative", api.UpdateUpstreamCircuitBreakerRequest{FailureThreshold: f64(-1)}, "failure_threshold"},
		{"min_samples zero", api.UpdateUpstreamCircuitBreakerRequest{MinSamples: ip(0)}, "min_samples"},
		{"min_samples too high", api.UpdateUpstreamCircuitBreakerRequest{MinSamples: ip(99999)}, "min_samples"},
		{"open_seconds too high", api.UpdateUpstreamCircuitBreakerRequest{OpenSeconds: ip(99999)}, "open_seconds"},
	}
	for _, tc := range cases {
		p := tc.req.Validate()
		if p == nil {
			t.Fatalf("%s: accepted, want rejection", tc.name)
		}
		if !strings.Contains(p.Detail, tc.want) {
			t.Fatalf("%s: detail = %q, want it to mention %q", tc.name, p.Detail, tc.want)
		}
	}
}

// The two breaker surfaces must share bounds, or an operator can tune the
// egress breaker into a regime the edge breaker forbids.
func TestUpstreamAndEdgeBreakerBoundsAgree(t *testing.T) {
	overMin := api.UpdateUpstreamCircuitBreakerRequest{MinSamples: ip(api.MaxEdgeRuleCircuitMinRequests + 1)}
	if overMin.Validate() == nil {
		t.Fatalf("min_samples above MaxEdgeRuleCircuitMinRequests (%d) accepted", api.MaxEdgeRuleCircuitMinRequests)
	}
	atMin := api.UpdateUpstreamCircuitBreakerRequest{MinSamples: ip(api.MaxEdgeRuleCircuitMinRequests)}
	if p := atMin.Validate(); p != nil {
		t.Fatalf("min_samples at the shared ceiling rejected: %v", p)
	}
	overOpen := api.UpdateUpstreamCircuitBreakerRequest{OpenSeconds: ip(api.MaxEdgeRuleCircuitOpenSeconds + 1)}
	if overOpen.Validate() == nil {
		t.Fatalf("open_seconds above MaxEdgeRuleCircuitOpenSeconds (%d) accepted", api.MaxEdgeRuleCircuitOpenSeconds)
	}
}
