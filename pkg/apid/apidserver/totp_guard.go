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
// skew accepts three values per attempt. Ten attempts in fifteen minutes
// hold that account's TOTP checks until the window has moved on; a correct
// code clears the count.
//
// An attempt is counted when it is admitted, before its code is checked.
// Counting failures after the check let every request of a concurrent burst
// pass a check meant to allow ten (bug hunt 2026-10-28 H74). The count is
// shared by every apid through account_mail_throttle (kind totp_attempt,
// ADR 20261110120000); this process's own count is the fallback when the
// store cannot count.
const (
	totpMaxFailures   = 10
	totpFailureWindow = 15 * time.Minute
)

const totpAttemptKind = "totp_attempt"

var totpAttemptLimit = state.MailSlotLimit{Window: totpFailureWindow, MaxPerWindow: totpMaxFailures}

// reserveTOTPAttempt counts an attempt for the account in the shared
// counter and returns zero when it may try a code, else how long to wait.
// The shared counter keeps a fixed window, so the wait is the whole window.
func (s *server) reserveTOTPAttempt(ctx context.Context, accountID string) time.Duration {
	if st, ok := s.store.(state.AccountMailThrottleStore); ok {
		allowed, err := st.TakeAccountMailSlot(ctx, accountID, totpAttemptKind, totpAttemptLimit)
		if err == nil {
			if allowed {
				return 0
			}
			return totpFailureWindow
		}
		s.log.Warn("totp: shared attempt count unavailable, using this process's", "account", accountID, "err", err)
	}
	return s.totp.reserve(accountID, time.Now())
}

// resetTOTPAttempts clears the account's counted attempts after a correct
// code, in the shared counter and in this process.
func (s *server) resetTOTPAttempts(ctx context.Context, accountID string) {
	if st, ok := s.store.(state.AccountThrottleClearStore); ok {
		if err := st.ClearAccountMailSlot(ctx, accountID, totpAttemptKind); err != nil {
			s.log.Warn("totp: clear shared attempt count", "account", accountID, "err", err)
		}
	}
	s.totp.reset(accountID)
}

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
