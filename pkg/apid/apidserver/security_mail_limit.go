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
// ErrQuota. The database counter (takeSharedMailSlot) is shared by every
// apid; this per-process one stands in when it is unavailable. The routes'
// per-IP limits still apply.
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
	return l.allowWithin(accountID, kind, securityMailSlot)
}

// allowWithin is allow with an explicit per-kind limit. Its window must be
// at most an hour, the span the limiter remembers.
func (l *securityMailLimiter) allowWithin(accountID, kind string, limit state.MailSlotLimit) bool {
	if l == nil || accountID == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	key := accountID + "\x00" + kind
	recent := l.sent[key][:0]
	for _, at := range l.sent[key] {
		if now.Sub(at) < limit.Window {
			recent = append(recent, at)
		}
	}
	if len(recent) >= limit.MaxPerWindow ||
		(len(recent) > 0 && now.Sub(recent[len(recent)-1]) < limit.MinInterval) {
		l.sent[key] = recent
		return false
	}
	if len(l.sent) >= api.SecurityMailTrackedAccountsMax {
		l.sweepLocked(now)
	}
	l.sent[key] = append(recent, now)
	return true
}

// securityMailSlot is the per-account cap on one kind of security mail.
var securityMailSlot = state.MailSlotLimit{
	Window:       time.Hour,
	MaxPerWindow: api.SecurityMailPerAccountPerHour,
	MinInterval:  api.SecurityMailPerAccountInterval,
}

// credentialMailSlot caps "a new credential was created" notices. They are
// not spaced out: minting a second key a few seconds after the first must
// still mail about the second, or the owner revokes only the one they were
// told about (F14-2, ADR-20261114120000). The hourly cap still bounds a
// flood.
var credentialMailSlot = state.MailSlotLimit{
	Window:       time.Hour,
	MaxPerWindow: api.CredentialNoticeMailsPerHour,
}

// supportActionMailSlot sends owners and admins at most one "support used
// its access" mail per SupportActionMailInterval (ADR-20261116163508); the
// activity log has every action.
var supportActionMailSlot = state.MailSlotLimit{
	Window:       time.Hour,
	MaxPerWindow: int(time.Hour / api.SupportActionMailInterval),
	MinInterval:  api.SupportActionMailInterval,
}

// securityMailLimitFor is the per-account cap for one kind of security mail.
func securityMailLimitFor(kind string) state.MailSlotLimit {
	if kind == credentialNoticeKind {
		return credentialMailSlot
	}
	if kind == supportActionMailKind {
		return supportActionMailSlot
	}
	return securityMailSlot
}

// takeSharedMailSlot counts a mail in the database, so every apid shares
// the account's cap (ADR 20261108160000). ok is false when the store has no
// counter or the count failed; the caller then uses its own process's
// counter instead of sending unbounded or refusing.
func (s *server) takeSharedMailSlot(ctx context.Context, accountID, kind string, limit state.MailSlotLimit) (allowed, ok bool) {
	st, has := s.store.(state.AccountMailThrottleStore)
	if !has || accountID == "" {
		return false, false
	}
	allowed, err := st.TakeAccountMailSlot(ctx, accountID, kind, limit)
	if err != nil {
		s.log.Warn("auth: shared mail throttle unavailable, using this process's", "account", accountID, "kind", kind, "err", err)
		return false, false
	}
	return allowed, true
}

// allowSecurityMail applies the per-account cap; a refused send is logged
// and audited, and the public endpoint still answers its constant 200.
func (s *server) allowSecurityMail(ctx context.Context, acct state.Account, kind string) bool {
	limit := securityMailLimitFor(kind)
	allowed, shared := s.takeSharedMailSlot(ctx, acct.ID, kind, limit)
	if !shared {
		allowed = s.securityMail.allowWithin(acct.ID, kind, limit)
	}
	if allowed {
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
