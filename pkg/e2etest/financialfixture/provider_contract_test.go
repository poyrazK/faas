package financialfixture_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/loader"
	"github.com/onebox-faas/faas/pkg/e2etest/financialfixture"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// meterd stamps a usage event when it sends it (ADR-20261028101500), so a
// replay after a lost reply or a restart carries a later timestamp. Polar
// counts it as a duplicate of the first event (sandbox, 2026-10-29); the
// fixture must too, while still refusing a replay whose quantity changed.
func TestFinancialFixtureReplayIgnoresDeliveryTimestamp(t *testing.T) {
	fixture := financialfixture.NewProvider(t, 2000)
	ingest := func(timestamp string, mbSeconds int64) (int, string) {
		t.Helper()
		body := fmt.Sprintf(`{"events":[{"external_id":"faas-usage-a-2026-10-28T00:00:00Z","name":"faas_ram_usage","external_customer_id":"a","timestamp":%q,"metadata":{"mb_seconds":%d,"window_start":"2026-10-28T00:00:00Z"}}]}`, timestamp, mbSeconds)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.Server.URL+"/v1/events/ingest", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer cost-acceptance-placeholder")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(out)
	}
	if code, body := ingest("2026-10-29T10:00:00Z", 3600); code != http.StatusOK || !strings.Contains(body, `"inserted":1`) {
		t.Fatalf("first delivery = %d %s", code, body)
	}
	if code, body := ingest("2026-10-29T11:00:00Z", 3600); code != http.StatusOK || !strings.Contains(body, `"duplicates":1`) {
		t.Fatalf("replay with a later timestamp = %d %s, want a duplicate", code, body)
	}
	if code, _ := ingest("2026-10-29T12:00:00Z", 7200); code != http.StatusConflict {
		t.Fatalf("replay with a changed quantity = %d, want 409", code)
	}
	snapshot := fixture.Snapshot()
	if got := snapshot.Events["faas-usage-a-2026-10-28T00:00:00Z"].Timestamp; got != "2026-10-29T10:00:00Z" {
		t.Fatalf("stored timestamp = %s, want the first delivery's", got)
	}
	if len(snapshot.Conflicts) != 1 {
		t.Fatalf("conflicts = %v, want only the changed quantity", snapshot.Conflicts)
	}
}

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
