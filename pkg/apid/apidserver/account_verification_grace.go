package apidserver

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// An account created without proof of its address has emailVerificationGrace
// to verify it. Past that it is restricted, never deleted: password sign-in
// and existing sessions and keys stop working except for the routes that end
// the restriction (resend, logout, reading the account, changing the
// address). Every channel that proves the address (the verification link, a
// magic link, Google, GitHub, forgot-password's magic link, an email change)
// verifies the account and lifts it. Accounts older than the enforcement get
// their grace from api.EmailVerificationEnforcedFrom, and an account still
// active near its deadline is mailed a reminder.

// verificationDeadline is when an unverified acct becomes restricted.
func verificationDeadline(acct state.Account) time.Time {
	start := acct.CreatedAt
	if start.Before(api.EmailVerificationEnforcedFrom) {
		start = api.EmailVerificationEnforcedFrom
	}
	return start.Add(emailVerificationGrace)
}

// verificationOverdue reports whether acct is unverified past its grace.
func verificationOverdue(acct state.Account, now time.Time) bool {
	return !acct.EmailVerified() && !acct.CreatedAt.IsZero() && now.After(verificationDeadline(acct))
}

func (s *server) verificationNow() time.Time {
	if s.verificationClock != nil {
		return s.verificationClock()
	}
	return time.Now()
}

func (s *server) verificationOverdueNow(acct state.Account) bool {
	return verificationOverdue(acct, s.verificationNow())
}

// verificationReminders remembers when each account was last reminded, so
// an API key in a busy CI loop gets one mail a day, not one per request.
type verificationReminders struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (v *verificationReminders) allow(accountID string, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.last == nil {
		v.last = map[string]time.Time{}
	}
	if at, ok := v.last[accountID]; ok && now.Sub(at) < api.EmailVerificationReminderInterval {
		return false
	}
	if len(v.last) >= api.SecurityMailTrackedAccountsMax {
		for id, at := range v.last {
			if now.Sub(at) >= api.EmailVerificationReminderInterval {
				delete(v.last, id)
			}
		}
		if len(v.last) >= api.SecurityMailTrackedAccountsMax {
			return false
		}
	}
	v.last[accountID] = now
	return true
}

// remindVerificationDue mails a fresh verification link, off the request
// path, to an unverified account in the last days before its deadline.
func (s *server) remindVerificationDue(r *http.Request, acct state.Account) {
	if acct.EmailVerified() || acct.CreatedAt.IsZero() {
		return
	}
	now := s.verificationNow()
	deadline := verificationDeadline(acct)
	if now.After(deadline) || deadline.Sub(now) > api.EmailVerificationReminderWindow ||
		!s.verificationReminders.allow(acct.ID, now) {
		return
	}
	base := s.passwordResetLinkBase(r)
	//nolint:contextcheck // The work outlives the request; it keeps the request values and has its own timeout.
	ctx := context.WithoutCancel(r.Context())
	if !s.resetMail.try(func() {
		ctx, cancel := context.WithTimeout(ctx, api.PasswordResetMailTimeout)
		defer cancel()
		s.sendEmailVerificationAt(ctx, base, acct)
	}) {
		s.log.Warn("email_verification.reminder_busy", "account_id", acct.ID)
	}
}

// unverifiedSignInRefused runs at every password sign-in before a session
// or key is minted. It mails an unverified account a fresh verification link
// and refuses the sign-in once the grace has run out.
func (s *server) unverifiedSignInRefused(w http.ResponseWriter, r *http.Request, acct state.Account) bool {
	if acct.EmailVerified() {
		return false
	}
	s.sendEmailVerification(r.Context(), r, acct)
	if !s.verificationOverdueNow(acct) {
		return false
	}
	s.audit.Emit(r.Context(), "auth.sign_in_refused_unverified", &acct.ID, nil)
	api.WriteProblem(w, api.ErrEmailVerificationOverdue())
	return true
}

// overdueVerificationAllowed lists the API routes an overdue account keeps.
// Changing the address stays open: an account that signed up with a typo
// can only prove an address by moving to one it receives mail at.
var overdueVerificationAllowed = map[string]bool{
	"POST /v1/auth/verify-email/resend": true,
	"POST /v1/account/email":            true,
	"POST /v1/auth/logout":              true,
	"GET /v1/auth/csrf":                 true,
	"GET /v1/account":                   true,
	"DELETE /v1/account":                true,
}

// requireVerificationNotOverdue gates authenticated API routes for an
// overdue account. Applied in the auth facades next to requireMFA so no
// route can forget it.
func (s *server) requireVerificationNotOverdue(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		actor := actorAccount(r, acct)
		if s.verificationOverdueNow(actor) && !overdueVerificationAllowed[r.Method+" "+r.URL.Path] {
			api.WriteProblem(w, api.ErrEmailVerificationOverdue())
			return
		}
		s.remindVerificationDue(r, actor)
		next(w, r, acct)
	}
}

// dashboardOverdueRoute lists the dashboard pages an overdue account keeps:
// the account page (which offers the resend button) and the resend form.
func dashboardOverdueRoute(method, path string) bool {
	switch method + " " + path {
	case "GET /dashboard/account", "POST /dashboard/account/verify-email/resend",
		"POST /dashboard/account/email", "POST /dashboard/account/delete":
		return true
	}
	return false
}
