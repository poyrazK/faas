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
			// Keep counting the tracked addresses rather than forget them.
			return false
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

func (l *loginFailureLimiter) sweepLocked(now time.Time) {
	for key, f := range l.failures {
		if len(f.at) == 0 || now.Sub(f.at[len(f.at)-1]) >= api.LoginFailureEmailWindow {
			delete(l.failures, key)
		}
	}
}

// decodeCredentials reads a password sign-in body and refuses it with 429
// while the address is out of failed-sign-in budget.
func (s *server) decodeCredentials(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	email, password, ok := decodeEmailPasswordRequest(w, r)
	if !ok {
		return "", "", false
	}
	if wait := s.loginFailures.retryAfter(auth.HashEmail(email)); wait > 0 {
		api.WriteProblem(w, api.ErrAuthRateLimited(int(math.Ceil(wait.Seconds()))))
		return "", "", false
	}
	return email, password, true
}

// recordLoginFailure counts a wrong password for email. When that uses up the
// address's budget it is audited and a bound account's owner is emailed, off
// the request so the response time does not reveal whether the address has
// an account.
func (s *server) recordLoginFailure(ctx context.Context, email string, acct *state.Account) {
	if !s.loginFailures.record(auth.HashEmail(email)) {
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
