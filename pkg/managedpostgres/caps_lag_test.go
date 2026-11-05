// adr: 20261025094915 — amendment 2026-11-04: the quotas keep back the
// provider's counter lag, and a suspension it causes is recorded.
package managedpostgres

import (
	"context"
	"slices"
	"testing"
	"time"
)

type capLagTestProvider struct {
	*capTestProvider
	lag   int64
	specs []Spec
}

func (p *capLagTestProvider) CounterLagUnitSeconds(spec Spec) int64 {
	p.specs = append(p.specs, spec)
	return p.lag
}

func TestCapEnforcerKeepsBackTheCounterLag(t *testing.T) {
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := month.Add(20 * 24 * time.Hour)
	for _, tc := range []struct {
		name       string
		used       int64
		wantQuota  int64
		wantEvents []string
	}{
		{"headroom shrinks by the lag", 5000, 8200, nil},
		{"use within the lag of the ceiling suspends", 8500, 8500, []string{CapThresholdWarning, CapThresholdSuspended}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &capLagTestProvider{capTestProvider: &capTestProvider{
				fakeProvider: &fakeProvider{capabilities: testCapabilities()},
				observed:     map[string]ComputeCapObservation{"proj-a": {Scope: "a", PeriodStart: month, UsedUnitSeconds: tc.used, QuotaUnitSeconds: 1}},
				set:          map[string]int64{},
			}, lag: 1800}
			registry := testRegistry(t, provider, func(config *Config) {
				config.Usage = UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
					StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 1 << 40,
					MaxMonthlyComputeUnitSeconds: 1 << 40, MaxMonthlyStorageByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 40}
			})
			backend, err := registry.Default("us-east-1")
			if err != nil {
				t.Fatal(err)
			}
			store := NewMemoryStore()
			spec := testSpec()
			store.databases["db-a"] = Database{ID: "db-a", AccountID: "acct-a", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
				ProviderResourceID: "proj-a", State: StateReady, Spec: spec, CreatedAt: month, UpdatedAt: month}
			enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
				Mode: CapEnforcementOn, Now: func() time.Time { return now }, Qualification: testCapQualification(backend),
				Ceiling: func(context.Context, string) (CapLimits, error) {
					return CapLimits{Allowance: 10000, Ceiling: 10000}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := enforcer.Enforce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := provider.set["proj-a"]; got != tc.wantQuota {
				t.Fatalf("quota %d; want %d", got, tc.wantQuota)
			}
			if len(provider.specs) != 1 || provider.specs[0].Class != spec.Class {
				t.Fatalf("lag asked for %+v; want the database's spec", provider.specs)
			}
			var events []string
			for _, threshold := range capThresholdOrder {
				if _, ok := store.capEvents[memoryCapEventKey{"acct-a", month, threshold}]; ok {
					events = append(events, threshold)
				}
			}
			if !slices.Equal(events, tc.wantEvents) {
				t.Fatalf("events %v; want %v", events, tc.wantEvents)
			}
		})
	}
}
