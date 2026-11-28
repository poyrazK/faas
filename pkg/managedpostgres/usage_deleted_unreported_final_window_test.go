package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

// unreportedFixture is production 2026-11-27 on either store: a database
// created at 17:30 and deleted at 19:07:30, optionally beside a live one on
// the same backend. Every provider window reports 60 compute seconds unless
// the case's answer says otherwise.
type unreportedFixture struct {
	store    retirementUsageStore
	registry *Registry
	provider *usageTestProvider
	deleted  Database
	live     Database
}

func newUnreportedFixture(t *testing.T, kind string, live bool) unreportedFixture {
	t.Helper()
	created := time.Date(2026, 11, 27, 17, 30, 0, 0, time.UTC)
	store, registry, provider, database := retirementFixture(t, kind, created)
	fixture := unreportedFixture{store: store, registry: registry, provider: provider}
	if live {
		input := postgresTestDatabase(database.AccountID, "live", created)
		input.BackendID, input.BackendFingerprint = database.BackendID, database.BackendFingerprint
		fixture.live = retirementReadyDatabase(t, store, input)
	}
	fixture.deleted = retirementDelete(t, store, database, time.Date(2026, 11, 27, 19, 7, 30, 0, time.UTC), true)
	return fixture
}

func (f unreportedFixture) sweep(t *testing.T, now time.Time) (UsageCollectionSummary, error) {
	t.Helper()
	collector, err := NewUsageCollector(f.registry, f.store, UsageCollectorOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	return collector.Collect(context.Background())
}

// Neon reports nothing for the hour a project was deleted in (production
// 2026-11-27: the final window never completed and admission refused the
// account's new databases as stale). That final window settles at zero only
// once the provider has published the hour for another database on the
// backend, or, with none there, after the correction horizon. A slow or
// failing provider, an earlier window and a malformed report all wait.
func TestUnreportedFinalWindowSettlesOnlyOncePublished(t *testing.T) {
	final := time.Date(2026, 11, 27, 19, 0, 0, 0, time.UTC)
	for _, kind := range []string{"memory", "postgres"} {
		for _, test := range []struct {
			name string
			live bool
			now  time.Time
			// answer is the provider's error for a resource's window; nil
			// reports 60 compute seconds.
			answer  func(deleted bool, window UsageWindow) error
			settles bool
			// compute is the account's total once the sweep ends.
			compute int64
		}{
			{
				name: "another database reported the hour", live: true, now: final.Add(65 * time.Minute),
				answer: func(deleted bool, window UsageWindow) error {
					return unreportedIf(deleted && window.From.Equal(final))
				},
				settles: true, compute: 2*60 + 3*60,
			},
			{
				name: "provider published nothing for the hour", live: true, now: final.Add(65 * time.Minute),
				answer: func(_ bool, window UsageWindow) error { return unreportedIf(window.From.Equal(final)) },
			},
			{
				name: "an earlier window unreported", live: true, now: final.Add(65 * time.Minute),
				answer: func(deleted bool, window UsageWindow) error {
					return unreportedIf(deleted && window.From.Equal(final.Add(-time.Hour)))
				},
			},
			{
				name: "a malformed final report", live: true, now: final.Add(65 * time.Minute),
				answer: func(deleted bool, window UsageWindow) error {
					if deleted && window.From.Equal(final) {
						return ErrUnavailable
					}
					return nil
				},
			},
			{
				name: "no other database, inside the correction horizon", now: final.Add(65 * time.Minute),
				answer: func(deleted bool, window UsageWindow) error {
					return unreportedIf(deleted && window.From.Equal(final))
				},
			},
			{
				name: "no other database, past the correction horizon", now: final.Add(4*time.Hour + 5*time.Minute),
				answer: func(deleted bool, window UsageWindow) error {
					return unreportedIf(deleted && window.From.Equal(final))
				},
				settles: true, compute: 2 * 60,
			},
		} {
			t.Run(kind+"/"+test.name, func(t *testing.T) {
				ctx := context.Background()
				fixture := newUnreportedFixture(t, kind, test.live)
				fixture.provider.usageErrorFor = func(resource string, window UsageWindow) error {
					return test.answer(resource == fixture.deleted.ProviderResourceID, window)
				}
				summary, err := fixture.sweep(t, test.now)
				progress, progressErr := fixture.store.UsageProgress(ctx, fixture.deleted.AccountID, fixture.deleted.ID, time.Hour)
				if progressErr != nil {
					t.Fatal(progressErr)
				}
				if !test.settles {
					if !errors.Is(err, ErrUnavailable) || summary.Deferred == 0 || summary.SettledUnreported != 0 {
						t.Fatalf("summary = %+v, err %v; want the deleted database deferred", summary, err)
					}
					if !progress.CollectedUntil.Before(final.Add(time.Hour)) {
						t.Fatalf("coverage reached %s; an unreported final window was recorded", progress.CollectedUntil)
					}
					return
				}
				if err != nil || summary.Deferred != 0 || summary.SettledUnreported != 1 {
					t.Fatalf("summary = %+v, err %v; want the final window settled", summary, err)
				}
				if !progress.CollectedUntil.Equal(final.Add(time.Hour)) {
					t.Fatalf("coverage until %s, want the final window's end", progress.CollectedUntil)
				}
				snapshot, err := fixture.store.UsageSnapshot(ctx, fixture.deleted.AccountID, test.now)
				if err != nil {
					t.Fatal(err)
				}
				if snapshot.ComputeUnitSeconds != test.compute {
					t.Fatalf("account compute = %d, want %d with the final window at zero", snapshot.ComputeUnitSeconds, test.compute)
				}
				if snapshot.Stale(fixture.registry.UsagePolicy(), test.now) {
					t.Fatal("the account stays stale after its deleted database's final window settled")
				}
			})
		}
	}
}

// The settled zeros are not final: the deleted database's correction tail
// asks for the window again an hour later. A report that has arrived by then
// replaces them, and one that has not keeps them without deferring.
func TestCorrectionRevisitsASettledFinalWindow(t *testing.T) {
	final := time.Date(2026, 11, 27, 19, 0, 0, 0, time.UTC)
	for _, kind := range []string{"memory", "postgres"} {
		for _, test := range []struct {
			name     string
			reported bool
			compute  int64
		}{
			// By then the live database has also recorded its 20:00 window.
			{name: "a late report replaces the zeros", reported: true, compute: 3*60 + 4*60},
			{name: "still unreported keeps the zeros", compute: 2*60 + 4*60},
		} {
			t.Run(kind+"/"+test.name, func(t *testing.T) {
				fixture := newUnreportedFixture(t, kind, true)
				reported := false
				fixture.provider.usageErrorFor = func(resource string, window UsageWindow) error {
					return unreportedIf(resource == fixture.deleted.ProviderResourceID && window.From.Equal(final) && !reported)
				}
				now := final.Add(65 * time.Minute)
				if summary, err := fixture.sweep(t, now); err != nil || summary.SettledUnreported != 1 {
					t.Fatalf("first sweep: summary %+v, err %v; want the final window settled", summary, err)
				}
				now, reported = now.Add(time.Hour), test.reported
				summary, err := fixture.sweep(t, now)
				if err != nil || summary.Deferred != 0 || summary.SettledUnreported != 0 {
					t.Fatalf("correction sweep: summary %+v, err %v; want a quiet correction", summary, err)
				}
				snapshot, err := fixture.store.UsageSnapshot(context.Background(), fixture.deleted.AccountID, now)
				if err != nil {
					t.Fatal(err)
				}
				if snapshot.ComputeUnitSeconds != test.compute {
					t.Fatalf("account compute = %d, want %d", snapshot.ComputeUnitSeconds, test.compute)
				}
			})
		}
	}
}

func unreportedIf(unreported bool) error {
	if unreported {
		return ErrUsageNotReported
	}
	return nil
}
