package projectsetup

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

// Without x-gregale-env-import, a Compose value that calls a sibling service
// over HTTP imports as that service's Gregale address (ADR-20261104221217):
// a Docker host does not resolve on Gregale, and the address is never a
// credential. Every other plain value still waits for the opt-in.
func TestSiblingServiceAddressImportsWithoutOptIn(t *testing.T) {
	value := func(s string) *string { return &s }
	env := reposcan.ComposeEnv{Services: map[string]map[string]*string{
		"api": {
			"BILLING_URL": value("http://billing:8080/v1"),
			"LOG_LEVEL":   value("info"),
			"CACHE_URL":   value("redis://cache:6379"),
			"PARTNER_URL": value("https://partner.example.com"),
		},
		"billing": {},
		"cache":   {},
	}}
	limits, _ := api.LimitsFor(api.PlanPro)
	plan := PlanEnvImport(env, "api", []string{"api", "billing"}, limits, false)
	if got := plan.Values["BILLING_URL"]; got != "http://billing.svc.gregale:10081/v1" {
		t.Fatalf("BILLING_URL = %q, want the Gregale address without the opt-in (values %v, skipped %v)", got, plan.Values, plan.Skipped)
	}
	if plan.Rewritten["BILLING_URL"] != "billing" {
		t.Fatalf("BILLING_URL not reported as rewritten: %v", plan.Rewritten)
	}
	for _, key := range []string{"LOG_LEVEL", "PARTNER_URL"} {
		if plan.Skipped[key] != EnvSkipNotOptedIn {
			t.Errorf("%s skipped as %q, want %q", key, plan.Skipped[key], EnvSkipNotOptedIn)
		}
	}
	if plan.Skipped["CACHE_URL"] != EnvSkipServiceAddress {
		t.Errorf("CACHE_URL skipped as %q, want %q", plan.Skipped["CACHE_URL"], EnvSkipServiceAddress)
	}

	// A Kubernetes workload keeps its own annotation opt-in.
	env.Kubernetes = map[string]bool{"api": true}
	plan = PlanEnvImport(env, "api", []string{"api", "billing"}, limits, false)
	if plan.Skipped["BILLING_URL"] != EnvSkipNotAnnotated {
		t.Errorf("Kubernetes BILLING_URL skipped as %q, want %q", plan.Skipped["BILLING_URL"], EnvSkipNotAnnotated)
	}
}
