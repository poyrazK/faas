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
	got := PlanEnvImport(env, "web", callable, api.MustLimitsFor(api.PlanPro), true)
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
	got = PlanEnvImport(env, "web", callable, api.MustLimitsFor(api.PlanPro), true)
	if got.Skipped["BILLING_URL"] != EnvSkipNotOptedIn || len(got.Rewritten) != 0 {
		t.Fatalf("without the opt-in: skipped %v, rewritten %v", got.Skipped, got.Rewritten)
	}
}

// ADR-20261029230000: without private service HTTPS an https or wss value
// imports as the plaintext service address, which answers, and the plan
// note says so.
func TestPlanEnvImportWithoutPrivateHTTPS(t *testing.T) {
	value := func(s string) *string { return &s }
	env := reposcan.ComposeEnv{Import: true, Services: map[string]map[string]*string{
		"web": {
			"SECURE_URL":  value("https://Billing_API/health"),
			"EVENTS_WSS":  value("wss://billing_api:9443/events"),
			"BILLING_URL": value("http://billing_api:8080/v1"),
		},
		"billing_api": {},
	}}
	got := PlanEnvImport(env, "web", []string{"web", "billing_api"}, api.MustLimitsFor(api.PlanPro), false)
	want := map[string]string{
		"SECURE_URL":  "http://billing-api.svc.gregale:10081/health",
		"EVENTS_WSS":  "ws://billing-api.svc.gregale:10081/events",
		"BILLING_URL": "http://billing-api.svc.gregale:10081/v1",
	}
	if !maps.Equal(got.Values, want) {
		t.Fatalf("imported %v, want %v", got.Values, want)
	}
	notes := RewrittenEnvNotes("web", []string{"BILLING_URL", "EVENTS_WSS", "SECURE_URL"}, got)
	plaintext := 0
	for _, note := range notes {
		if strings.Contains(note, "because private service HTTPS is not enabled") {
			plaintext++
		}
	}
	if len(notes) != 3 || plaintext != 2 {
		t.Fatalf("notes = %q; want the two secure values marked plaintext", notes)
	}
}
