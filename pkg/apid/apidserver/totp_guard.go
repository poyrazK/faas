package apidserver

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TOTP guesses are limited per account as well as per IP. The auth limiter
// counts failures per IP only, so someone holding the password and many
// addresses could walk the code space: a six-digit code with one step of
// skew accepts three values per attempt. Ten failures in fifteen minutes
// hold that account's TOTP checks until the window has moved on.
//
// ADR-1237: apid runs on several control nodes, so the window lives in the
// database (state.TOTPFailureStore) whenever the store provides it; a window
// per replica would hand each replica its own budget of guesses. Without such
// a store (tests, tools) the guard keeps the window in process.
const (
	totpMaxFailures   = 10
	totpFailureWindow = 15 * time.Minute
	// totpStoreErrorRetry is the wait returned when the shared window cannot
	// be read. The guard fails closed: another replica may already have used
	// the account's budget.
	totpStoreErrorRetry = 5 * time.Second
)

type totpGuard struct {
	store state.TOTPFailureStore // nil: in-process window

	mu    sync.Mutex
	fails map[string][]time.Time
}

func newTOTPGuard() *totpGuard { return &totpGuard{fails: map[string][]time.Time{}} }

// newSharedTOTPGuard uses store's shared window when it has one.
func newSharedTOTPGuard(store state.Store) *totpGuard {
	g := newTOTPGuard()
	if shared, ok := store.(state.TOTPFailureStore); ok {
		g.store = shared
	}
	return g
}

// retryAfter is zero when the account may attempt a code, else how long
// until its oldest counted failure leaves the window. now is used only by
// the in-process window; the shared window uses the database clock.
func (g *totpGuard) retryAfter(ctx context.Context, accountID string, now time.Time) time.Duration {
	if g == nil {
		return 0
	}
	if g.store != nil {
		w, err := g.store.TOTPFailures(ctx, accountID, totpFailureWindow)
		if err != nil {
			return totpStoreErrorRetry
		}
		if w.Failures < totpMaxFailures {
			return 0
		}
		return w.Oldest.Add(totpFailureWindow).Sub(w.CheckedAt)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	recent := g.pruneLocked(accountID, now)
	if len(recent) < totpMaxFailures {
		return 0
	}
	return recent[0].Add(totpFailureWindow).Sub(now)
}

func (g *totpGuard) fail(ctx context.Context, accountID string, now time.Time) {
	if g == nil {
		return
	}
	if g.store != nil {
		// A failed write loses one count; the next check still reads every
		// failure recorded so far.
		_ = g.store.RecordTOTPFailure(ctx, accountID, totpFailureWindow)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fails[accountID] = append(g.pruneLocked(accountID, now), now)
}

func (g *totpGuard) reset(ctx context.Context, accountID string) {
	if g == nil {
		return
	}
	if g.store != nil {
		_ = g.store.ResetTOTPFailures(ctx, accountID)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.fails, accountID)
}

func (g *totpGuard) pruneLocked(accountID string, now time.Time) []time.Time {
	recent := g.fails[accountID][:0:0]
	for _, at := range g.fails[accountID] {
		if now.Sub(at) < totpFailureWindow {
			recent = append(recent, at)
		}
	}
	if len(recent) == 0 {
		delete(g.fails, accountID)
		return nil
	}
	g.fails[accountID] = recent
	return recent
}

func retryAfterSeconds(d time.Duration) string {
	secs := int(d / time.Second)
	if d%time.Second != 0 {
		secs++
	}
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}
