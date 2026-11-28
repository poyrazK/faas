package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

// production 2026-11-27: two idle Neon projects deleted at 19:07 got no
// consumption report at all for 19:00–20:00. The final window never completed,
// every sweep deferred both databases from 20:11 on, and admission refused the
// account's new databases as stale. Only that window, of a deleted database,
// settles at zero; every other unreported or malformed window still waits.
func TestUnreportedWindowSettlesOnlyForADeletedDatabasesFinalWindow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 27, 20, 5, 0, 0, time.UTC)
	deleted := time.Date(2026, 11, 27, 19, 7, 30, 0, time.UTC)
	final := time.Date(2026, 11, 27, 19, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name       string
		deleted    bool
		unreported time.Time
		err        error
		settles    bool
	}{
		{name: "deleted database, final window unreported", deleted: true, unreported: final, err: ErrUsageNotReported, settles: true},
		{name: "deleted database, earlier window unreported", deleted: true, unreported: final.Add(-time.Hour), err: ErrUsageNotReported},
		{name: "deleted database, final window malformed", deleted: true, unreported: final, err: ErrUnavailable},
		{name: "live database, latest window unreported", unreported: final, err: ErrUsageNotReported},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, registry, provider := usageRecoveryFixture(t, now, 2*time.Hour+35*time.Minute)
			if test.deleted {
				database := store.databases["db"]
				database.State, database.DeletedAt = StateDeleted, &deleted
				store.databases["db"] = database
			}
			provider.usageError = func(window UsageWindow) error {
				if window.From.Equal(test.unreported) {
					return test.err
				}
				return nil
			}
			collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			summary, err := collector.Collect(ctx)
			if test.settles {
				if err != nil || summary.Deferred != 0 {
					t.Fatalf("summary = %+v, err %v; want the final window settled", summary, err)
				}
				record, ok := store.usage[usageKey{"db", final, final.Add(time.Hour), MeterComputeUnitSeconds}]
				if !ok || record.Quantity != 0 || record.CostMillicents != 0 {
					t.Fatalf("final window record = %+v (found %v); want a zero reading", record, ok)
				}
				snapshot, err := store.UsageSnapshot(ctx, "account", now)
				if err != nil {
					t.Fatal(err)
				}
				if snapshot.Stale(registry.UsagePolicy(), now) {
					t.Fatal("the account stays stale after its deleted database's final window settled")
				}
				return
			}
			if !errors.Is(err, ErrUnavailable) || summary.Deferred != 1 {
				t.Fatalf("summary = %+v, err %v; want the database deferred", summary, err)
			}
			if _, ok := store.usage[usageKey{"db", test.unreported, test.unreported.Add(time.Hour), MeterComputeUnitSeconds}]; ok {
				t.Fatal("an unreported window was recorded")
			}
		})
	}
}

// The settled zeros are not final: the deleted database's correction tail
// asks for the window again, and a report that arrives late replaces them.
func TestLateReportReplacesASettledFinalWindow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 27, 20, 5, 0, 0, time.UTC)
	deleted := time.Date(2026, 11, 27, 19, 7, 30, 0, time.UTC)
	final := time.Date(2026, 11, 27, 19, 0, 0, 0, time.UTC)
	store, registry, provider := usageRecoveryFixture(t, now, 2*time.Hour+35*time.Minute)
	database := store.databases["db"]
	database.State, database.DeletedAt = StateDeleted, &deleted
	store.databases["db"] = database
	reported := false
	provider.usageError = func(window UsageWindow) error {
		if window.From.Equal(final) && !reported {
			return ErrUsageNotReported
		}
		return nil
	}
	sweep := func() {
		t.Helper()
		collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		if summary, err := collector.Collect(ctx); err != nil || summary.Deferred != 0 {
			t.Fatalf("sweep at %s: summary %+v, err %v", now, summary, err)
		}
	}
	key := usageKey{"db", final, final.Add(time.Hour), MeterComputeUnitSeconds}
	sweep()
	if store.usage[key].Quantity != 0 {
		t.Fatalf("settled record = %+v; want zero", store.usage[key])
	}

	now, reported = now.Add(time.Hour), true
	sweep()
	if got := store.usage[key]; got.Quantity != 60 || !got.ObservedAt.Equal(now) {
		t.Fatalf("record after the late report = %+v; want the reported 60 observed at %s", got, now)
	}
}
