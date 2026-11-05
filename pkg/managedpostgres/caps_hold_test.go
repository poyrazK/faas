// adr: 20261025094915 — amendment 2026-11-05: the cap sweep holds the
// databases its quotas would suspend, and releases them when room returns.
package managedpostgres

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type capHoldTestProvider struct {
	*capReportTestProvider
	held    map[string]bool
	heldErr error
	changes map[string]bool
}

func (p *capHoldTestProvider) ComputeHeld(_ context.Context, id string) (bool, error) {
	return p.held[id], p.heldErr
}

func (p *capHoldTestProvider) HoldForDeletion(_ context.Context, id string) error {
	p.held[id], p.changes[id] = true, true
	return nil
}

func (p *capHoldTestProvider) ReleaseDeletionHold(_ context.Context, id string) error {
	p.held[id], p.changes[id] = false, false
	return nil
}

func TestCapEnforcerHoldsAndReleases(t *testing.T) {
	const hour = int64(3600)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 5, 11, 22, 0, 0, time.UTC)
	for _, tc := range []struct {
		name        string
		mode        CapEnforcementMode
		planExcess  CapEnforcementMode
		limits      CapLimits
		ledger      int64
		held        map[string]bool
		heldErr     error
		wantChanges map[string]bool
		wantSummary [4]int // held after, applied, released, shadowed
		wantErr     error
	}{
		{name: "nothing left holds every database", mode: CapEnforcementOn, limits: CapLimits{Allowance: 5 * hour, Ceiling: 5 * hour}, ledger: 5 * hour,
			wantChanges: map[string]bool{"proj-a": true, "proj-b": true}, wantSummary: [4]int{2, 2, 0, 0}},
		{name: "a held database stays held", mode: CapEnforcementOn, limits: CapLimits{Allowance: 5 * hour, Ceiling: 5 * hour}, ledger: 5 * hour,
			held: map[string]bool{"proj-a": true}, wantChanges: map[string]bool{"proj-b": true}, wantSummary: [4]int{2, 1, 0, 0}},
		{name: "room releases the holds", mode: CapEnforcementOn, limits: CapLimits{Allowance: 5 * hour, Ceiling: 10 * hour, Overage: true}, ledger: 5 * hour,
			held: map[string]bool{"proj-a": true, "proj-b": true}, wantChanges: map[string]bool{"proj-a": false, "proj-b": false}, wantSummary: [4]int{0, 0, 2, 0}},
		{name: "an inactive subject is held", mode: CapEnforcementOn, limits: CapLimits{Allowance: 5 * hour, Ceiling: 5 * hour, Inactive: true},
			wantChanges: map[string]bool{"proj-a": true, "proj-b": true}, wantSummary: [4]int{2, 2, 0, 0}},
		{name: "an uncapped subject is released", mode: CapEnforcementOn, limits: CapLimits{},
			held: map[string]bool{"proj-a": true}, wantChanges: map[string]bool{"proj-a": false}, wantSummary: [4]int{0, 0, 1, 0}},
		{name: "only the database past its plan is held", mode: CapEnforcementOn, planExcess: CapEnforcementOn,
			limits:      CapLimits{Allowance: 5 * hour, Ceiling: 5 * hour, Entitlement: api.ManagedPostgresPlanLimits{DatabasesMax: 1, DevelopmentAllowed: true}},
			wantChanges: map[string]bool{"proj-b": true}, wantSummary: [4]int{1, 1, 0, 0}},
		{name: "a plan excess only logged holds nothing", mode: CapEnforcementOn, planExcess: CapEnforcementShadow,
			limits: CapLimits{Allowance: 5 * hour, Ceiling: 5 * hour, Entitlement: api.ManagedPostgresPlanLimits{DatabasesMax: 1, DevelopmentAllowed: true}}},
		{name: "shadow only logs", mode: CapEnforcementShadow, limits: CapLimits{Allowance: 5 * hour, Ceiling: 5 * hour}, ledger: 5 * hour,
			held: map[string]bool{"proj-a": true}, wantSummary: [4]int{1, 0, 0, 1}},
		{name: "an unreadable hold fails the subject", mode: CapEnforcementOn, limits: CapLimits{Allowance: 5 * hour, Ceiling: 5 * hour}, ledger: 5 * hour,
			heldErr: ErrUnavailable, wantErr: ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := func(scope string) ComputeCapObservation {
				return ComputeCapObservation{Scope: scope, PeriodStart: month, UsedUnitSeconds: 600, QuotaUnitSeconds: 1}
			}
			provider := &capHoldTestProvider{capReportTestProvider: &capReportTestProvider{capTestProvider: &capTestProvider{
				fakeProvider: &fakeProvider{capabilities: testCapabilities()},
				observed:     map[string]ComputeCapObservation{"proj-a": observed("a"), "proj-b": observed("b"), "proj-new": observed("n")},
				set:          map[string]int64{},
			}, reported: map[string]int64{}, from: map[string]time.Time{}}, held: map[string]bool{}, heldErr: tc.heldErr, changes: map[string]bool{}}
			maps.Copy(provider.held, tc.held)
			registry := testRegistry(t, provider, func(config *Config) {
				config.Usage = UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
					StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 1 << 40,
					MaxMonthlyComputeUnitSeconds: 1 << 40, MaxMonthlyStorageByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 40}
			})
			backend, err := registry.Default("us-east-1")
			if err != nil {
				t.Fatal(err)
			}
			database := func(id, resource string, state State, created time.Time) Database {
				return Database{ID: id, AccountID: "acct-a", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
					ProviderResourceID: resource, State: state, Spec: testSpec(), CreatedAt: created}
			}
			store := &capReportTestStore{capTestStore: &capTestStore{
				databases: []Database{
					database("db-a", "proj-a", StateReady, month),
					database("db-b", "proj-b", StateReady, month.Add(time.Hour)),
					// A database being created is left to its worker.
					database("db-new", "proj-new", StateProvisioning, month.Add(2*time.Hour)),
				},
				ledger: map[string]int64{"acct-a": tc.ledger},
			}}
			enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
				Mode: tc.mode, PlanExcessMode: tc.planExcess, Now: func() time.Time { return now }, Qualification: testCapQualification(backend),
				Ceiling: func(context.Context, string) (CapLimits, error) { return tc.limits, nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			summary, err := enforcer.Enforce(context.Background())
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || len(provider.changes) != 0 {
					t.Fatalf("err %v, changes %v; want %v and no change", err, provider.changes, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(provider.changes, tc.wantChanges) && (len(provider.changes) != 0 || len(tc.wantChanges) != 0) {
				t.Fatalf("changes %v; want %v", provider.changes, tc.wantChanges)
			}
			if got := [4]int{summary.HeldDatabases, summary.HoldsApplied, summary.HoldsReleased, summary.HoldsShadowed}; got != tc.wantSummary {
				t.Fatalf("held, applied, released, shadowed = %v; want %v", got, tc.wantSummary)
			}
		})
	}
}
