package apidserver

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// securityMailLimiter caps the sign-in mail (password reset, magic link,
// email verification) one account receives. Those routes were limited per
// client IP only, so many addresses could mail-bomb one customer, hurt sender
// reputation, and fill the outbox's pending budget so other mail got
// ErrQuota. Per apid process; the routes' per-IP limits still apply.
type securityMailLimiter struct {
	mu   sync.Mutex
	now  func() time.Time
	sent map[string][]time.Time // account ID + kind → sends within the last hour
}

func newSecurityMailLimiter() *securityMailLimiter {
	return &securityMailLimiter{now: time.Now, sent: map[string][]time.Time{}}
}

// allow reports whether accountID may be sent another sign-in mail of kind
// now and, if so, counts it. Kinds are counted apart, so a verification mail
// does not hold up the magic link the customer asks for next.
func (l *securityMailLimiter) allow(accountID, kind string) bool {
	if l == nil || accountID == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	key := accountID + "\x00" + kind
	recent := l.sent[key][:0]
	for _, at := range l.sent[key] {
		if now.Sub(at) < time.Hour {
			recent = append(recent, at)
		}
	}
	if len(recent) >= api.SecurityMailPerAccountPerHour ||
		(len(recent) > 0 && now.Sub(recent[len(recent)-1]) < api.SecurityMailPerAccountInterval) {
		l.sent[key] = recent
		return false
	}
	if len(l.sent) >= api.SecurityMailTrackedAccountsMax {
		l.sweepLocked(now)
	}
	l.sent[key] = append(recent, now)
	return true
}

// allowSecurityMail applies the per-account cap; a refused send is logged
// and audited, and the public endpoint still answers its constant 200.
func (s *server) allowSecurityMail(ctx context.Context, acct state.Account, kind string) bool {
	if s.securityMail.allow(acct.ID, kind) {
		return true
	}
	s.log.Info("auth: sign-in mail throttled for account", "account", acct.ID, "kind", kind)
	s.audit.Emit(ctx, "auth.mail_throttled", &acct.ID, map[string]any{"kind": kind})
	return false
}

func (l *securityMailLimiter) sweepLocked(now time.Time) {
	for id, sends := range l.sent {
		if len(sends) == 0 || now.Sub(sends[len(sends)-1]) >= time.Hour {
			delete(l.sent, id)
		}
	}
}
