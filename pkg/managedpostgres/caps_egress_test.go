// adr: 20261110160000 — amendment 2026-11-11: each database has its own
// monthly egress allowance, as Neon's is per project; the cap sweep holds a
// database that sent it, and only that one. Amendment 2026-11-13: each held
// database is recorded and mailed on its own, naming it.
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
	egress map[string]int64
	// held are the month's egress hold notices by database, as
	// EgressHoldNoticeStore reads them.
	held map[string]*CapEvent
}

func (s *capEgressTestStore) RecordCapEvent(ctx context.Context, event CapEvent) (bool, error) {
	if event.Threshold == CapThresholdEgressHeld {
		return false, errors.New("egress hold recorded per subject")
	}
	return s.capReportTestStore.RecordCapEvent(ctx, event)
}

func (s *capEgressTestStore) RecordEgressHoldNotice(_ context.Context, event CapEvent) (bool, error) {
	if !validEgressHoldNotice(event) {
		return false, ErrInvalid
	}
	if s.held == nil {
		s.held = map[string]*CapEvent{}
	}
	if s.held[event.DatabaseID] != nil {
		return false, nil
	}
	event.DatabaseName = ""
	s.held[event.DatabaseID] = &event
	return true, nil
}

func (s *capEgressTestStore) EgressHoldNotice(_ context.Context, databaseID string, _ time.Time) (*CapEvent, error) {
	held := s.held[databaseID]
	if held == nil {
		return nil, nil
	}
	copied := *held
	return &copied, nil
}

func (s *capEgressTestStore) UsageSnapshot(ctx context.Context, accountID string, periodStart time.Time) (UsageSnapshot, error) {
	snapshot, err := s.capReportTestStore.UsageSnapshot(ctx, accountID, periodStart)
	snapshot.EgressBytes, snapshot.DatabaseEgress = 0, s.egress
	for _, egress := range s.egress {
		snapshot.EgressBytes += egress
	}
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
		dbEgress    map[string]int64
		allowance   EgressAllowance
		allowErr    error
		billingLive bool
		held        map[string]bool
		wantChanges map[string]bool
		wantErr     error
	}{
		{name: "a database past its own allowance is held alone", limits: room, dbEgress: map[string]int64{"db-a": 10 * gib, "db-b": 2 * gib},
			allowance:   EgressAllowance{CeilingBytes: 10 * gib},
			wantChanges: map[string]bool{"proj-a": true}},
		{name: "databases under their own allowances hold nothing together", limits: room, dbEgress: map[string]int64{"db-a": 9 * gib, "db-b": 9 * gib},
			allowance: EgressAllowance{CeilingBytes: 10 * gib}},
		{name: "a subject without a compute ceiling is still held", limits: CapLimits{}, dbEgress: map[string]int64{"db-a": 10 * gib, "db-b": 11 * gib},
			allowance:   EgressAllowance{CeilingBytes: 10 * gib},
			wantChanges: map[string]bool{"proj-a": true, "proj-b": true}},
		{name: "lifted egress overage releases the hold", limits: room, dbEgress: map[string]int64{"db-a": 50 * gib, "db-b": 50 * gib},
			allowance: EgressAllowance{}, held: map[string]bool{"proj-a": true, "proj-b": true},
			wantChanges: map[string]bool{"proj-a": false, "proj-b": false}},
		{name: "billed egress overage holds at twice the allowance, not at it", limits: room, dbEgress: map[string]int64{"db-a": 19 * gib}, billingLive: true,
			allowance: EgressAllowance{CeilingBytes: 10 * gib, OverageLifted: true}},
		{name: "billed egress overage still holds past twice the allowance", limits: room, dbEgress: map[string]int64{"db-a": 20 * gib, "db-b": 19 * gib}, billingLive: true,
			allowance:   EgressAllowance{CeilingBytes: 10 * gib, OverageLifted: true},
			wantChanges: map[string]bool{"proj-a": true}},
		{name: "an election not billed live keeps the hold at the allowance", limits: room, dbEgress: map[string]int64{"db-a": 10 * gib},
			allowance:   EgressAllowance{CeilingBytes: 10 * gib, OverageLifted: true},
			wantChanges: map[string]bool{"proj-a": true}},
		{name: "live billing without an election keeps the hold at the allowance", limits: room, dbEgress: map[string]int64{"db-a": 10 * gib}, billingLive: true,
			allowance:   EgressAllowance{CeilingBytes: 10 * gib},
			wantChanges: map[string]bool{"proj-a": true}},
		{name: "an unreadable allowance fails the subject", limits: room, dbEgress: map[string]int64{"db-a": 50 * gib},
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
				return Database{ID: id, Name: "name-" + id, AccountID: "acct-a", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
					ProviderResourceID: resource, State: StateReady, Spec: testSpec(), CreatedAt: created}
			}
			store := &capEgressTestStore{capReportTestStore: &capReportTestStore{capTestStore: &capTestStore{
				databases: []Database{database("db-a", "proj-a", month), database("db-b", "proj-b", month.Add(time.Hour))},
				ledger:    map[string]int64{"acct-a": hour},
			}}, egress: tc.dbEgress}
			var mailed []CapEvent
			enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
				Notify: func(_ context.Context, event CapEvent) error {
					mailed = append(mailed, event)
					if held := store.held[event.DatabaseID]; held != nil {
						held.Mailed = true
					}
					return nil
				},
				Mode: CapEnforcementOn, Now: func() time.Time { return now }, Qualification: testCapQualification(backend),
				EgressBillingLive: tc.billingLive,
				Ceiling:           func(context.Context, string) (CapLimits, error) { return tc.limits, nil },
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
			// Each held database is recorded and mailed once, naming it, with
			// the egress it sent and the hold ceiling it was held at.
			if _, err := enforcer.Enforce(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := tc.allowance.CeilingBytes
			if tc.billingLive && tc.allowance.OverageLifted {
				want *= 2
			}
			wantMailed := map[string]bool{}
			for id, proj := range map[string]string{"db-a": "proj-a", "db-b": "proj-b"} {
				if tc.wantChanges[proj] {
					wantMailed[id] = true
				}
			}
			if len(mailed) != len(wantMailed) || len(store.held) != len(wantMailed) {
				t.Fatalf("mailed %+v, notices %v; want one email for each of %v", mailed, store.held, wantMailed)
			}
			for _, event := range mailed {
				if !wantMailed[event.DatabaseID] || event.DatabaseName != "name-"+event.DatabaseID || event.SubjectID != "acct-a" ||
					event.Threshold != CapThresholdEgressHeld || event.Used != tc.dbEgress[event.DatabaseID] ||
					event.Allowance != tc.allowance.CeilingBytes || event.Ceiling != want || event.Overage != (want != tc.allowance.CeilingBytes) {
					t.Fatalf("mailed %+v; want %s's egress hold at %d of %d", event, event.DatabaseID, tc.dbEgress[event.DatabaseID], want)
				}
				delete(wantMailed, event.DatabaseID)
			}
		})
	}
}
