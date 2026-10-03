package routehealth

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestObservedRouteComparisons(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 5, 45, 0, time.UTC)
	anchor := now.Add(-time.Hour)
	cases := []struct {
		name              string
		candidate, stable int64
		requests          int64
		sparse, mixed     bool
		unavailable       string
		want              string
	}{
		{name: "healthy", requests: 100, want: "healthy"},
		{name: "critical route regression", candidate: 10, requests: 100, want: "regressed"},
		{name: "weighted denominator", candidate: 1, requests: 100, want: "healthy"},
		{name: "zero baseline errors", candidate: 2, requests: 20, want: "regressed"},
		{name: "exact factor boundary", candidate: 6, stable: 2, requests: 20, want: "regressed"},
		{name: "exact delta boundary", candidate: 6, stable: 1, requests: 100, want: "regressed"},
		{name: "stable also failing", candidate: 20, stable: 10, requests: 100, want: "healthy"},
		{name: "small absolute increase", candidate: 6, stable: 2, requests: 100, want: "healthy"},
		{name: "too sparse", requests: 19, want: "unknown"},
		{name: "stable sparse", requests: 100, sparse: true, want: "unknown"},
		{name: "one noisy window", candidate: 10, requests: 100, mixed: true, want: "unknown"},
		{name: "unavailable", requests: 100, unavailable: "telemetry_unavailable", want: "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			report := api.RouteHealthReport{Mode: "enforce", Routes: []api.RouteHealthFinding{{Method: "POST", Path: "/checkout", Windows: Windows(now)}}}
			for i := range report.Routes[0].Windows {
				w := &report.Routes[0].Windows[i]
				w.Candidate = api.RouteHealthCounts{Requests: c.requests, ServerErrors: c.candidate}
				w.Stable = api.RouteHealthCounts{Requests: c.requests, ServerErrors: c.stable}
				if c.sparse {
					w.Stable.Requests = 19
				}
				if c.mixed && i == 1 {
					w.Candidate.ServerErrors = 0
				}
			}
			Evaluate(&report, &anchor, c.unavailable)
			if report.Status != c.want {
				t.Fatalf("%+v", report)
			}
			decision := Decision(report)
			if (decision.Status == "allowed") != (c.want == "healthy") {
				t.Fatalf("failed open: %+v", decision)
			}
			report.Mode = "report"
			if Decision(report).Status != "report_only" {
				t.Fatal("report mode enforced")
			}
		})
	}
}
func TestRouteHealthAnchorsAndSelectedRoutes(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 5, 45, 0, time.UTC)
	for _, anchor := range []*time.Time{nil, &now} {
		r := api.RouteHealthReport{Routes: []api.RouteHealthFinding{{Windows: Windows(now)}}}
		for i := range r.Routes[0].Windows {
			r.Routes[0].Windows[i].Candidate.Requests = 100
			r.Routes[0].Windows[i].Stable.Requests = 100
		}
		Evaluate(&r, anchor, "")
		if r.Status != "unknown" {
			t.Fatal("prior-stage evidence passed")
		}
	}
	anchor := now.Add(-time.Hour)
	r := api.RouteHealthReport{Routes: []api.RouteHealthFinding{{Path: "/busy", Windows: Windows(now)}, {Path: "/checkout", Windows: Windows(now)}}}
	for i := range r.Routes {
		for j := range r.Routes[i].Windows {
			w := &r.Routes[i].Windows[j]
			w.Candidate.Requests = 100
			w.Stable.Requests = 100
			if i == 1 {
				w.Candidate.ServerErrors = 10
			}
		}
	}
	Evaluate(&r, &anchor, "")
	if r.Status != "regressed" || r.Routes[0].Status != "healthy" {
		t.Fatal("busy healthy route hid critical regression")
	}
	windows := Windows(now)
	if !windows[0].End.Equal(windows[1].Start) || windows[1].End.After(now.Add(-api.RouteHealthIngestionLag)) {
		t.Fatal("windows overlap or have no ingestion allowance")
	}
}
func TestRouteHealthConfigurationValidation(t *testing.T) {
	zero := int64(0)
	for _, request := range []api.SetRouteHealthGateRequest{
		{Mode: "enforce", ExpectedRevision: &zero},
		{Mode: "report"},
		{Mode: "auto", ExpectedRevision: &zero},
		{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/a?secret=x"}}},
		{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/a\n"}}},
		{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/*"}}},
		{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/a"}, {Method: "GET", Path: "/a"}}},
	} {
		if Validate(request) == nil {
			t.Fatalf("accepted invalid config: %+v", request)
		}
	}
	if err := Validate(api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/profiles/{id}"}}}); err != nil {
		t.Fatal(err)
	}
}
