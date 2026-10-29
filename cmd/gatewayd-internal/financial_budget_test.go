package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var errBudgetStoreDown = errors.New("database unavailable")

// budgetCacheFixture drives a budgetAdmissionCache with a manual clock and
// runs background refreshes only when the test says so.
type budgetCacheFixture struct {
	t       *testing.T
	now     time.Time
	cache   *budgetAdmissionCache
	pending []func()
	reads   int
	result  error
}

func newBudgetCacheFixture(t *testing.T, grace time.Duration) *budgetCacheFixture {
	f := &budgetCacheFixture{t: t, now: time.Unix(1_700_000_000, 0)}
	f.cache = newBudgetAdmissionCache(func() time.Time { return f.now }, grace)
	f.cache.spawn = func(run func()) { f.pending = append(f.pending, run) }
	return f
}

func (f *budgetCacheFixture) check(key budgetAdmissionKey) error {
	return f.cache.check(context.Background(), key, func(context.Context) error { f.reads++; return f.result })
}

// refresh runs the background reads the cache started.
func (f *budgetCacheFixture) refresh() int {
	runs := f.pending
	f.pending = nil
	for _, run := range runs {
		run()
	}
	return len(runs)
}

func TestBudgetAdmissionCacheReusesDecisionsNotFailures(t *testing.T) {
	f := newBudgetCacheFixture(t, api.FinancialBudgetAdmissionOutageGrace)
	key := budgetAdmissionKey{app: "app", scope: "production"}
	if err := f.check(key); err != nil || f.reads != 1 {
		t.Fatalf("first admission: %v reads=%d", err, f.reads)
	}
	if err := f.check(key); err != nil || f.reads != 1 || len(f.pending) != 0 {
		t.Fatalf("warm request re-read the store: %v reads=%d", err, f.reads)
	}
	// Past the TTL the request is answered from the cache and one
	// background read refreshes it.
	f.now = f.now.Add(api.FinancialBudgetAdmissionCacheTTL)
	f.result = state.FinancialBudgetHeldProblem()
	if err := f.check(key); err != nil || f.reads != 1 {
		t.Fatalf("an expired decision made the request wait: %v reads=%d", err, f.reads)
	}
	if err := f.check(key); err != nil || len(f.pending) != 1 {
		t.Fatalf("one refresh per key: pending=%d err=%v", len(f.pending), err)
	}
	f.refresh()
	if err := f.check(key); !errors.Is(err, state.ErrFinancialBudgetHeld) || f.reads != 2 {
		t.Fatalf("the refreshed hold was not served: %v reads=%d", err, f.reads)
	}

	other := budgetAdmissionKey{app: "app", scope: "preview"}
	f.result = errBudgetStoreDown
	for range 2 {
		if err := f.check(other); err == nil {
			t.Fatal("an app never read admitted traffic while the store was down")
		}
	}
	if f.reads != 4 {
		t.Fatalf("an unavailable store was cached: reads=%d", f.reads)
	}
}

// ADR-20261028083324: with the store unreachable, an app's last decision
// keeps answering until the grace after the last successful read, then
// admission fails closed. A hold is never turned into an admission.
func TestBudgetAdmissionCacheOutageGrace(t *testing.T) {
	const grace = time.Minute
	for _, tc := range []struct {
		name     string
		last     error // the decision before the outage
		at       time.Duration
		admitted bool
	}{
		{name: "admitted, inside the grace", at: grace / 2, admitted: true},
		{name: "admitted, just before the grace", at: grace - time.Second, admitted: true},
		{name: "admitted, past the grace", at: grace},
		{name: "held, inside the grace", last: state.FinancialBudgetHeldProblem(), at: grace / 2},
		{name: "held, past the grace", last: state.FinancialBudgetHeldProblem(), at: grace + time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBudgetCacheFixture(t, grace)
			key := budgetAdmissionKey{app: "app"}
			f.result = tc.last
			_ = f.check(key)
			f.result = errBudgetStoreDown
			// Requests and failed refreshes during the outage.
			for elapsed := time.Second; elapsed < tc.at; elapsed += 5 * time.Second {
				f.now = f.now.Add(5 * time.Second)
				_ = f.check(key)
				f.refresh()
			}
			f.now = time.Unix(1_700_000_000, 0).Add(tc.at)
			err := f.check(key)
			if got := err == nil; got != tc.admitted {
				t.Fatalf("at %s: err=%v, want admitted=%v", tc.at, err, tc.admitted)
			}
			if tc.last != nil && tc.at < grace && !errors.Is(err, state.ErrFinancialBudgetHeld) {
				t.Fatalf("a held app was not held during the outage: %v", err)
			}
			if tc.at >= grace && !errors.Is(err, errBudgetStoreDown) {
				t.Fatalf("past the grace the request did not read the store: %v", err)
			}
			// The store comes back: the next read answers again.
			f.result = nil
			f.now = f.now.Add(api.FinancialBudgetAdmissionCacheTTL)
			_ = f.check(key)
			f.refresh()
			if err := f.check(key); err != nil {
				t.Fatalf("no recovery once the store answered: %v", err)
			}
		})
	}
}

// A store answer that the admission cannot be evaluated drops the decision
// at once; the next request reads and is refused.
func TestBudgetAdmissionCacheDefinitiveAnswerDropsDecision(t *testing.T) {
	for _, definitive := range []error{state.ErrNotFound, state.ErrInvalidArgument, state.ErrConflict} {
		t.Run(definitive.Error(), func(t *testing.T) {
			f := newBudgetCacheFixture(t, time.Minute)
			key := budgetAdmissionKey{app: "app"}
			_ = f.check(key)
			f.now = f.now.Add(api.FinancialBudgetAdmissionCacheTTL)
			f.result = definitive
			if err := f.check(key); err != nil {
				t.Fatalf("stale admission was not served while refreshing: %v", err)
			}
			f.refresh()
			if err := f.check(key); !errors.Is(err, definitive) {
				t.Fatalf("after a definitive answer: %v, want %v", err, definitive)
			}
		})
	}
}
