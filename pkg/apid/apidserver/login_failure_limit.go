package apidserver

import (
	"context"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth"
	mailpkg "github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// loginFailureLimiter counts failed password sign-ins per email address from
// every client address. The only limit was 10 failures a minute per client
// IP, so a botnet or a residential-proxy pool could try thousands of
// passwords a minute against one customer (bug hunt 2026-10-28 H36). Keys are
// email hashes. Per apid process; the per-IP limit still applies.
type loginFailureLimiter struct {
	mu       sync.Mutex
	now      func() time.Time
	failures map[string]*loginFailures
}

type loginFailures struct {
	at       []time.Time // within the window, oldest first
	notified time.Time   // when the budget last ran out
}

func newLoginFailureLimiter() *loginFailureLimiter {
	return &loginFailureLimiter{now: time.Now, failures: map[string]*loginFailures{}}
}

// recentLocked drops key's failures that left the window.
func (l *loginFailureLimiter) recentLocked(key string, now time.Time) *loginFailures {
	f := l.failures[key]
	if f == nil {
		return nil
	}
	i := 0
	for i < len(f.at) && now.Sub(f.at[i]) >= api.LoginFailureEmailWindow {
		i++
	}
	f.at = f.at[i:]
	if len(f.at) == 0 {
		delete(l.failures, key)
		return nil
	}
	return f
}

// retryAfter returns how long password sign-in for key stays refused; zero
// when it may be tried.
func (l *loginFailureLimiter) retryAfter(key string) time.Duration {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	f := l.recentLocked(key, now)
	if f == nil || len(f.at) < api.LoginFailuresPerEmail {
		return 0
	}
	return f.at[len(f.at)-api.LoginFailuresPerEmail].Add(api.LoginFailureEmailWindow).Sub(now)
}

// record counts a failure for key. It reports true when this failure used up
// the budget and the owner has not been told within the window.
func (l *loginFailureLimiter) record(key string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	f := l.recentLocked(key, now)
	if f == nil {
		if len(l.failures) >= api.LoginFailureEmailsTrackedMax {
			l.sweepLocked(now)
		}
		if len(l.failures) >= api.LoginFailureEmailsTrackedMax {
			// A full table used to stop tracking new addresses, so a
			// botnet could fill it with random ones and then guess the
			// victim's password limited only per IP. Make room by
			// forgetting the address with the fewest failures instead:
			// a targeted address outlasts the filler.
			l.evictLocked()
		}
		f = &loginFailures{}
		l.failures[key] = f
	}
	f.at = append(f.at, now)
	if len(f.at) > api.LoginFailuresPerEmail {
		f.at = f.at[len(f.at)-api.LoginFailuresPerEmail:]
	}
	if len(f.at) < api.LoginFailuresPerEmail || now.Sub(f.notified) < api.LoginFailureEmailWindow {
		return false
	}
	f.notified = now
	return true
}

// reset forgets key's failures: its owner proved control of the address.
func (l *loginFailureLimiter) reset(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

// evictLocked forgets the tracked address with the fewest failures (oldest
// last failure on a tie) among a random sample of the table. Go randomizes
// map iteration, so the sample costs a bounded amount per new address.
func (l *loginFailureLimiter) evictLocked() {
	var victim string
	var fewest *loginFailures
	seen := 0
	for key, f := range l.failures {
		if fewest == nil || len(f.at) < len(fewest.at) ||
			len(f.at) == len(fewest.at) && len(f.at) > 0 && f.at[len(f.at)-1].Before(fewest.at[len(fewest.at)-1]) {
			victim, fewest = key, f
		}
		seen++
		if seen >= api.LoginFailureEvictionSample {
			break
		}
	}
	if fewest != nil {
		delete(l.failures, victim)
	}
}

func (l *loginFailureLimiter) sweepLocked(now time.Time) {
	for key, f := range l.failures {
		if len(f.at) == 0 || now.Sub(f.at[len(f.at)-1]) >= api.LoginFailureEmailWindow {
			delete(l.failures, key)
		}
	}
}

// loginFailureBudget is the per-address budget the shared store enforces.
var loginFailureBudget = state.LoginFailureBudget{
	MaxFailures: api.LoginFailuresPerEmail,
	Window:      api.LoginFailureEmailWindow,
}

// sharedLoginFailures returns the store's shared counter, or nil when the
// store has none: every apid then counts on its own (ADR 20261111150000).
func (s *server) sharedLoginFailures() state.LoginFailureBudgetStore {
	st, _ := s.store.(state.LoginFailureBudgetStore)
	return st
}

// loginRetryAfter reports how long password checks for email stay refused.
// The counts are shared by every apid; when the shared counter cannot be
// read this process's own counts decide.
func (s *server) loginRetryAfter(ctx context.Context, email string) time.Duration {
	key := auth.HashEmail(email)
	if st := s.sharedLoginFailures(); st != nil {
		wait, err := st.LoginFailureRetryAfter(ctx, key, loginFailureBudget)
		if err == nil {
			return wait
		}
		s.log.Warn("auth: shared sign-in failure budget unavailable, using this process's", "err", err)
	}
	return s.loginFailures.retryAfter(key)
}

// countLoginFailure records one failure and reports whether it used up the
// address's budget with the owner not yet told.
func (s *server) countLoginFailure(ctx context.Context, key string) bool {
	if st := s.sharedLoginFailures(); st != nil {
		notify, err := st.RecordLoginFailure(ctx, key, loginFailureBudget)
		if err == nil {
			return notify
		}
		s.log.Warn("auth: shared sign-in failure budget unavailable, using this process's", "err", err)
	}
	return s.loginFailures.record(key)
}

// resetLoginFailures lifts a failed-password pause on email: its owner
// proved control of the address.
func (s *server) resetLoginFailures(ctx context.Context, email string) {
	key := auth.HashEmail(email)
	s.loginFailures.reset(key)
	if st := s.sharedLoginFailures(); st != nil {
		if err := st.ResetLoginFailures(ctx, key); err != nil {
			s.log.Warn("auth: could not reset shared sign-in failure budget", "err", err)
		}
	}
}

// refuseWhileLoginLimited answers 429 while email is out of failed-password
// budget. Every password check (sign-in, enrollment, email change, password
// change, turning off two-factor) shares the budget, so a stolen session
// cannot guess the password through a route sign-in does not count.
func (s *server) refuseWhileLoginLimited(w http.ResponseWriter, r *http.Request, email string) bool {
	wait := s.loginRetryAfter(r.Context(), email)
	if wait <= 0 {
		return false
	}
	api.WriteProblem(w, api.ErrAuthRateLimited(int(math.Ceil(wait.Seconds()))))
	return true
}

// decodeCredentials reads a password sign-in body and refuses it with 429
// while the address is out of failed-sign-in budget.
func (s *server) decodeCredentials(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	email, password, ok := decodeEmailPasswordRequest(w, r)
	if !ok {
		return "", "", false
	}
	if s.refuseWhileLoginLimited(w, r, email) {
		return "", "", false
	}
	return email, password, true
}

// recordLoginFailure counts a wrong password for email. When that uses up the
// address's budget it is audited and a bound account's owner is emailed, off
// the request so the response time does not reveal whether the address has
// an account.
func (s *server) recordLoginFailure(ctx context.Context, email string, acct *state.Account) {
	if !s.countLoginFailure(ctx, auth.HashEmail(email)) {
		return
	}
	var accountID *string
	if acct != nil {
		accountID = &acct.ID
	}
	s.audit.Emit(ctx, "auth.login_email_limited", accountID, map[string]any{"email_hash": auth.HashEmail(email)})
	if acct == nil || s.mailer == nil || !s.allowSecurityMail(ctx, *acct, "sign_in_failures") {
		return
	}
	owner := *acct
	go func() {
		mailCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		subject, body := mailpkg.SignInFailuresBodyV1(owner.Email, api.LoginFailuresPerEmail, api.LoginFailureEmailWindow, time.Now())
		if err := s.mailer.Send(mailCtx, Message{To: []string{owner.Email}, Subject: subject, TextBody: body}); err != nil {
			s.log.Error("login.failures_mailer", "err", err)
		}
	}()
}
