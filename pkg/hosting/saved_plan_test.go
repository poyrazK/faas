package hosting

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func savedPlanFixture(t *testing.T) (DeploymentPlanEvidence, api.HostingManifest, api.HostingDeploymentPlan) {
	t.Helper()
	e, local := deploymentPlanFixture()
	p, err := PlanDeployment(e, local, time.Now().UTC(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	p.APIOrigin = "https://api.gregale.test"
	return e, local, p
}

func TestHostingAPIOrigin(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"https://API.Gregale.test:443/", "https://api.gregale.test"},
		{"https://api.gregale.test:8443", "https://api.gregale.test:8443"},
		{"http://LOCALHOST:80/", "http://localhost"},
		{"http://127.0.0.1:1234", "http://127.0.0.1:1234"},
		{"http://[::1]:80/", "http://[::1]"},
	} {
		got, err := HostingAPIOrigin(tc.input)
		if err != nil || got != tc.want {
			t.Fatalf("%s: %s %v", tc.input, got, err)
		}
	}
	for _, input := range []string{"", "https://", "http://api.gregale.test", "https://user:password@api.gregale.test", "https://api.gregale.test/api", "https://api.gregale.test?x=1", "https://api.gregale.test?", "https://api.gregale.test/#fragment", "https://api.gregale.test/#", "https://api.gregale.test/%2f", "https://api.gregale.test:0", "https://api.gregale.test:65536", "//api.gregale.test", "file:///tmp/api"} {
		if _, err := HostingAPIOrigin(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}

func TestSavedDeploymentPlanStrictJSON(t *testing.T) {
	// adr: 202610171405 — a saved plan is bounded review evidence, never a capability.
	_, _, p := savedPlanFixture(t)
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseDeploymentPlan(raw)
	if err != nil || parsed.APIOrigin != p.APIOrigin || parsed.Version != 1 {
		t.Fatalf("parsed %+v %v", parsed, err)
	}
	for name, body := range map[string]string{
		"empty": "", "null": "null", "array": "[]", "truncated": string(raw[:len(raw)-1]),
		"trailing":         string(raw) + "{}",
		"unknown":          strings.Replace(string(raw), `"version": 1`, `"version": 1, "unexpected": true`, 1),
		"case_alias":       strings.Replace(string(raw), `"version": 1`, `"Version": 1`, 1),
		"duplicate":        strings.Replace(string(raw), `"version": 1`, `"version": 0, "version": 1`, 1),
		"nested_duplicate": strings.Replace(string(raw), `"added": 1`, `"added": 0, "added": 1`, 1),
		"unknown_nested":   strings.Replace(string(raw), `"added": 1`, `"added": 1, "typo": true`, 1),
		"deep_json":        `{"unknown":` + strings.Repeat("[", api.HostingDeploymentPlanJSONDepth+1) + "0" + strings.Repeat("]", api.HostingDeploymentPlanJSONDepth+1) + "}",
		"oversized":        strings.Repeat(" ", api.HostingDeploymentPlanMaxBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDeploymentPlan([]byte(body)); err == nil {
				t.Fatal("ambiguous or invalid plan accepted")
			}
		})
	}
}

func TestSavedDeploymentPlanRejectsIncompleteBlockedOrInconsistentReports(t *testing.T) {
	for name, change := range map[string]func(*api.HostingDeploymentPlan){
		"legacy":                func(p *api.HostingDeploymentPlan) { p.Version = 0 },
		"future_version":        func(p *api.HostingDeploymentPlan) { p.Version++ },
		"missing_origin":        func(p *api.HostingDeploymentPlan) { p.APIOrigin = "" },
		"noncanonical_origin":   func(p *api.HostingDeploymentPlan) { p.APIOrigin += "/" },
		"invalid_site":          func(p *api.HostingDeploymentPlan) { p.SiteID = "other" },
		"invalid_baseline":      func(p *api.HostingDeploymentPlan) { p.BaseReleaseID = "other" },
		"missing_revision":      func(p *api.HostingDeploymentPlan) { p.ExpectedRevision = 0 },
		"missing_fingerprint":   func(p *api.HostingDeploymentPlan) { p.ManifestFingerprint = "" },
		"not_advisory":          func(p *api.HostingDeploymentPlan) { p.Advisory = false },
		"blocked":               func(p *api.HostingDeploymentPlan) { p.State = "blocked" },
		"blockers":              func(p *api.HostingDeploymentPlan) { p.Blockers = []string{"storage_bytes"} },
		"missing_blockers":      func(p *api.HostingDeploymentPlan) { p.Blockers = nil },
		"missing_changes":       func(p *api.HostingDeploymentPlan) { p.Changes.Files = nil },
		"missing_start":         func(p *api.HostingDeploymentPlan) { p.StartedAt = time.Time{} },
		"missing_usage_time":    func(p *api.HostingDeploymentPlan) { p.UsageObservedAt = time.Time{} },
		"reversed_time":         func(p *api.HostingDeploymentPlan) { p.ObservedAt = p.StartedAt.Add(-time.Second) },
		"invalid_files":         func(p *api.HostingDeploymentPlan) { p.FileCount = 0 },
		"invalid_size":          func(p *api.HostingDeploymentPlan) { p.PayloadBytes = -1 },
		"invalid_manifest_size": func(p *api.HostingDeploymentPlan) { p.ManifestBytes = 0 },
		"reservation":           func(p *api.HostingDeploymentPlan) { p.AdditionalReservedBytes-- },
		"missing_quota":         func(p *api.HostingDeploymentPlan) { p.Quotas = p.Quotas[:2] },
		"duplicate_quota":       func(p *api.HostingDeploymentPlan) { p.Quotas[1] = p.Quotas[0] },
		"wrong_limit":           func(p *api.HostingDeploymentPlan) { p.Quotas[0].Limit-- },
		"wrong_projection":      func(p *api.HostingDeploymentPlan) { p.Quotas[0].Projected-- },
	} {
		t.Run(name, func(t *testing.T) {
			_, _, p := savedPlanFixture(t)
			change(&p)
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseDeploymentPlan(raw); err == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
}

func TestReviewedDeploymentPlanRequiresExactContentIntentAndFreshCapacity(t *testing.T) {
	e, local, saved := savedPlanFixture(t)
	current := saved
	if err := CheckReviewedDeploymentPlan(saved, current); err != nil {
		t.Fatal(err)
	}
	e.Usage.ReservedBytes = 50
	current, err := PlanDeployment(e, local, time.Now(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	current.APIOrigin = saved.APIOrigin
	if err := CheckReviewedDeploymentPlan(saved, current); err != nil {
		t.Fatalf("fresh capacity cannot differ: %v", err)
	}
	for _, mutate := range []func(*api.HostingDeploymentPlan){
		func(p *api.HostingDeploymentPlan) { p.APIOrigin = "https://other.gregale.test" },
		func(p *api.HostingDeploymentPlan) { p.SiteID = "44444444-4444-4444-8444-444444444444" },
		func(p *api.HostingDeploymentPlan) { p.ExpectedRevision++ },
		func(p *api.HostingDeploymentPlan) { p.BaseReleaseID = "44444444-4444-4444-8444-444444444444" },
		func(p *api.HostingDeploymentPlan) { p.ManifestFingerprint = strings.Repeat("b", 64) },
		func(p *api.HostingDeploymentPlan) { p.Changes.Changed++ },
		func(p *api.HostingDeploymentPlan) { p.State = "blocked"; p.Blockers = []string{"pending_uploads"} },
	} {
		bad := current
		mutate(&bad)
		if CheckReviewedDeploymentPlan(saved, bad) == nil {
			t.Fatal("changed or blocked plan allowed")
		}
	}
}

func TestSavedPlanCanonicalConfigComparison(t *testing.T) {
	e, local := deploymentPlanFixture()
	e.Capabilities.ConfigEnabled = true
	local.Config = &api.HostingReleaseConfig{Version: 1, Headers: []api.HostingHeaderRule{}, Redirects: []api.HostingRedirect{}}
	current, err := PlanDeployment(e, local, time.Now(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	current.APIOrigin = "https://api.gregale.test"
	raw, _ := json.Marshal(current)
	saved, err := ParseDeploymentPlan(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckReviewedDeploymentPlan(saved, current); err != nil {
		t.Fatalf("canonical omitted empty config arrays differ: %v", err)
	}
}
