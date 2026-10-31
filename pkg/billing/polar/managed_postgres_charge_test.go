// adr: 20261031110037 — managed PostgreSQL overage charges reach Polar as
// idempotent events on their own charge meter.
package polar

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
)

func managedPostgresTestConfig(baseURL, mode string) Config {
	cfg := testConfig(baseURL)
	cfg.ManagedPostgresBillingMode = mode
	cfg.ManagedPostgresBillingFrom = "2026-11-01T00:00:00Z"
	cfg.ManagedPostgresUsageEventName = "managed_postgres_usage"
	cfg.ManagedPostgresMeterID = "meter-managed-postgres"
	return cfg
}

func TestManagedPostgresBillingDefaultsOffAndValidatesItsConfig(t *testing.T) {
	p, err := NewProvider(testConfig("http://example.test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.ManagedPostgresChargePolicy(); ok || p.Capabilities().Has(billing.CapManagedPostgresUsage) {
		t.Fatal("the default provider charges managed postgres overage")
	}
	for name, edit := range map[string]func(*Config){
		"mid-month activation": func(c *Config) { c.ManagedPostgresBillingFrom = "2026-11-02T00:00:00Z" },
		"offset activation":    func(c *Config) { c.ManagedPostgresBillingFrom = "2026-11-01T03:00:00+03:00" },
		"no meter":             func(c *Config) { c.ManagedPostgresMeterID = "" },
		"compute meter reused": func(c *Config) { c.ManagedPostgresMeterID = c.MeterID },
		"event reused":         func(c *Config) { c.ManagedPostgresUsageEventName = c.UsageEventName },
		"unknown mode":         func(c *Config) { c.ManagedPostgresBillingMode = "on" },
	} {
		cfg := managedPostgresTestConfig("http://example.test", ManagedPostgresBillingShadow)
		edit(&cfg)
		if _, err := NewProvider(cfg, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	shadow, err := NewProvider(managedPostgresTestConfig("http://example.test", ManagedPostgresBillingShadow), nil)
	if err != nil {
		t.Fatal(err)
	}
	policy, ok := shadow.ManagedPostgresChargePolicy()
	if !ok || policy.Live || policy.Provider != "polar" || !policy.EffectiveFrom.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("shadow policy = %+v, %v", policy, ok)
	}
	if err := shadow.PublishManagedPostgresCharge(context.Background(), billing.ManagedPostgresChargeStep{SubjectID: "acct", PeriodStart: policy.EffectiveFrom, At: time.Now(), TargetMillicents: 1}); err == nil {
		t.Fatal("a shadow provider published a charge")
	}
}

func TestPublishManagedPostgresChargeSendsTheStepDifference(t *testing.T) {
	var got usageEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body ingestRequest
		if r.URL.Path != "/v1/events/ingest" || json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Events) != 1 {
			http.Error(w, "bad event", http.StatusBadRequest)
			return
		}
		got = body.Events[0]
		_, _ = io.WriteString(w, `{"inserted":1,"duplicates":0}`)
	}))
	defer server.Close()
	p, err := NewProvider(managedPostgresTestConfig(server.URL, ManagedPostgresBillingLive), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Capabilities().Has(billing.CapManagedPostgresUsage) {
		t.Fatal("a live provider lacks the managed postgres capability")
	}
	period := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	at := period.Add(10 * 24 * time.Hour)
	for _, bad := range []billing.ManagedPostgresChargeStep{
		{SubjectID: "acct", PeriodStart: period, At: at, DeliveredMillicents: 40_000, TargetMillicents: 40_000},
		{SubjectID: "acct", PeriodStart: period.Add(time.Hour), At: at, TargetMillicents: 1},
		{PeriodStart: period, At: at, TargetMillicents: 1},
	} {
		if err := p.PublishManagedPostgresCharge(context.Background(), bad); err == nil {
			t.Fatalf("invalid step %+v was published", bad)
		}
	}
	step := billing.ManagedPostgresChargeStep{SubjectID: "acct", PeriodStart: period, At: at, UsedUnitSeconds: 93600,
		AllowanceSeconds: 90000, BilledUnitSeconds: 3600 * 3, DeliveredMillicents: 40_000, TargetMillicents: 60_000}
	if err := p.PublishManagedPostgresCharge(context.Background(), step); err != nil {
		t.Fatal(err)
	}
	if got.Name != "managed_postgres_usage" || got.ExternalID != "faas-managed-postgres-acct-2026-11-60000" || got.ExternalCustomerID != "acct" || got.Timestamp != at.Format(time.RFC3339) {
		t.Fatalf("event = %+v", got)
	}
	if got.Metadata["charge_millicents"] != float64(20_000) || got.Metadata["cumulative_millicents"] != float64(60_000) ||
		got.Metadata["millicents_per_unit_hour"] != float64(api.ManagedPostgresOverageMillicentsPerComputeUnitHour) || got.Metadata["billed_unit_seconds"] != float64(10800) {
		t.Fatalf("metadata = %+v", got.Metadata)
	}
}

func TestEnsurePlanProductsValidatesTheManagedPostgresMeter(t *testing.T) {
	meterAggregation := "charge_millicents"
	server := httptest.NewServer(withOrganization(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/meters/meter-1":
			_, _ = io.WriteString(w, catalogMeterJSON())
			return
		case "/v1/meters/meter-managed-postgres":
			_, _ = io.WriteString(w, `{"id":"meter-managed-postgres","unit":"scalar","archived_at":null,"filter":{"conjunction":"and","clauses":[{"property":"name","operator":"eq","value":"managed_postgres_usage"}]},"aggregation":{"func":"sum","property":"`+meterAggregation+`"}}`)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/products/") {
			id := strings.TrimPrefix(r.URL.Path, "/v1/products/")
			fixed := map[string]int64{"pro-product": 3000, "scale-product": 9900}[id]
			if fixed == 0 {
				fixed = 1000
			}
			product := strings.Replace(catalogProductJSON(id, fixed), `],"benefits"`, `,{"amount_type":"metered_unit","price_currency":"usd","unit_amount":"0.001","meter_id":"meter-managed-postgres","cap_amount":null,"is_archived":false}],"benefits"`, 1)
			_, _ = io.WriteString(w, product)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	for _, mode := range []string{ManagedPostgresBillingLive, ManagedPostgresBillingShadow, ManagedPostgresBillingOff} {
		p, err := NewProvider(managedPostgresTestConfig(server.URL, mode), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.EnsurePlanProducts(context.Background()); err != nil {
			t.Fatalf("mode %s: %v", mode, err)
		}
	}
	meterAggregation = "gb_ram_hours"
	p, err := NewProvider(managedPostgresTestConfig(server.URL, ManagedPostgresBillingLive), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.EnsurePlanProducts(context.Background()); err == nil {
		t.Fatal("a managed postgres meter summing the wrong property was accepted")
	}
}
