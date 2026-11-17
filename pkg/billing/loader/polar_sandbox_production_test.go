package loader

// A production deployment refuses Polar's sandbox: a sandbox checkout pays
// with a test card and still grants a paid plan (billing launch review
// 2026-11-10, R4-2). Other environments keep using it.

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func polarSandboxEnv(t *testing.T, environment string, extra map[string]string) func(string) string {
	t.Helper()
	values := map[string]string{
		"FAAS_BILLING_PROVIDER":       "polar",
		"FAAS_POLAR_ACCESS_TOKEN":     "polar_test_token",
		"FAAS_POLAR_WEBHOOK_SECRET":   "dGVzdA==",
		"FAAS_POLAR_HOBBY_PRODUCT_ID": "hobby-product",
		"FAAS_POLAR_PRO_PRODUCT_ID":   "pro-product",
		"FAAS_POLAR_SCALE_PRODUCT_ID": "scale-product",
		"FAAS_POLAR_USAGE_EVENT_NAME": "ram_usage",
		"FAAS_POLAR_METER_ID":         "meter-1",
		"FAAS_POLAR_BASE_URL":         polarCatalogServer(t, true),
		"FAAS_ENVIRONMENT":            environment,
	}
	for k, v := range extra {
		values[k] = v
	}
	return mapEnv(values)
}

func TestPolarSandboxRefusedInProduction(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		extra map[string]string
	}{
		{"sandbox flag", map[string]string{"FAAS_POLAR_SANDBOX": "true"}},
		{"sandbox base URL", map[string]string{"FAAS_POLAR_BASE_URL": "https://sandbox-api.polar.sh"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := polarSandboxEnv(t, "Production", tc.extra)
			if _, _, err := LoadProviderForAPID(context.Background(), resolveCfg(t, env), env, discardLog()); !errors.Is(err, ErrPolarSandboxInProduction) {
				t.Fatalf("apid err = %v, want ErrPolarSandboxInProduction", err)
			}
			if _, _, err := LoadProviderForMeterd(resolveCfg(t, env), env, state.NewMemStore(), discardLog()); !errors.Is(err, ErrPolarSandboxInProduction) {
				t.Fatalf("meterd err = %v, want ErrPolarSandboxInProduction", err)
			}
		})
	}
}

func TestPolarSandboxAllowedOutsideProduction(t *testing.T) {
	t.Parallel()
	for _, environment := range []string{"", "staging"} {
		env := polarSandboxEnv(t, environment, map[string]string{"FAAS_POLAR_SANDBOX": "1"})
		if _, _, err := LoadProviderForAPID(context.Background(), resolveCfg(t, env), env, discardLog()); err != nil {
			t.Fatalf("FAAS_ENVIRONMENT=%q: err = %v, want nil", environment, err)
		}
	}
}
