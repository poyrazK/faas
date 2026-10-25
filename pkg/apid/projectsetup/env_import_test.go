package projectsetup

import (
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
	got := PlanEnvImport(env, "api", api.MustLimitsFor(api.PlanPro))
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
	got = PlanEnvImport(env, "api", api.MustLimitsFor(api.PlanPro))
	if len(got.Values) != 0 || got.Skipped["LOG_LEVEL"] != EnvSkipNotOptedIn || got.Skipped["STRIPE_KEY"] != EnvSkipSecret {
		t.Fatalf("without the opt-in: values %v, skipped %v", got.Values, got.Skipped)
	}
}
