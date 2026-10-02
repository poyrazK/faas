package routehealth

import (
	"math"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func floatPointer(value float64) *float64 { return &value }
func latencyReport(now time.Time, check bool, budget int64, candidate, stable float64, requests int64) api.RouteHealthReport {
	r := api.RouteHealthReport{Mode: "enforce", Routes: []api.RouteHealthFinding{{Method: "POST", Path: "/checkout", CheckLatency: check, MaxP95MS: budget, Windows: Windows(now)}}}
	for i := range r.Routes[0].Windows {
		w := &r.Routes[0].Windows[i]
		w.Candidate = api.RouteHealthCounts{Requests: requests, P95LatencyMS: floatPointer(candidate)}
		w.Stable = api.RouteHealthCounts{Requests: requests, P95LatencyMS: floatPointer(stable)}
	}
	return r
}
func TestLatencyBudgetsAndRelativeComparisons(t *testing.T) {
	now := time.Now().UTC()
	anchor := now.Add(-time.Hour)
	cases := []struct {
		name                 string
		check                bool
		budget               int64
		candidate, stable    float64
		requests             int64
		status, windowReason string
	}{
		{"absolute budget", false, 300, 850, 800, 100, "regressed", "latency_budget_exceeded"},
		{"exact budget allowed", false, 300, 300, 120, 100, "healthy", "latency_comparison_healthy"},
		{"budget does not imply relative check", false, 300, 280, 120, 100, "healthy", "latency_comparison_healthy"},
		{"relative only", true, 0, 300, 120, 100, "regressed", "p95_latency_increased"},
		{"both conditions", true, 300, 850, 120, 100, "regressed", "latency_budget_and_regression"},
		{"factor and delta boundary", true, 0, 300, 200, 100, "regressed", "p95_latency_increased"},
		{"small absolute slowdown", true, 0, 100, 10, 100, "healthy", "latency_comparison_healthy"},
		{"small relative slowdown", true, 0, 1150, 1000, 100, "healthy", "latency_comparison_healthy"},
		{"zero stable latency", true, 0, 100, 0, 100, "regressed", "p95_latency_increased"},
		{"zero observed latency", true, 0, 0, 0, 100, "healthy", "latency_comparison_healthy"},
		{"too few latency samples", true, 0, 850, 120, 99, "unknown", "insufficient_latency_requests"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := latencyReport(now, c.check, c.budget, c.candidate, c.stable, c.requests)
			Evaluate(&r, &anchor, "")
			if r.Status != c.status || r.Routes[0].LatencyStatus != c.status || r.Routes[0].Windows[0].LatencyReason != c.windowReason || r.MinimumLatencyRequests != 100 {
				t.Fatalf("%+v", r)
			}
			if (Decision(r).Status == "allowed") != (c.status == "healthy") {
				t.Fatal("latency failed open")
			}
			if c.stable == 0 && r.Routes[0].Windows[0].LatencyFactor != nil {
				t.Fatal("invented ratio for zero baseline")
			}
			r.Mode = "report"
			if Decision(r).Status != "report_only" {
				t.Fatal("report enforced latency")
			}
		})
	}
}
func TestLatencyUnknownIndependentConfirmationAndLegacy(t *testing.T) {
	now := time.Now().UTC()
	anchor := now.Add(-time.Hour)
	for _, value := range []*float64{nil, floatPointer(-1), floatPointer(math.NaN()), floatPointer(math.Inf(1))} {
		r := latencyReport(now, true, 300, 850, 120, 100)
		r.Routes[0].Windows[0].Candidate.P95LatencyMS = value
		Evaluate(&r, &anchor, "")
		if r.Status != "unknown" {
			t.Fatal("invalid percentile authorized progression")
		}
	}
	r := latencyReport(now, true, 300, 850, 120, 100)
	r.Routes[0].Windows[1].Candidate.P95LatencyMS = floatPointer(120)
	r.Routes[0].Windows[1].Candidate.ServerErrors = 10
	Evaluate(&r, &anchor, "")
	if r.Status != "unknown" || r.Routes[0].ErrorStatus != "unknown" || r.Routes[0].LatencyStatus != "unknown" || r.Routes[0].Windows[0].Status != "regressed" || r.Routes[0].Windows[1].Status != "regressed" {
		t.Fatal("different failures falsely confirmed one sustained signal")
	}
	r = latencyReport(now, true, 300, 850, 120, 100)
	r.Routes[0].Windows[0].Stable.Requests = 99
	Evaluate(&r, &anchor, "")
	if r.Status != "unknown" {
		t.Fatal("stable denominator missing")
	}
	r = latencyReport(now, true, 300, 850, 120, 100)
	Evaluate(&r, &now, "")
	if r.Status != "unknown" {
		t.Fatal("stale-stage latency authorized progress")
	}
	r = latencyReport(now, true, 300, 850, 120, 100)
	Evaluate(&r, &anchor, "telemetry_unavailable")
	if r.Status != "unknown" {
		t.Fatal("unavailable latency authorized progress")
	}
	r = latencyReport(now, false, 0, 850, 120, 20)
	Evaluate(&r, &anchor, "")
	if r.Status != "healthy" || r.MinimumLatencyRequests != 0 || r.Routes[0].LatencyStatus != "" {
		t.Fatal("legacy selector changed behavior")
	}
	r = latencyReport(now, true, 300, 850, 120, 20)
	for i := range r.Routes[0].Windows {
		r.Routes[0].Windows[i].Candidate.ServerErrors = 2
	}
	Evaluate(&r, &anchor, "")
	if r.Status != "regressed" || r.Routes[0].LatencyStatus != "unknown" {
		t.Fatal("missing latency hid established 5xx violation")
	}
}
func TestLatencyConfigBoundsAndRouteIdentity(t *testing.T) {
	zero := int64(0)
	for _, routes := range [][]api.RouteHealthRoute{
		{{Method: "POST", Path: "/checkout", MaxP95MS: -1}},
		{{Method: "POST", Path: "/checkout", MaxP95MS: api.RouteHealthMaxP95BudgetMS + 1}},
		{{Method: "POST", Path: "/checkout", MaxP95MS: 300}, {Method: "POST", Path: "/checkout", MaxP95MS: 600}},
	} {
		if Validate(api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: routes}) == nil {
			t.Fatal("invalid latency configuration accepted")
		}
	}
}
