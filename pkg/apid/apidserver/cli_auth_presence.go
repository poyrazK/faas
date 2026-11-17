package apidserver

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
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
		h.renderCliAuthVerification(w, r, acct, rawCode)
		return true
	}
	if cliApprovalFresh(r, acct) {
		return false
	}
	h.srv.audit.Emit(r.Context(), "auth.recent_sign_in_required", &acct.ID, map[string]any{
		"action":     "cli_auth",
		"window_sec": int(api.RecentSignInWindow.Seconds()),
	})
	http.Redirect(w, r, cliAuthSignInURL(r, rawCode), http.StatusSeeOther)
	return true
}

func cliAuthSignInURL(r *http.Request, rawCode string) string {
	next := cliAuthPath + "?code=" + url.QueryEscape(rawCode)
	if port := cliAuthLoopbackPort(r.FormValue("port"), rawCode); port != 0 {
		next += "&port=" + strconv.Itoa(port)
	}
	if r.FormValue("return") == "mcp" {
		next += "&return=mcp"
	}
	return loginPath + "?reauth=1&next=" + url.QueryEscape(next)
}

func cliAuthSwitchAccountURL(r *http.Request, rawCode string) string {
	// Loopback approval can return to the same request. A typed code stays
	// in the terminal.
	if cliAuthLoopbackPort(r.FormValue("port"), rawCode) != 0 {
		return cliAuthSignInURL(r, rawCode)
	}
	return loginPath + "?reauth=1"
}

func (h *cliAuthHandlers) renderCliAuthVerification(w http.ResponseWriter, r *http.Request, acct state.Account, rawCode string) {
	page := dashboard.Page{
		Title: "Verify your email first",
		Body:  "cli_auth",
		Data: map[string]any{
			"EmailVerificationRequired": true,
			"AccountEmail":              acct.Email,
			"MCP":                       isMCPAuthCode(rawCode),
			"ResendToken":               h.srv.verificationResendFormToken(w, acct.ID),
			"SignInURL":                 cliAuthSwitchAccountURL(r, rawCode),
		},
	}
	if err := dashboard.Render(w, h.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		h.log.Error("cli_auth.render_verification", "err", err)
		renderProblem(w, h.log, err)
	}
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
