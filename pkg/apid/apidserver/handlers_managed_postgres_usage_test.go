package apidserver_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestManagedPostgresUsageViewIsCustomerSafe(t *testing.T) {
	observed := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	view := apidserver.ManagedPostgresUsageView(managedpostgres.UsageSummary{
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
	}, api.ManagedPostgresPlanLimits{DatabasesMax: 1, StorageLimitBytes: 10 << 30}, managedpostgres.CapEnforcementShadow)
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

// TestManagedPostgresUsageViewReportsEnforcedDatabaseLimit pins the
// production-us canary display: `postgres usage` printed "ready_databases
// 0 / 10" (the plan maximum) while the operator capped accounts at 3 and
// `postgres capabilities` said 3.
func TestManagedPostgresUsageViewReportsEnforcedDatabaseLimit(t *testing.T) {
	limits := api.ManagedPostgresPlanLimits{DatabasesMax: 10, StorageLimitBytes: 1 << 30}
	cases := []struct {
		name     string
		enforced int
		want     int
	}{
		{name: "operator cap below plan", enforced: 3, want: 3},
		{name: "plan below operator cap", enforced: 25, want: 10},
		{name: "enforced limit unknown", enforced: 0, want: 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := apidserver.ManagedPostgresUsageView(managedpostgres.UsageSummary{DatabaseLimit: tc.enforced}, limits, managedpostgres.CapEnforcementShadow)
			if view.DatabaseLimit != tc.want {
				t.Fatalf("database_limit = %d, want %d", view.DatabaseLimit, tc.want)
			}
		})
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
			if got := apidserver.ManagedPostgresUsageView(tc.summary, limits, managedpostgres.CapEnforcementShadow).GuardrailState; got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestManagedPostgresUsageOperatorViewIncludesNormalizedCostOnly(t *testing.T) {
	period := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	view, err := apidserver.ManagedPostgresUsageOperatorView("00000000-0000-0000-0000-000000000001", managedpostgres.UsageSummary{
		PolicyEnabled: true,
		Fresh:         true,
		EffectivePolicy: managedpostgres.UsagePolicy{
			Enabled: true, MaxMonthlyCostMillicents: 1000,
			MaxMonthlyComputeUnitSeconds: 1000, MaxMonthlyStorageByteSeconds: 10000,
			MaxMonthlyHistoryByteSeconds: 10000, MaxMonthlyEgressBytes: 10000,
			ComputeUnitHourMillicents: 3600,
		},
		Snapshot: managedpostgres.UsageSnapshot{PeriodStart: period, ComputeUnitSeconds: 3600, CostMillicents: 100},
	}, api.ManagedPostgresPlanLimits{DatabasesMax: 1, StorageLimitBytes: 1 << 30}, managedpostgres.CapEnforcementShadow)
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

// adr: 20261025094915 — compute_cap_state reports suspension only while caps
// are enforced, and leaves guardrail_state's values unchanged for old SDKs.
func TestManagedPostgresUsageViewComputeCapState(t *testing.T) {
	const allowance = int64(25 * 3600)
	limits := api.ManagedPostgresPlanLimits{DatabasesMax: 1, StorageLimitBytes: 1 << 30, MonthlyComputeUnitSeconds: allowance}
	summary := func(used int64) managedpostgres.UsageSummary {
		return managedpostgres.UsageSummary{PolicyEnabled: true, Fresh: true,
			EffectivePolicy: managedpostgres.UsagePolicy{Enabled: true, MaxMonthlyComputeUnitSeconds: allowance},
			Snapshot:        managedpostgres.UsageSnapshot{ComputeUnitSeconds: used}}
	}
	for _, tc := range []struct {
		name string
		used int64
		mode managedpostgres.CapEnforcementMode
		want string
	}{
		{name: "under 80% is healthy", used: allowance*4/5 - 1, mode: managedpostgres.CapEnforcementOn, want: "healthy"},
		{name: "80% warns", used: allowance * 4 / 5, mode: managedpostgres.CapEnforcementOn, want: "warning"},
		{name: "the ceiling suspends while enforced", used: allowance, mode: managedpostgres.CapEnforcementOn, want: "suspended"},
		{name: "shadow never reports a suspension", used: allowance * 2, mode: managedpostgres.CapEnforcementShadow, want: "warning"},
		{name: "off never reports a suspension", used: allowance, mode: managedpostgres.CapEnforcementOff, want: "warning"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := apidserver.ManagedPostgresUsageView(summary(tc.used), limits, tc.mode)
			if view.ComputeCapState != tc.want || view.ComputeAllowanceUnitSeconds != allowance || view.ComputeCeilingUnitSeconds != allowance {
				t.Fatalf("compute cap = %q allowance %d ceiling %d; want %q, %d, %d", view.ComputeCapState, view.ComputeAllowanceUnitSeconds, view.ComputeCeilingUnitSeconds, tc.want, allowance, allowance)
			}
			if view.GuardrailState != "healthy" {
				t.Fatalf("guardrail_state changed to %q", view.GuardrailState)
			}
		})
	}
	t.Run("a disabled ledger reports the allowance without a ceiling", func(t *testing.T) {
		view := apidserver.ManagedPostgresUsageView(managedpostgres.UsageSummary{}, limits, managedpostgres.CapEnforcementOn)
		if view.ComputeAllowanceUnitSeconds != allowance || view.ComputeCeilingUnitSeconds != 0 || view.ComputeCapState != "healthy" {
			t.Fatalf("view = %+v", view)
		}
	})
	t.Run("a plan without compute omits the fields", func(t *testing.T) {
		encoded, err := json.Marshal(apidserver.ManagedPostgresUsageView(summary(0), api.ManagedPostgresPlanLimits{DatabasesMax: 1}, managedpostgres.CapEnforcementOn))
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"compute_allowance_unit_seconds", "compute_ceiling_unit_seconds", "compute_cap_state"} {
			if strings.Contains(string(encoded), key) {
				t.Fatalf("%s present without a plan allowance: %s", key, encoded)
			}
		}
	})
}
