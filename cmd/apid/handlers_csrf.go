package main

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// csrfActions exposed by the browser API are deliberately closed. A token is
// bound to one action and one account, so accepting arbitrary action names
// here would make the endpoint a generic signer for future mutations.
// csrfActionSetPassword binds the token POST /dashboard/account/set-password
// verifies (ADR-140).
const csrfActionSetPassword = "set_password"

var csrfActions = map[string]struct{}{
	csrfActionLogout:            {},
	csrfActionSessionRevoke:     {},
	csrfActionSessionsRevokeAll: {},
	"mfa_confirm":               {},
	"mfa_recover":               {},
	"mfa_disable":               {},
	csrfActionSetPassword:       {},
}

// issueCSRFToken mints the double-submit token used by browser mutations.
// The cookie is HttpOnly; the JSON copy is the only value the SPA needs to
// send back in its request body. This endpoint is also MFA-allowlisted so a
// pending session can obtain the token required to complete the MFA flow.
func (s *server) issueCSRFToken(w http.ResponseWriter, r *http.Request, acct state.Account) {
	action := strings.TrimSpace(r.URL.Query().Get("action"))
	if _, ok := csrfActions[action]; !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF action", "the requested action is not available to browser clients"))
		return
	}

	tok, err := middleware.IssueForAuthenticated(s.sessions, action, acct.ID)
	if err != nil {
		s.log.Error("auth.csrf.issue", "action", action, "account_id", acct.ID, "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not issue CSRF token"))
		return
	}
	setDashboardCSRFCookie(w, s, tok)
	writeJSON(w, http.StatusOK, api.CSRFTokenResponse{CSRFToken: tok})
}
