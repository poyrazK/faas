// adr: 20261031165104 — once overage is billed, a subject at its spend cap has
// its databases suspended at what they have used, while its cap events keep
// the plan's ceiling.
package managedpostgres

import (
	"context"
	"testing"
	"time"
)

func TestCapEnforcerSuspendsAtTheSpendCap(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		reached   bool
		wantQuota int64
		spendCap  int
	}{
		{name: "under the spend cap the ceiling holds", wantQuota: 4000 + (36000 - 4000)},
		{name: "at the spend cap the quota drops to its use", reached: true, wantQuota: 4000, spendCap: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &capTestProvider{
				fakeProvider: &fakeProvider{capabilities: testCapabilities()},
				observed:     map[string]ComputeCapObservation{"proj-a": {Scope: "a", PeriodStart: month, UsedUnitSeconds: 4000}},
				set:          map[string]int64{},
			}
			registry := testRegistry(t, provider, func(config *Config) {
				config.Usage = UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
					StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 1 << 40,
					MaxMonthlyComputeUnitSeconds: 1 << 40, MaxMonthlyStorageByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 40}
			})
			backend, err := registry.Default("us-east-1")
			if err != nil {
				t.Fatal(err)
			}
			store := &capTestStore{
				databases: []Database{{ID: "db-a", AccountID: "acct-a", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, ProviderResourceID: "proj-a", State: StateReady}},
				ledger:    map[string]int64{"acct-a": 4000},
			}
			enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
				Mode: CapEnforcementOn, Now: func() time.Time { return now }, Qualification: testCapQualification(backend),
				Ceiling: func(context.Context, string) (CapLimits, error) {
					return CapLimits{Allowance: 9000, Ceiling: 36000, Overage: true, SpendCapReached: tc.reached}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			summary, err := enforcer.Enforce(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := provider.set["proj-a"]; got != tc.wantQuota {
				t.Fatalf("quota = %d, want %d", got, tc.wantQuota)
			}
			if summary.SpendCapAccounts != tc.spendCap {
				t.Fatalf("spend cap accounts = %d, want %d (%+v)", summary.SpendCapAccounts, tc.spendCap, summary)
			}
		})
	}
}
