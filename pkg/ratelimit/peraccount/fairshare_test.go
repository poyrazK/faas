package peraccount_test

// adr: 20261026143210

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
)

// One busy app used to take every token of its account's bucket, so its
// sibling apps' rows were refused and their route evidence went blind. Under
// pressure the busy app now yields once it has used its fair share of the
// minute; the quiet app keeps getting rows until the bucket is empty.
func TestTakeForAppKeepsSiblingRowsUnderPressure(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	r := peraccount.NewLimiter()
	r.SetClock(func() time.Time { return now })
	account, busy, quiet := uuid.New(), uuid.New(), uuid.New()
	const capacity = 100

	if ok, _ := r.TakeForApp(account, quiet, capacity); !ok {
		t.Fatal("quiet app's first row refused")
	}
	busyRows := 0
	for range capacity {
		if ok, _ := r.TakeForApp(account, busy, capacity); ok {
			busyRows++
		}
	}
	// Two active apps: the busy one may take at most half the capacity once
	// the bucket is below half.
	if busyRows != capacity/2 {
		t.Fatalf("busy app took %d rows, want %d", busyRows, capacity/2)
	}
	ok, retryMs := r.TakeForApp(account, busy, capacity)
	if ok || retryMs <= 0 || retryMs > 60_000 {
		t.Fatalf("busy app over its share: ok=%v retry=%dms, want refused until the next minute", ok, retryMs)
	}
	quietRows := 0
	for range capacity {
		if ok, _ := r.TakeForApp(account, quiet, capacity); ok {
			quietRows++
		}
	}
	if quietRows != capacity-1-capacity/2 {
		t.Fatalf("quiet app got %d more rows, want the remaining %d", quietRows, capacity-1-capacity/2)
	}

	// The next minute the busy app's count starts again; the bucket refills
	// at its usual rate.
	now = now.Add(time.Minute)
	if ok, _ := r.TakeForApp(account, busy, capacity); !ok {
		t.Fatal("busy app still refused in the next minute with a refilled bucket")
	}
}

// An account with one active app is limited by its bucket alone.
func TestTakeForAppSingleAppUsesWholeBucket(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	r := peraccount.NewLimiter()
	r.SetClock(func() time.Time { return now })
	account, app := uuid.New(), uuid.New()
	const capacity = 50
	taken := 0
	for range capacity + 10 {
		if ok, _ := r.TakeForApp(account, app, capacity); ok {
			taken++
		}
	}
	if taken != capacity {
		t.Fatalf("single app took %d rows, want the whole bucket of %d", taken, capacity)
	}
}

// Without pressure every app takes rows freely: fairness only applies once
// the bucket is below half.
func TestTakeForAppWithoutPressureIsUnlimitedPerApp(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	r := peraccount.NewLimiter()
	r.SetClock(func() time.Time { return now })
	account, a, b := uuid.New(), uuid.New(), uuid.New()
	const capacity = 100
	for range 10 {
		if ok, _ := r.TakeForApp(account, b, capacity); !ok {
			t.Fatal("app b refused without pressure")
		}
	}
	for i := range capacity/2 - 10 {
		if ok, _ := r.TakeForApp(account, a, capacity); !ok {
			t.Fatalf("app a refused at row %d without pressure", i)
		}
	}
}
