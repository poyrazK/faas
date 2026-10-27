package projectsetup

import (
	"maps"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

// ADR-20261026181020: a Compose value that calls a project service over HTTP
// by its Docker host imports as that service's Gregale address. Datastores,
// other schemes, credentials and non-service hosts keep their skip reasons.
func TestPlanEnvImportRewritesServiceAddresses(t *testing.T) {
	value := func(s string) *string { return &s }
	env := reposcan.ComposeEnv{Import: true, Services: map[string]map[string]*string{
		"web": {
			"BILLING_URL":   value("http://billing:8080/v1?x=1"),
			"EVENTS_WS":     value("ws://billing_api:9000/events"),
			"SECURE_URL":    value("https://Billing_API/health"),
			"CACHE_URL":     value("redis://cache:6379"),
			"BILLING_RAW":   value("billing:8080"),
			"WITH_PASSWORD": value("http://user:pw@billing:8080"),
			"EXTERNAL_URL":  value("https://api.example.com"),
		},
		"billing": {}, "billing_api": {}, "cache": {},
	}}
	callable := []string{"web", "billing", "billing_api"} // cache is an image-only datastore
	got := PlanEnvImport(env, "web", callable, api.MustLimitsFor(api.PlanPro))
	wantValues := map[string]string{
		"BILLING_URL":  "http://billing.svc.gregale:10081/v1?x=1",
		"EVENTS_WS":    "ws://billing-api.svc.gregale:10081/events",
		"SECURE_URL":   "https://billing-api.internal/health",
		"EXTERNAL_URL": "https://api.example.com",
	}
	if !maps.Equal(got.Values, wantValues) {
		t.Fatalf("imported %v, want %v", got.Values, wantValues)
	}
	wantSkipped := map[string]string{"CACHE_URL": EnvSkipServiceAddress, "BILLING_RAW": EnvSkipServiceAddress, "WITH_PASSWORD": EnvSkipSecret}
	if !maps.Equal(got.Skipped, wantSkipped) {
		t.Fatalf("skipped %v, want %v", got.Skipped, wantSkipped)
	}
	notes := RewrittenEnvNotes("web", []string{"BILLING_URL", "EXTERNAL_URL"}, got)
	if len(notes) != 1 || !strings.Contains(notes[0], "web: BILLING_URL calls billing; imported with billing.svc.gregale:10081 as its address") {
		t.Fatalf("notes = %q", notes)
	}

	// Without the opt-in a rewritable value is reported for import, not as a
	// Docker address the developer must replace by hand.
	env.Import = false
	got = PlanEnvImport(env, "web", callable, api.MustLimitsFor(api.PlanPro))
	if got.Skipped["BILLING_URL"] != EnvSkipNotOptedIn || len(got.Rewritten) != 0 {
		t.Fatalf("without the opt-in: skipped %v, rewritten %v", got.Skipped, got.Rewritten)
	}
}
