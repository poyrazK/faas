package routehealth

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 945
func TestValidateProbe(t *testing.T) {
	zero := int64(0)
	gate := func(routes ...api.RouteHealthRoute) error {
		return Validate(api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: routes})
	}
	probe := func(method, selector, path string) api.RouteHealthRoute {
		return api.RouteHealthRoute{Method: method, Path: selector, Probe: &api.RouteHealthProbe{Path: path}}
	}
	for _, ok := range []api.RouteHealthRoute{
		probe("GET", "/users/{id}", "/users/42"),
		probe("HEAD", "/status", "/status"),
		probe("GET", "/orgs/{org}/repos/{repo}", "/orgs/acme/repos/api"),
	} {
		if err := gate(ok); err != nil {
			t.Errorf("Validate(%+v) = %v", ok, err)
		}
	}
	for name, bad := range map[string]api.RouteHealthRoute{
		"write method":        probe("POST", "/checkout", "/checkout"),
		"parameter left":      probe("GET", "/users/{id}", "/users/{id}"),
		"query":               probe("GET", "/search", "/search?q=x"),
		"different literal":   probe("GET", "/users/{id}", "/accounts/42"),
		"extra segment":       probe("GET", "/users/{id}", "/users/42/admin"),
		"empty parameter":     probe("GET", "/users/{id}", "/users/"),
		"relative":            probe("GET", "/status", "status"),
		"whitespace in value": probe("GET", "/users/{id}", "/users/4 2"),
	} {
		if gate(bad) == nil {
			t.Errorf("%s: Validate accepted %+v", name, bad)
		}
	}
	many := []api.RouteHealthRoute{}
	for _, p := range []string{"/a", "/b", "/c", "/d", "/e", "/f"} {
		many = append(many, probe("GET", p, p))
	}
	if gate(many...) == nil {
		t.Errorf("Validate accepted %d probed routes", len(many))
	}
	a := []api.RouteHealthRoute{probe("GET", "/users/{id}", "/users/1")}
	b := []api.RouteHealthRoute{probe("GET", "/users/{id}", "/users/2")}
	if RoutesEqual(a, b) {
		t.Error("RoutesEqual ignored a probe path change")
	}
	clone := CloneRoutes(a)
	clone[0].Probe.Path = "/users/9"
	if a[0].Probe.Path != "/users/1" {
		t.Error("CloneRoutes shared the probe")
	}
}

// adr: 945
func TestEvaluateAdoptsSyntheticEvidenceOnlyWhenSparse(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 20, 45, 0, time.UTC)
	anchor := now.Add(-12 * time.Minute)
	pooled, ok := PooledWindows(&anchor, now)
	if !ok {
		t.Fatal("expected pooled windows")
	}
	finding := func(path string, minute, pooledCandidate int64, synthetic api.RouteHealthCounts, latency bool) api.RouteHealthFinding {
		f := api.RouteHealthFinding{Method: "GET", Path: path, CheckLatency: latency, Windows: Windows(now), PooledWindows: append([]api.RouteHealthWindowEvidence(nil), pooled...), SyntheticWindows: append([]api.RouteHealthWindowEvidence(nil), pooled...)}
		for i := range f.Windows {
			f.Windows[i].Candidate, f.Windows[i].Stable = api.RouteHealthCounts{Requests: minute}, api.RouteHealthCounts{Requests: minute}
		}
		for i := range f.PooledWindows {
			f.PooledWindows[i].Candidate, f.PooledWindows[i].Stable = api.RouteHealthCounts{Requests: pooledCandidate}, api.RouteHealthCounts{Requests: pooledCandidate}
			f.SyntheticWindows[i].Candidate, f.SyntheticWindows[i].Stable = synthetic, api.RouteHealthCounts{Requests: synthetic.Requests}
		}
		return f
	}
	report := api.RouteHealthReport{Routes: []api.RouteHealthFinding{
		finding("/healthy", 0, 0, api.RouteHealthCounts{Requests: 50}, false),
		finding("/broken", 0, 0, api.RouteHealthCounts{Requests: 50, ServerErrors: 25}, false),
		finding("/guarded", 0, 0, api.RouteHealthCounts{Requests: 50, Unauthenticated: 50}, false),
		finding("/pooled", 0, 40, api.RouteHealthCounts{Requests: 50, ServerErrors: 25}, false),
		finding("/latency", 0, 0, api.RouteHealthCounts{Requests: 50}, true),
		finding("/latency-broken", 0, 0, api.RouteHealthCounts{Requests: 50, ServerErrors: 25}, true),
	}}
	Evaluate(&report, &anchor, "")
	want := map[string][2]string{
		"/healthy":        {"healthy", "synthetic"},
		"/broken":         {"regressed", "synthetic"},
		"/guarded":        {"unknown", ""},
		"/pooled":         {"healthy", "pooled"}, // organic pooled evidence wins over probes
		"/latency":        {"unknown", ""},       // probes cannot settle latency
		"/latency-broken": {"regressed", "synthetic"},
	}
	for _, f := range report.Routes {
		if got := [2]string{f.Status, f.EvidenceWindow}; got != want[f.Path] {
			t.Errorf("%s = %v, want %v", f.Path, got, want[f.Path])
		}
	}
	if report.Routes[2].SyntheticWindows[0].ErrorReason != "probe_unauthenticated" {
		t.Errorf("guarded probe reason = %q", report.Routes[2].SyntheticWindows[0].ErrorReason)
	}
	saved := report.Routes[1]
	saved.Status, saved.EvidenceWindow = "", ""
	SummarizeFindingWithPooled(&saved, &anchor)
	if saved.Status != "regressed" || saved.EvidenceWindow != "synthetic" {
		t.Errorf("re-derived synthetic finding = %s/%q", saved.Status, saved.EvidenceWindow)
	}
	blocked := api.RouteHealthReport{Routes: []api.RouteHealthFinding{finding("/broken", 0, 0, api.RouteHealthCounts{Requests: 50, ServerErrors: 25}, false)}}
	Evaluate(&blocked, &anchor, "telemetry_not_entitled")
	if blocked.Routes[0].EvidenceWindow != "" {
		t.Error("unavailable report adopted synthetic evidence")
	}
}
