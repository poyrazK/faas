// adr: 20261110160000 — database egress counts toward the plan's monthly
// egress allowance with the subject's app egress; the cap sweep holds every
// database of a subject that spent it.
package managedpostgres

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"
)

type capEgressTestStore struct {
	*capReportTestStore
	egress int64
}

func (s *capEgressTestStore) UsageSnapshot(ctx context.Context, accountID string, periodStart time.Time) (UsageSnapshot, error) {
	snapshot, err := s.capReportTestStore.UsageSnapshot(ctx, accountID, periodStart)
	snapshot.EgressBytes = s.egress
	return snapshot, err
}

func TestCapEnforcerHoldsSpentEgress(t *testing.T) {
	const hour = int64(3600)
	const gib = int64(1 << 30)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 5, 11, 22, 0, 0, time.UTC)
	room := CapLimits{Allowance: 5 * hour, Ceiling: 10 * hour}
	errRead := errors.New("usage unavailable")
	for _, tc := range []struct {
		name        string
		limits      CapLimits
		dbEgress    int64
		allowance   EgressAllowance
		allowErr    error
		held        map[string]bool
		wantChanges map[string]bool
		wantErr     error
	}{
		{name: "databases and apps together spent the allowance", limits: room, dbEgress: 6 * gib,
			allowance:   EgressAllowance{CeilingBytes: 10 * gib, AppEgressBytes: 4 * gib},
			wantChanges: map[string]bool{"proj-a": true, "proj-b": true}},
		{name: "under the allowance holds nothing", limits: room, dbEgress: 5 * gib,
			allowance: EgressAllowance{CeilingBytes: 10 * gib, AppEgressBytes: 4 * gib}},
		{name: "a subject without a compute ceiling is still held", limits: CapLimits{}, dbEgress: 10 * gib,
			allowance:   EgressAllowance{CeilingBytes: 10 * gib},
			wantChanges: map[string]bool{"proj-a": true, "proj-b": true}},
		{name: "lifted egress overage releases the hold", limits: room, dbEgress: 50 * gib,
			allowance: EgressAllowance{}, held: map[string]bool{"proj-a": true, "proj-b": true},
			wantChanges: map[string]bool{"proj-a": false, "proj-b": false}},
		{name: "an unreadable allowance fails the subject", limits: room, dbEgress: 50 * gib,
			allowErr: errRead, wantErr: errRead},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := func(scope string) ComputeCapObservation {
				return ComputeCapObservation{Scope: scope, PeriodStart: month, UsedUnitSeconds: 600, QuotaUnitSeconds: 1}
			}
			provider := &capHoldTestProvider{capReportTestProvider: &capReportTestProvider{capTestProvider: &capTestProvider{
				fakeProvider: &fakeProvider{capabilities: testCapabilities()},
				observed:     map[string]ComputeCapObservation{"proj-a": observed("a"), "proj-b": observed("b")},
				set:          map[string]int64{},
			}, reported: map[string]int64{}, from: map[string]time.Time{}}, held: map[string]bool{}, changes: map[string]bool{}}
			maps.Copy(provider.held, tc.held)
			registry := testRegistry(t, provider, func(config *Config) {
				config.Usage = UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
					StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 1 << 40,
					MaxMonthlyComputeUnitSeconds: 1 << 40, MaxMonthlyStorageByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 60}
			})
			backend, err := registry.Default("us-east-1")
			if err != nil {
				t.Fatal(err)
			}
			database := func(id, resource string, created time.Time) Database {
				return Database{ID: id, AccountID: "acct-a", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
					ProviderResourceID: resource, State: StateReady, Spec: testSpec(), CreatedAt: created}
			}
			store := &capEgressTestStore{capReportTestStore: &capReportTestStore{capTestStore: &capTestStore{
				databases: []Database{database("db-a", "proj-a", month), database("db-b", "proj-b", month.Add(time.Hour))},
				ledger:    map[string]int64{"acct-a": hour},
			}}, egress: tc.dbEgress}
			enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
				Mode: CapEnforcementOn, Now: func() time.Time { return now }, Qualification: testCapQualification(backend),
				Ceiling: func(context.Context, string) (CapLimits, error) { return tc.limits, nil },
				Egress: func(_ context.Context, subject string, at time.Time) (EgressAllowance, error) {
					if subject != "acct-a" || !at.Equal(now) {
						t.Errorf("egress read for %q at %v, want acct-a at %v", subject, at, now)
					}
					return tc.allowance, tc.allowErr
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = enforcer.Enforce(context.Background())
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
		})
	}
}
