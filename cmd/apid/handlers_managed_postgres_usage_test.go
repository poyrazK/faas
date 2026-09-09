package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestManagedPostgresUsageViewIsCustomerSafe(t *testing.T) {
	observed := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	view := managedPostgresUsageView(managedpostgres.UsageSummary{
		PolicyEnabled: true,
		Fresh:         true,
		Snapshot: managedpostgres.UsageSnapshot{
			PeriodStart:        time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			LastObservedAt:     observed,
			ReadyDatabases:     1,
			ComputeUnitSeconds: 30,
			StorageByteSeconds: 40,
			HistoryByteSeconds: 50,
			EgressBytes:        60,
			CostMillicents:     999,
		},
	}, api.ManagedPostgresPlanLimits{DatabasesMax: 1, StorageLimitBytes: 10 << 30})
	if view.GuardrailState != "healthy" || view.StorageByteSecondsRemaining <= 0 || view.ObservedAt == nil {
		t.Fatalf("view = %+v", view)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"cost_millicents", "backend_id", "provider", "connection_url", "password"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("customer usage response contains %q: %s", forbidden, encoded)
		}
	}
}

func TestManagedPostgresUsageViewGuardrailStates(t *testing.T) {
	limits := api.ManagedPostgresPlanLimits{DatabasesMax: 1, StorageLimitBytes: 1 << 30}
	for _, tc := range []struct {
		name    string
		summary managedpostgres.UsageSummary
		want    string
	}{
		{name: "disabled", summary: managedpostgres.UsageSummary{}, want: "disabled"},
		{name: "stale", summary: managedpostgres.UsageSummary{PolicyEnabled: true, Fresh: false}, want: "stale"},
		{name: "reached", summary: managedpostgres.UsageSummary{PolicyEnabled: true, Fresh: true, Exceeded: true}, want: "reached"},
		{name: "healthy", summary: managedpostgres.UsageSummary{PolicyEnabled: true, Fresh: true}, want: "healthy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := managedPostgresUsageView(tc.summary, limits).GuardrailState; got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestManagedPostgresUsageOperatorViewIncludesNormalizedCostOnly(t *testing.T) {
	period := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	view, err := managedPostgresUsageOperatorView("00000000-0000-0000-0000-000000000001", managedpostgres.UsageSummary{
		PolicyEnabled: true,
		Fresh:         true,
		EffectivePolicy: managedpostgres.UsagePolicy{
			Enabled: true, MaxMonthlyCostMillicents: 1000,
			MaxMonthlyComputeUnitSeconds: 1000, MaxMonthlyStorageByteSeconds: 10000,
			MaxMonthlyHistoryByteSeconds: 10000, MaxMonthlyEgressBytes: 10000,
			ComputeUnitHourMillicents: 3600,
		},
		Snapshot: managedpostgres.UsageSnapshot{PeriodStart: period, ComputeUnitSeconds: 3600, CostMillicents: 100},
	}, api.ManagedPostgresPlanLimits{DatabasesMax: 1, StorageLimitBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	if view.AccountID == "" || view.CostMillicents != 100 || len(view.LineItems) != 1 || view.LineItems[0].Code != "managed_postgres.compute" {
		t.Fatalf("operator view = %+v", view)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"provider_resource_id", "connection_url", "password"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("operator usage response contains %q: %s", forbidden, encoded)
		}
	}
}
