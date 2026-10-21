package financialfixture_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/loader"
	"github.com/onebox-faas/faas/pkg/e2etest/financialfixture"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// Catalog drift must fail at the real daemon startup boundary before the
// PostgreSQL reconciliation and outage scenarios need to build meterd.
func TestFinancialFixtureCatalogPassesMeterdPreflight(t *testing.T) {
	for _, rate := range []int64{2000, 4000} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			fixture := financialfixture.NewProvider(t, rate)
			values := make(map[string]string)
			for _, entry := range fixture.Env(financial.ComputeMonth(time.Now().UTC())) {
				key, value, _ := strings.Cut(entry, "=")
				values[key] = value
			}
			env := func(key string) string { return values[key] }
			cfg := loader.ApplyBillingEnvOverlay(&loader.RootBillingConfig{}, env)
			provider, name, err := loader.LoadProviderForMeterd(cfg, env, state.NewMemStore(), nil)
			if err != nil {
				t.Fatalf("fixture catalog refused by meterd: %v", err)
			}
			if name != "polar" || provider == nil || !provider.Capabilities().Has(billing.CapEgressUsage) {
				t.Fatal("fixture did not retain the configured live egress contract")
			}
		})
	}
}
