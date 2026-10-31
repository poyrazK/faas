package managedpostgres

import (
	"context"
	"testing"
	"time"
)

// Only the process holding the exclusive sweep collects usage and enforces
// caps; the others skip the tick.
func TestUsageSweepRunsOnlyWithTheExclusiveSweep(t *testing.T) {
	now := time.Date(2026, 10, 1, 10, 7, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		acquired bool
	}{{"holder", true}, {"other replica", false}} {
		t.Run(tc.name, func(t *testing.T) {
			store, registry, provider := usageRecoveryFixture(t, now, 3*time.Hour)
			released, afterSweeps := 0, 0
			collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{
				Now:        func() time.Time { return now },
				AfterSweep: func(context.Context) { afterSweeps++ },
				Exclusive: func(context.Context) (func(), bool, error) {
					if !tc.acquired {
						return nil, false, nil
					}
					return func() { released++ }, true, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			collector.sweep(context.Background())
			swept := len(provider.usageCalls) > 0 && afterSweeps == 1
			if swept != tc.acquired || released != map[bool]int{true: 1, false: 0}[tc.acquired] {
				t.Fatalf("usage calls=%d after sweeps=%d released=%d", len(provider.usageCalls), afterSweeps, released)
			}
		})
	}
}

func TestPostgresUsageSweepLockIsExclusive(t *testing.T) {
	store, _, ctx, _ := postgresStoreFixture(t)
	release, acquired, err := store.TryAcquireUsageSweep(ctx)
	if err != nil || !acquired {
		t.Fatalf("first acquire = %v, %v", acquired, err)
	}
	if _, again, err := store.TryAcquireUsageSweep(ctx); err != nil || again {
		t.Fatalf("second acquire while held = %v, %v", again, err)
	}
	release()
	release, acquired, err = store.TryAcquireUsageSweep(ctx)
	if err != nil || !acquired {
		t.Fatalf("acquire after release = %v, %v", acquired, err)
	}
	release()
}
