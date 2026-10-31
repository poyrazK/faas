package projectsetup

import (
	"fmt"
	"maps"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

func TestPlanEnvImport(t *testing.T) {
	value := func(s string) *string { return &s }
	env := reposcan.ComposeEnv{Import: true, Services: map[string]map[string]*string{
		"api": {
			"LOG_LEVEL":     value("info"),
			"RATIO":         value("1.0"),
			"PUBLIC_URL":    value("https://shop.example.com"),
			"STRIPE_KEY":    value("plain"),
			"SESSION_TOKEN": value("plain"),
			"UPSTREAM":      value("sk_live_" + "abcdefghijklmnopqrstuvwxyz0123"),
			"DATABASE_URL":  value("postgres://app:hunter2@db.example.com/app"),
			"BILLING_URL":   value("http://billing:8080"),
			"CACHE_HOST":    value("cache"),
			"QUEUE":         value("cache:6379"),
			"LOCAL_URL":     value("http://localhost:3000"),
			"DOCKER_HOST":   value("host.docker.internal"),
			"FROM_DOTENV":   value("${TOKEN_FROM_DOTENV}"),
			"FROM_HOST":     nil,
			"BLANK":         value(""),
			"lowercase":     value("x"),
			"PORT":          value("3000"),
			"GREGALE_TRACE": value("1"),
		},
		"billing": {},
		"cache":   {},
	}}
	got := PlanEnvImport(env, "api", []string{"api"}, api.MustLimitsFor(api.PlanPro), true)
	wantValues := map[string]string{"LOG_LEVEL": "info", "RATIO": "1.0", "PUBLIC_URL": "https://shop.example.com"}
	if !maps.Equal(got.Values, wantValues) {
		t.Fatalf("imported %v, want %v", got.Values, wantValues)
	}
	wantSkipped := map[string]string{
		"STRIPE_KEY": EnvSkipSecret, "SESSION_TOKEN": EnvSkipSecret, "UPSTREAM": EnvSkipSecret, "DATABASE_URL": EnvSkipSecret,
		"BILLING_URL": EnvSkipServiceAddress, "CACHE_HOST": EnvSkipServiceAddress, "QUEUE": EnvSkipServiceAddress,
		"LOCAL_URL": EnvSkipLocalAddress, "DOCKER_HOST": EnvSkipLocalAddress,
		"FROM_DOTENV": EnvSkipInterpolated, "FROM_HOST": EnvSkipHostValue, "BLANK": EnvSkipEmpty, "lowercase": EnvSkipInvalid,
	}
	if !maps.Equal(got.Skipped, wantSkipped) {
		t.Fatalf("skipped %v, want %v", got.Skipped, wantSkipped)
	}

	env.Import = false
	got = PlanEnvImport(env, "api", []string{"api"}, api.MustLimitsFor(api.PlanPro), true)
	if len(got.Values) != 0 || got.Skipped["LOG_LEVEL"] != EnvSkipNotOptedIn || got.Skipped["STRIPE_KEY"] != EnvSkipSecret {
		t.Fatalf("without the opt-in: values %v, skipped %v", got.Values, got.Skipped)
	}
}

// A Kubernetes workload opts in with its own annotation, never with a
// Compose file's opt-in; its Secret keys are reported, never imported; and a
// cluster DNS name it did not resolve to a scanned workload is not imported
// (ADR-20261029093621 §5).
func TestPlanEnvImport_Kubernetes(t *testing.T) {
	value := func(s string) *string { return &s }
	env := reposcan.ComposeEnv{
		Import: true,
		Services: map[string]map[string]*string{
			"api": {
				"LOG_LEVEL":   value("debug"),
				"BILLING_URL": value("http://billing:8080/v1"),
				"CACHE_URL":   value("redis://cache.infra.svc.cluster.local:6379"),
				"SEARCH":      value("http://search.infra.svc:9200"),
			},
			"worker":  {"LOG_LEVEL": value("info")},
			"billing": {},
		},
		Kubernetes:     map[string]bool{"api": true, "worker": true, "billing": true},
		ImportServices: map[string]bool{"api": true},
		SecretRefs:     map[string][]string{"api": {"DB_PASSWORD"}, "worker": {"API_TOKEN"}},
	}
	limits := api.MustLimitsFor(api.PlanPro)

	plan := PlanEnvImport(env, "api", []string{"api", "billing"}, limits, true)
	if got, want := plan.Values, map[string]string{
		"LOG_LEVEL":   "debug",
		"BILLING_URL": "http://billing.svc.gregale:" + fmt.Sprint(api.ServiceBindingPort) + "/v1",
	}; !maps.Equal(got, want) {
		t.Errorf("api values = %v, want %v", got, want)
	}
	if got, want := plan.Skipped, map[string]string{
		"CACHE_URL":   EnvSkipServiceAddress,
		"SEARCH":      EnvSkipServiceAddress,
		"DB_PASSWORD": EnvSkipSecretRef,
	}; !maps.Equal(got, want) {
		t.Errorf("api skipped = %v, want %v", got, want)
	}

	worker := PlanEnvImport(env, "worker", nil, limits, true)
	if len(worker.Values) != 0 {
		t.Errorf("worker values = %v, want none: Compose's opt-in does not cover it", worker.Values)
	}
	if got, want := worker.Skipped, map[string]string{"LOG_LEVEL": EnvSkipNotAnnotated, "API_TOKEN": EnvSkipSecretRef}; !maps.Equal(got, want) {
		t.Errorf("worker skipped = %v, want %v", got, want)
	}
}
