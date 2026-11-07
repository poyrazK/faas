package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Resending the address-verification link used to require signing in
// again with a password, which an OAuth- or magic-link-created account and
// a session that outlived the 24-hour link could not do. Both surfaces
// below reuse sendEmailVerification, so the per-account security-mail
// throttle (security_mail_limit.go) bounds how often they mail.

const (
	dashboardVerifyResendAction     = "account_verify_resend"
	dashboardVerifyResendCSRFCookie = "faas_csrf_account_verify_resend"
)

// resendEmailVerification is POST /v1/auth/verify-email/resend. It answers
// 202 whether or not a mail went out (throttled sends are audited), and
// 200 already_verified when there is nothing to resend.
func (s *server) resendEmailVerification(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if acct.EmailVerified() {
		writeJSON(w, http.StatusOK, map[string]any{"status": "already_verified"})
		return
	}
	s.sendEmailVerification(r.Context(), r, acct)
	s.audit.Emit(r.Context(), "account.email_verification_resent", &acct.ID, nil)
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "sent", "email": acct.Email})
}

// dashboardResendVerification is the account page's form for the same
// action. It redirects back to the account page with a flash.
func (s *server) dashboardResendVerification(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(
		s.sessions, r, dashboardVerifyResendAction, acct.ID, dashboardVerifyResendCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	if !acct.EmailVerified() {
		s.sendEmailVerification(r.Context(), r, acct)
		s.audit.Emit(r.Context(), "account.email_verification_resent", &acct.ID, nil)
	}
	http.Redirect(w, r, "/dashboard/account?verification=sent", http.StatusSeeOther)
}
