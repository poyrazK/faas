package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// An account created without proof of its address has emailVerificationGrace
// to verify it. Past that it is restricted, never deleted: password sign-in
// and existing sessions and keys stop working except for the routes that end
// the restriction (resend, logout, reading the account). Every channel that
// proves the address (the verification link, a magic link, Google, GitHub,
// forgot-password's magic link) verifies the account and lifts it.

// verificationOverdue reports whether acct is unverified past its grace.
func verificationOverdue(acct state.Account, now time.Time) bool {
	return !acct.EmailVerified() && !acct.CreatedAt.IsZero() && now.Sub(acct.CreatedAt) > emailVerificationGrace
}

func (s *server) verificationOverdueNow(acct state.Account) bool {
	now := time.Now()
	if s.verificationClock != nil {
		now = s.verificationClock()
	}
	return verificationOverdue(acct, now)
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
var overdueVerificationAllowed = map[string]bool{
	"POST /v1/auth/verify-email/resend": true,
	"POST /v1/auth/logout":              true,
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
		next(w, r, acct)
	}
}

// dashboardOverdueRoute lists the dashboard pages an overdue account keeps:
// the account page (which offers the resend button) and the resend form.
func dashboardOverdueRoute(method, path string) bool {
	switch method + " " + path {
	case "GET /dashboard/account", "POST /dashboard/account/verify-email/resend", "POST /dashboard/account/delete":
		return true
	}
	return false
}
