package apidserver

import (
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// cliApprovalRefused holds a CLI or MCP sign-in approval to what minting the
// same key directly needs (POST /v1/keys: verified email and a fresh
// step-up). An approved code becomes an admin-scoped key that outlives the
// session, so a copied browser cookie must not be able to mint one
// (account-flow audit 2026-11-10, ADR 20261111110000). A stale session is
// sent to sign in again and comes back to the same code. It reports whether
// it has answered the request.
func (h *cliAuthHandlers) cliApprovalRefused(w http.ResponseWriter, r *http.Request, acct state.Account, rawCode string) bool {
	if !acct.EmailVerified() {
		h.srv.audit.Emit(r.Context(), "auth.cli_approval_refused", &acct.ID, map[string]any{"reason": "email_unverified"})
		h.renderCliAuthError(w, r, "Verify your email first",
			"Open the verification link Gregale emailed you, then run the sign-in again.")
		return true
	}
	if cliApprovalFresh(r, acct) {
		return false
	}
	h.srv.audit.Emit(r.Context(), "auth.recent_sign_in_required", &acct.ID, map[string]any{
		"action":     "cli_auth",
		"window_sec": int(api.RecentSignInWindow.Seconds()),
	})
	next := cliAuthPath + "?code=" + url.QueryEscape(rawCode)
	if r.FormValue("return") == "mcp" {
		next += "&return=mcp"
	}
	http.Redirect(w, r, loginPath+"?reauth=1&next="+url.QueryEscape(next), http.StatusSeeOther)
	return true
}

// cliApprovalFresh reports whether the browser proved itself recently: a
// two-factor code within RecentSignInWindow when the account has one, else
// a sign-in within it.
func cliApprovalFresh(r *http.Request, acct state.Account) bool {
	if acct.MFAEnrolled() {
		ts, has := authmw.StepUpFrom(r)
		return has && !ts.IsZero() && time.Since(ts) <= api.RecentSignInWindow
	}
	return signedInRecently(r)
}
