// adr: 20261025094915 — amendment 2026-11-05: use is the ledger plus each
// scope's reported use since the ledger's coverage; quotas stay on the
// provider's counters as a backstop.
package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

type capReportTestProvider struct {
	*capTestProvider
	reported map[string]int64
	from     map[string]time.Time
	err      error
}

func (p *capReportTestProvider) ComputeUnitSecondsSince(_ context.Context, id string, from time.Time) (int64, error) {
	if p.err != nil {
		return 0, p.err
	}
	p.from[id] = from
	return p.reported[id], nil
}

// capReportTestStore adds the ledger's coverage to capTestStore's snapshot
// and records cap events.
type capReportTestStore struct {
	*capTestStore
	coverage []UsageProgress
	events   []string
}

func (s *capReportTestStore) RecordCapEvent(_ context.Context, event CapEvent) (bool, error) {
	s.events = append(s.events, event.Threshold)
	return true, nil
}

func (s *capReportTestStore) CapSuspendedAt(context.Context, string, time.Time) (*time.Time, error) {
	return nil, nil
}

func (s *capReportTestStore) MostSevereCapEvent(context.Context, string, time.Time) (*CapEvent, error) {
	return nil, nil
}

func (s *capReportTestStore) UsageSnapshot(ctx context.Context, accountID string, periodStart time.Time) (UsageSnapshot, error) {
	snapshot, err := s.capTestStore.UsageSnapshot(ctx, accountID, periodStart)
	snapshot.Databases = s.coverage
	return snapshot, err
}

func TestCapEnforcerCountsReportedUse(t *testing.T) {
	const hour = int64(3600)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 5, 11, 22, 0, 0, time.UTC)
	collected := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		created    time.Time
		coverage   []UsageProgress
		ledger     int64
		counter    int64
		reported   int64
		reportErr  error
		wantFrom   time.Time
		wantQuota  int64
		wantEvents []string
	}{
		{name: "use since the ledger's coverage adds to it", created: month, ledger: 2 * hour, counter: 600, reported: 1800,
			coverage: []UsageProgress{{DatabaseID: "db-a", CollectedUntil: collected}, {DatabaseID: "db-a-restore", CollectedUntil: collected.Add(-time.Hour)}},
			wantFrom: collected, wantQuota: 600 + 5*hour - 2*hour - 1800},
		{name: "a stale counter cannot hide use past the ceiling", created: month, ledger: 4 * hour, counter: 600, reported: hour,
			coverage: []UsageProgress{{DatabaseID: "db-a", CollectedUntil: collected}},
			wantFrom: collected, wantQuota: 600, wantEvents: []string{CapThresholdWarning, CapThresholdAllowance, CapThresholdSuspended}},
		{name: "a database never collected reads from its creation hour", created: time.Date(2026, 11, 5, 9, 41, 0, 0, time.UTC), reported: 600,
			wantFrom: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC), wantQuota: 5*hour - 600},
		{name: "coverage from last month reads from the month start", created: month.AddDate(0, -2, 0), reported: 600,
			coverage: []UsageProgress{{DatabaseID: "db-a", CollectedUntil: month.Add(-time.Hour)}, {DatabaseID: "db-other", CollectedUntil: collected}},
			wantFrom: month, wantQuota: 5*hour - 600},
		{name: "a failed report changes nothing", created: month, reportErr: ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &capReportTestProvider{capTestProvider: &capTestProvider{
				fakeProvider: &fakeProvider{capabilities: testCapabilities()},
				observed: map[string]ComputeCapObservation{
					"proj-a":         {Scope: "a", PeriodStart: month, UsedUnitSeconds: tc.counter, QuotaUnitSeconds: 1},
					"proj-a/br-rest": {Scope: "a", PeriodStart: month, UsedUnitSeconds: tc.counter, QuotaUnitSeconds: 1},
				},
				set: map[string]int64{},
			}, reported: map[string]int64{"proj-a": tc.reported}, from: map[string]time.Time{}, err: tc.reportErr}
			registry := testRegistry(t, provider, func(config *Config) {
				config.Usage = UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
					StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 1 << 40,
					MaxMonthlyComputeUnitSeconds: 1 << 40, MaxMonthlyStorageByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 40}
			})
			backend, err := registry.Default("us-east-1")
			if err != nil {
				t.Fatal(err)
			}
			database := func(id, resource string) Database {
				return Database{ID: id, AccountID: "acct-a", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
					ProviderResourceID: resource, State: StateReady, CreatedAt: tc.created}
			}
			store := &capReportTestStore{capTestStore: &capTestStore{
				databases: []Database{database("db-a", "proj-a"), database("db-a-restore", "proj-a/br-rest")},
				ledger:    map[string]int64{"acct-a": tc.ledger},
			}, coverage: tc.coverage}
			enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
				Mode: CapEnforcementOn, Now: func() time.Time { return now }, Qualification: testCapQualification(backend),
				Ceiling: func(context.Context, string) (CapLimits, error) {
					return CapLimits{Allowance: 5 * hour, Ceiling: 5 * hour}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = enforcer.Enforce(context.Background())
			if tc.reportErr != nil {
				if !errors.Is(err, tc.reportErr) || len(provider.set) != 0 {
					t.Fatalf("err %v, quotas %v; want the report's error and no write", err, provider.set)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := provider.from["proj-a"]; !got.Equal(tc.wantFrom) || len(provider.from) != 1 {
				t.Fatalf("read from %v; want one read for the scope from %v", provider.from, tc.wantFrom)
			}
			if got := provider.set["proj-a"]; got != tc.wantQuota {
				t.Fatalf("quota %d; want %d", got, tc.wantQuota)
			}
			if !slices.Equal(store.events, tc.wantEvents) {
				t.Fatalf("events %v; want %v", store.events, tc.wantEvents)
			}
		})
	}
}
