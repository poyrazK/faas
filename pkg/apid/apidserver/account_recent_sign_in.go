package apidserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A stolen browser session must not be enough to take an account over.
// Account changes ask for the strongest proof the account has: a fresh
// authenticator code, else the current password. An account with neither
// (signed up with Google, GitHub or an email link) has only the session, so
// that session must have signed in within api.RecentSignInWindow: a cookie
// copied out of an old browser has to sign in again first
// (ADR 20261110100000).

const (
	recentSignInFlashKey = "recent_sign_in"
	recentSignInFlash    = "For your security, sign in again to make that change."
)

// signedInRecently reports whether r's browser session signed in within
// api.RecentSignInWindow. sessions.issued_at is the sign-in time: renewing
// the cookie does not move it.
func signedInRecently(r *http.Request) bool {
	sess, ok := sessionFrom(r)
	return ok && !sess.IssuedAt.IsZero() && time.Since(sess.IssuedAt) <= api.RecentSignInWindow
}

// requireRecentSignIn lets the change through when the session signed in
// recently. Otherwise it audits the refusal, writes recent_sign_in_required
// and returns false. action names the change in the audit row.
func (s *server) requireRecentSignIn(w http.ResponseWriter, r *http.Request, acct state.Account, action string) bool {
	if signedInRecently(r) {
		return true
	}
	s.audit.Emit(r.Context(), "auth.recent_sign_in_required", &acct.ID, map[string]any{
		"action":     action,
		"window_sec": int(api.RecentSignInWindow.Seconds()),
	})
	api.WriteProblem(w, api.ErrRecentSignInRequired())
	return false
}

// mfaEnrollFirstFactorReady holds an enrollment on an account without a
// password to a recent sign-in. An account with a password proves it at
// confirm instead.
func (s *server) mfaEnrollFirstFactorReady(w http.ResponseWriter, r *http.Request, acct state.Account) bool {
	_, err := s.store.AccountPasswordByAccountID(r.Context(), acct.ID)
	switch {
	case err == nil:
		return true
	case !errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.ErrCapacity("could not check enrollment credentials"))
		return false
	}
	return s.requireRecentSignIn(w, r, acct, "mfa_enroll")
}

// redirectRecentSignIn sends an account-page form to sign-in, coming back
// to the account page.
func redirectRecentSignIn(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, loginPath+"?reauth=1&next="+accountPagePath, http.StatusSeeOther)
}
