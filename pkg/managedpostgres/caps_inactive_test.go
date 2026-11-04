// adr: 20261025094915 — amendment 2026-11-03: an inactive subject's
// databases are suspended, as its apps are parked.
package managedpostgres

import (
	"context"
	"testing"
	"time"
)

// A suspended or abuse-held subject keeps no compute headroom, whatever its
// ceiling, and an uncapped one is suspended too; an active subject keeps its
// quota. Its data is untouched and nothing is mailed for it.
func TestCapEnforcerSuspendsInactiveSubjects(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	provider := &capTestProvider{
		fakeProvider: &fakeProvider{capabilities: testCapabilities()},
		observed: map[string]ComputeCapObservation{
			"proj-active":   {Scope: "a", PeriodStart: month, UsedUnitSeconds: 4000, QuotaUnitSeconds: 0},
			"proj-held":     {Scope: "h", PeriodStart: month, UsedUnitSeconds: 4000, QuotaUnitSeconds: 0},
			"proj-uncapped": {Scope: "u", PeriodStart: month, UsedUnitSeconds: 10, QuotaUnitSeconds: 0},
		},
		set: map[string]int64{},
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
	database := func(id, account, resource string) Database {
		return Database{ID: id, AccountID: account, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, ProviderResourceID: resource, State: StateReady}
	}
	store := &capTestStore{
		databases: []Database{
			database("db-active", "acct-active", "proj-active"),
			database("db-held", "acct-held", "proj-held"),
			database("db-uncapped", "acct-uncapped", "proj-uncapped"),
		},
		ledger: map[string]int64{"acct-active": 4000, "acct-held": 4000, "acct-uncapped": 10},
	}
	limits := map[string]CapLimits{
		"acct-active":   {Allowance: 18000, Ceiling: 18000},
		"acct-held":     {Allowance: 18000, Ceiling: 18000, Inactive: true},
		"acct-uncapped": {Inactive: true},
	}
	enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
		Mode: CapEnforcementOn, Now: func() time.Time { return now }, Qualification: testCapQualification(backend),
		Ceiling: func(_ context.Context, subject string) (CapLimits, error) { return limits[subject], nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := enforcer.Enforce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The active subject keeps its 14000 seconds of headroom; the inactive
	// ones are held at what they used.
	want := map[string]int64{"proj-active": 18000, "proj-held": 4000, "proj-uncapped": 10}
	for id, quota := range want {
		if provider.set[id] != quota {
			t.Errorf("%s quota = %d, want %d (all %v)", id, provider.set[id], quota, provider.set)
		}
	}
	if summary.SuspendedAccounts != 2 || summary.Uncapped != 0 {
		t.Fatalf("summary %+v; want both inactive subjects suspended and none left uncapped", summary)
	}
}
