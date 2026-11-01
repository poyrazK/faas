// /oauth/callback handler (review finding #1+#2 closure).
//
// The M7.5 PR shipped a githubd.RealService.ExchangeOAuthCode that
// accepted whatever installation_id the caller handed it with no
// verification against api.github.com. This was the §11 least-
// privilege regression ADR-012 was meant to prevent — a forged
// callback could claim any installation_id, and tokensForRepo
// hardcoded installation_id=1 so every CheckRun went out under
// one customer's install token.
//
// The handler in this file closes the gap:
//
//  1. sessionAuth ensures the request is from a logged-in dashboard
//     user. We need an account context because the bind row is
//     account-scoped; an unauthenticated callback is a forged one.
//  2. Read installation_id from the query (the GitHub App install
//     callback shape: ?installation_id=N&setup_action=install).
//  3. Call githubd.VerifyInstallation over gRPC to confirm the ID is an
//     installation of this GitHub App. 404 / transport errors →
//     302 / 502. Nothing is persisted here.
//  4. On verified=true: redirect to GitHub's user authorization with the
//     installation bound into the OAuth state. /oauth/code-callback then
//     proves that the GitHub user may connect it (its owner, or an admin
//     of its organization) and persists the connection. A GitHub sign-in
//     on the Gregale session is not required (ADR-20261101160300).
//
// Mounted on the dashboard router so it shares the §11 middleware
// stack (RequestID + Recovery) but NOT behind s.auth (which is
// API-key auth, not session-cookie auth). The sessionAuth middleware
// gives us the right auth shape for cookie-bearing browsers.
package apidserver

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

// stripLogInt is defined in handlers_install_github.go (same package);
// both files share the helper automatically. See that file for the
// rationale on why int64 log values still need the dataflow break.

// oauthCallbackPath is the GitHub App install callback URL that
// the dashboard's "Connect GitHub" button targets. Kept distinct
// from loginPath / verifyPath in handlers_auth.go so a future
// caller grepping for "oauth" lands here.
const oauthCallbackPath = "/oauth/callback"

// renderOAuthCallback is the GET /oauth/callback handler. It is
// mounted in server.go behind sessionAuth so the request already
// carries an authenticated account in the context. GitHub's
// install-redirect (Setup URL) points here.
//
// Failure surfaces:
//   - missing/invalid installation_id     → 400 problem
//   - account suspended                   → 302 to /login (handled
//     by sessionAuth; should not reach the handler)
//   - githubd.VerifyInstallation returns  → 302 to
//     verified=false (install unknown)      /dashboard/account?github=forged
//   - githubd.VerifyInstallation errs     → 502 problem
//   - success                             → 302 to GitHub's user
//     authorization, then /oauth/code-callback
func (s *server) renderOAuthCallback(w http.ResponseWriter, r *http.Request) {
	const op = "renderOAuthCallback"
	log := s.log.With("op", op)

	acct, ok := AccountFrom(r.Context())
	if !ok {
		// sessionAuth would have redirected before this; defend
		// against a future refactor that drops the middleware.
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "sign in to connect GitHub"))
		return
	}

	installIDStr := r.URL.Query().Get("installation_id")
	if installIDStr == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_request",
			"Missing installation_id", "the GitHub App install callback must include ?installation_id=…"))
		return
	}
	installationID, err := strconv.ParseInt(installIDStr, 10, 64)
	if err != nil || installationID <= 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_request",
			"Invalid installation_id", "installation_id must be a positive integer"))
		return
	}

	// Optional setup_action tells us whether the install is brand new
	// ("install") or a re-install with updated permissions ("update").
	// We log it but don't gate the flow on it — both shapes warrant
	// a fresh /dashboard/apps/new visit so the user can re-confirm
	// the binding.
	setupAction := r.URL.Query().Get("setup_action")
	// CodeQL go/log-injection (CWE-117): setupAction arrives from the
	// GitHub App install callback query string (`?setup_action=…`); a
	// hostile redirect can stuff CR/LF into it. installationID is a
	// query-string int, but slog.Any() renders it through fmt and a
	// future type relaxation could taint the audit line. Wrap both
	// through logsanitize so the audit field stays one-line-per-event.
	log.Info("oauth callback received",
		"account_id", acct.ID,
		"installation_id", stripLogInt(installationID),
		"setup_action", stripLogCRLF(setupAction))

	// Confirm that the ID belongs to this GitHub App. This callback persists
	// nothing: the OAuth step it redirects to proves that the signed-in
	// GitHub user may connect the installation, whatever provider the
	// Gregale session signed in with (ADR-20261101160300 amends ADR-1345,
	// which kept a GitHub sign-in gate here and stranded Google and password
	// accounts after installing the App).
	verified, accountLogin, defaultBranch, err := s.githubd.VerifyInstallation(r.Context(), installationID, "")
	if err != nil {
		log.Warn("verify installation failed",
			"account_id", acct.ID,
			"install_id", stripLogInt(installationID),
			"err", err)
		api.WriteProblem(w, githubUnreachableProblem("Retry the connect flow in a minute."))
		return
	}
	if !verified {
		log.Warn("verify installation: forged or unknown install_id",
			"account_id", acct.ID,
			"install_id", stripLogInt(installationID))
		http.Redirect(w, r, "/dashboard/account?github=forged", http.StatusFound)
		return
	}

	// The install exists for this App. Emit the audit event so the dashboard
	// "GitHub linked" line in the security log mirrors the same
	// trail the Google handler (PR-A) established.
	acctID := acct.ID
	s.audit.Emit(r.Context(), "auth.install.verified", &acctID, map[string]any{
		"install_id":     installationID,
		"install_owner":  accountLogin,
		"default_branch": defaultBranch,
	})

	// Bind the selected installation into a fresh OAuth state and prove that
	// this signed-in GitHub user can see it. Only that flow persists the token
	// and unlocks the repository picker.
	if !s.redirectToGitHubAuthorization(w, r, installationID) {
		return
	}
}

// _ keeps slog import live for future structured logging added
// alongside the bind-picker redirect (today slog is used via s.log).
var _ = slog.Default
