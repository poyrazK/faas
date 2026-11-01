package apidserver

import (
	"strconv"
	"sync"
	"time"
)

// TOTP guesses are limited per account as well as per IP. The auth limiter
// counts failures per IP only, so someone holding the password and many
// addresses could walk the code space: a six-digit code with one step of
// skew accepts three values per attempt. Ten attempts in fifteen minutes
// hold that account's TOTP checks until the window has moved on; a correct
// code clears the count.
//
// An attempt is counted when it is admitted, before its code is checked.
// Counting failures after the check let every request of a concurrent burst
// pass a check meant to allow ten (bug hunt 2026-10-28 H74). The count lives
// in this apid process, which is the control plane's only one today.
const (
	totpMaxFailures   = 10
	totpFailureWindow = 15 * time.Minute
)

type totpGuard struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

func newTOTPGuard() *totpGuard { return &totpGuard{fails: map[string][]time.Time{}} }

// reserve counts an attempt and returns zero when the account may try a
// code, else how long until its oldest counted attempt leaves the window
// (counting nothing).
func (g *totpGuard) reserve(accountID string, now time.Time) time.Duration {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	recent := g.pruneLocked(accountID, now)
	if len(recent) >= totpMaxFailures {
		return recent[0].Add(totpFailureWindow).Sub(now)
	}
	g.fails[accountID] = append(recent, now)
	return 0
}

func (g *totpGuard) reset(accountID string) {
	if g == nil {
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
