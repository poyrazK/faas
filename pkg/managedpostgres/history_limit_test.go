// adr: 20261123062111 — D2: a subject with overage off whose restore history
// passes twice its included history keeps a 24-hour restore window until the
// month ends or overage is turned on.
package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const weekSeconds = int64(7 * 24 * 3600)

func TestParseHistoryLimitMode(t *testing.T) {
	for value, want := range map[string]CapEnforcementMode{"": CapEnforcementShadow, " Shadow ": CapEnforcementShadow, "off": CapEnforcementOff, "ON": CapEnforcementOn} {
		if got, err := ParseHistoryLimitMode(value); err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", value, got, err, want)
		}
	}
	if _, err := ParseHistoryLimitMode("enforce"); !errors.Is(err, ErrInvalid) {
		t.Error("an unknown mode was accepted")
	}
}

func TestHistoryLimited(t *testing.T) {
	pool := int64(1 << 30) // Hobby: 1 GiB included.
	limit := HistoryLimitByteSeconds(pool)
	if want := 2 * pool * api.ManagedPostgresHistoryMonthSeconds; limit != want {
		t.Fatalf("limit = %d, want twice the included history held for the month, %d", limit, want)
	}
	for _, tc := range []struct {
		name               string
		history, snapshots int64
		pool               int64
		overage            bool
		want               bool
	}{
		{name: "below the limit", history: limit - 1, pool: pool},
		{name: "at the limit", history: limit, pool: pool, want: true},
		{name: "snapshots count with restore history", history: limit / 2, snapshots: limit / 2, pool: pool, want: true},
		{name: "overage on pays for history", history: 2 * limit, pool: pool, overage: true},
		{name: "an unknown plan is not limited", history: 2 * limit, pool: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := UsageSnapshot{HistoryByteSeconds: tc.history, SnapshotByteSeconds: tc.snapshots}
			if got := HistoryLimited(snapshot, tc.pool, tc.overage); got != tc.want {
				t.Fatalf("limited = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSpecForObservationAcceptsTheLimitedRestoreWindow(t *testing.T) {
	expected := testSpec()
	expected.RestoreWindowSeconds = weekSeconds
	for _, tc := range []struct {
		name   string
		window int64
		want   int64
	}{
		{name: "the spec's window", window: weekSeconds, want: weekSeconds},
		{name: "the limited window", window: historyLimitedSeconds, want: weekSeconds},
		{name: "another shorter window is drift", window: 3 * 24 * 3600, want: 3 * 24 * 3600},
		{name: "a longer window is drift", window: 2 * weekSeconds, want: 2 * weekSeconds},
		{name: "no window is drift", window: 0, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := expected
			observed.RestoreWindowSeconds = tc.window
			if got := SpecForObservation(expected, observed); got.RestoreWindowSeconds != tc.want {
				t.Fatalf("restore window %d, want %d", got.RestoreWindowSeconds, tc.want)
			}
		})
	}
	observed := expected
	observed.RestoreWindowSeconds, observed.Class = historyLimitedSeconds, ClassProduction
	if got := SpecForObservation(expected, observed); got.Class != ClassProduction {
		t.Fatal("the limited window hid a class drift")
	}
}

type historyTestProvider struct {
	*capTestProvider
	retention map[string]HistoryRetentionObservation
	set       map[string]int64
	setErr    map[string]error
}

func (p *historyTestProvider) ObserveHistoryRetention(_ context.Context, id string) (HistoryRetentionObservation, error) {
	observed, ok := p.retention[id]
	if !ok {
		return HistoryRetentionObservation{}, ErrUnavailable
	}
	return observed, nil
}

func (p *historyTestProvider) SetHistoryRetention(_ context.Context, id string, seconds int64) error {
	if err := p.setErr[id]; err != nil {
		return err
	}
	p.set[id] = seconds
	return nil
}

type historyTestStore struct {
	*capTestStore
	history map[string]int64
}

func (s *historyTestStore) UsageSnapshot(ctx context.Context, subject string, at time.Time) (UsageSnapshot, error) {
	snapshot, err := s.capTestStore.UsageSnapshot(ctx, subject, at)
	snapshot.HistoryByteSeconds = s.history[subject]
	return snapshot, err
}

// The history pass runs beside the compute and storage passes: shadow writes
// nothing, on limits a subject over its limit with overage off and gives the
// spec's window back to one that turned overage on, and a window that is
// neither its spec's nor the limited one is drift it leaves alone.
func TestCapEnforcerHistoryLimit(t *testing.T) {
	now := time.Date(2026, 12, 20, 12, 0, 0, 0, time.UTC)
	pool := int64(1 << 30)
	over := HistoryLimitByteSeconds(pool)
	run := func(t *testing.T, mode CapEnforcementMode, setErr map[string]error) (*historyTestProvider, CapSweepSummary) {
		t.Helper()
		provider := &historyTestProvider{
			capTestProvider: &capTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}, observed: map[string]ComputeCapObservation{}, set: map[string]int64{}},
			retention: map[string]HistoryRetentionObservation{
				"proj-o":         {Scope: "o", Seconds: weekSeconds},
				"proj-o/br-rest": {Scope: "o", Seconds: weekSeconds},
				"proj-n":         {Scope: "n", Seconds: historyLimitedSeconds},
				"proj-u":         {Scope: "u", Seconds: weekSeconds},
				"proj-d":         {Scope: "d", Seconds: 3 * 24 * 3600},
				"proj-x":         {Scope: "x", Seconds: weekSeconds},
			},
			set: map[string]int64{}, setErr: setErr,
		}
		registry := testRegistry(t, provider, nil)
		backend, err := registry.Default("us-east-1")
		if err != nil {
			t.Fatal(err)
		}
		database := func(id, subject, resource string) Database {
			d := Database{ID: id, AccountID: subject, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
				ProviderResourceID: resource, State: StateReady, Spec: testSpec()}
			d.Spec.RestoreWindowSeconds = weekSeconds
			return d
		}
		pitrOff := database("db-x", "acct-o", "proj-x")
		pitrOff.Spec.RestoreWindowSeconds = 0
		store := &historyTestStore{capTestStore: &capTestStore{databases: []Database{
			database("db-o", "acct-o", "proj-o"),
			database("db-o-restore", "acct-o", "proj-o/br-rest"),
			pitrOff,
			database("db-n", "acct-n", "proj-n"),
			database("db-u", "acct-u", "proj-u"),
			database("db-d", "acct-d", "proj-d"),
		}}, history: map[string]int64{"acct-o": over, "acct-n": 3 * over, "acct-u": over - 1, "acct-d": over}}
		overage := map[string]bool{"acct-n": true}
		enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
			Mode: CapEnforcementOff, StorageMode: CapEnforcementOff, HistoryMode: mode, Now: func() time.Time { return now },
			Ceiling: func(_ context.Context, subject string) (CapLimits, error) {
				return CapLimits{StorageAllowance: pool, Overage: overage[subject]}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		summary, err := enforcer.Enforce(context.Background())
		if err != nil {
			t.Fatalf("Enforce = %v; a history failure must not fail the sweep", err)
		}
		if summary.Accounts != 0 || summary.StorageAccounts != 0 || summary.HistorySubjects != 4 || summary.HistoryMode != mode {
			t.Fatalf("the history pass did not run on its own: %+v", summary)
		}
		return provider, summary
	}

	t.Run("shadow", func(t *testing.T) {
		provider, summary := run(t, CapEnforcementShadow, nil)
		if len(provider.set) != 0 || summary.HistoryScopesShadowed != 2 || summary.HistoryScopesApplied != 0 ||
			summary.HistoryScopesUnchanged != 1 || summary.HistoryScopesDrift != 1 || summary.HistoryLimitedSubjects != 2 || summary.HistoryFailed != 0 {
			t.Fatalf("shadow wrote %v; summary %+v", provider.set, summary)
		}
	})

	t.Run("on limits and restores windows", func(t *testing.T) {
		provider, summary := run(t, CapEnforcementOn, nil)
		want := map[string]int64{"proj-o": historyLimitedSeconds, "proj-n": weekSeconds}
		if fmt.Sprint(provider.set) != fmt.Sprint(want) {
			t.Fatalf("windows written %v; want %v (a shared project once, PITR-off and drifted ones never)", provider.set, want)
		}
		if summary.HistoryScopesApplied != 2 || summary.HistoryScopesShadowed != 0 || summary.HistoryScopesDrift != 1 {
			t.Fatalf("summary %+v", summary)
		}
	})

	t.Run("a failed write fails only its subject's history pass", func(t *testing.T) {
		provider, summary := run(t, CapEnforcementOn, map[string]error{"proj-o": ErrUnavailable})
		if provider.set["proj-n"] != weekSeconds || summary.HistoryScopesApplied != 1 || summary.HistoryFailed != 1 {
			t.Fatalf("windows written %v; summary %+v", provider.set, summary)
		}
	})

	t.Run("off skips the pass", func(t *testing.T) {
		registry := testRegistry(t, &fakeProvider{capabilities: testCapabilities()}, nil)
		enforcer, err := NewCapEnforcer(registry, &capTestStore{}, CapEnforcerOptions{Mode: CapEnforcementOff, StorageMode: CapEnforcementOff,
			Ceiling: func(context.Context, string) (CapLimits, error) { return CapLimits{}, nil }})
		if err != nil {
			t.Fatal(err)
		}
		if summary, err := enforcer.Enforce(context.Background()); err != nil || summary.HistorySubjects != 0 || summary.HistoryMode != CapEnforcementOff {
			t.Fatalf("summary %+v, err %v", summary, err)
		}
	})
}

type historyRestoreProvider struct {
	*fakeProvider
	retention int64
}

func (p *historyRestoreProvider) ObserveHistoryRetention(_ context.Context, id string) (HistoryRetentionObservation, error) {
	return HistoryRetentionObservation{Scope: id, Seconds: p.retention}, nil
}

func (p *historyRestoreProvider) SetHistoryRetention(context.Context, string, int64) error {
	return nil
}

// A restore to a point the limited window no longer holds is refused before a
// target is reserved, and says why; one inside it still restores.
func TestRestoreRefusesAPointPastTheLimitedWindow(t *testing.T) {
	provider := &historyRestoreProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}}
	store := NewMemoryStore()
	clock := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	sequence := 0
	service, err := NewService(testRegistry(t, provider, nil), store, ServiceOptions{
		PollInterval: time.Second, ProvisioningEnabled: func() bool { return true },
		Now:           func() time.Time { return clock },
		NewID:         func() string { sequence++; return fmt.Sprintf("database-%d", sequence) },
		NewLeaseToken: func() string { sequence++; return fmt.Sprintf("lease-%d", sequence) },
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := testSpec()
	spec.RestoreWindowSeconds = weekSeconds
	source, err := service.Create(context.Background(), CreateRequest{AccountID: "account-a", Name: "orders", Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(5 * 24 * time.Hour)
	twoDaysAgo := RestoreDatabaseRequest{AccountID: source.AccountID, SourceDatabaseID: source.ID, Name: "two-days-ago", PointInTime: clock.Add(-48 * time.Hour)}

	provider.retention = historyLimitedSeconds
	_, created, err := service.RestoreWithResult(context.Background(), twoDaysAgo)
	if !errors.Is(err, ErrInvalid) || created || !strings.Contains(err.Error(), "24h0m0s restore window") || !strings.Contains(err.Error(), "overage on") {
		t.Fatalf("restore past the limited window: created=%v err=%v", created, err)
	}
	if provider.restoreCalls != 0 {
		t.Fatalf("provider contacted %d times", provider.restoreCalls)
	}
	if _, err := store.FindByName(context.Background(), source.AccountID, twoDaysAgo.Name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refused restore reserved a target: %v", err)
	}
	recent := RestoreDatabaseRequest{AccountID: source.AccountID, SourceDatabaseID: source.ID, Name: "an-hour-ago", PointInTime: clock.Add(-time.Hour)}
	if _, created, err := service.RestoreWithResult(context.Background(), recent); err != nil || !created {
		t.Fatalf("restore inside the limited window: created=%v err=%v", created, err)
	}

	// With the spec's window back, the older point restores.
	provider.retention = weekSeconds
	if _, created, err := service.RestoreWithResult(context.Background(), twoDaysAgo); err != nil || !created {
		t.Fatalf("restore with the full window: created=%v err=%v", created, err)
	}
}
