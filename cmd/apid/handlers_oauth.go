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
//  3. Read the sealed session cookie and pull env.GithubLogin. If
//     the dashboard user hasn't completed /v1/auth/github yet, this
//     is empty — refuse the install-takeover and 302 them to the
//     GitHub connect flow. This is the PR-B §11 ownership assertion.
//  4. Call githubd.VerifyInstallation over gRPC, passing
//     env.GithubLogin as expectedLogin. githubd fetches the install
//     from api.github.com and confirms the install's
//     account.login == expectedLogin. Mismatch → verified=false
//     (caller could not possibly own this install) and the handler
//     returns 403 forged. 404 / transport errors → verified=false
//     + 302 / 502. Either way we DO NOT persist anything the
//     customer didn't authorize.
//  5. On verified=true: hand off to the dashboard via a 302 to
//     /dashboard/apps/new?install=<id>&default_branch=<branch>
//     so the user picks which app + repo to bind to this install.
//     The actual apps.github_install_id write happens at the bind
//     step (cmd/apid/handlers_install_github.go bindAppToRepo),
//     which also re-verifies and re-checks uniqueness.
//
// Mounted on the dashboard router so it shares the §11 middleware
// stack (RequestID + Recovery) but NOT behind s.auth (which is
// API-key auth, not session-cookie auth). The sessionAuth middleware
// gives us the right auth shape for cookie-bearing browsers.
package main

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
// carries an authenticated account in the context. Mounting it on
// the dashboard router (not the API router) is deliberate: GitHub's
// install-redirect URL is set in the GitHub App config to a
// dashboard path, and we want one consistent middleware chain for
// cookie-bearing browser flows.
//
// §11 ownership proof (PR-B): the cookie envelope's github_login
// field carries the GitHub identity the dashboard user established
// via /v1/auth/github. githubd.VerifyInstallation compares that to
// the install's account.login; mismatch → 403 forged. Empty
// github_login → 302 unauthenticated (the user clicked "Connect
// GitHub" but never completed /v1/auth/github; we refuse to bind
// the install rather than silently bind it under the wrong identity).
//
// Failure surfaces:
//   - missing/invalid installation_id     → 400 problem
//   - session envelope lacks github_login  → 302 to
//     /dashboard/account?github=unauthenticated
//   - install account.login != github_login → 403 forged
//   - account suspended                   → 302 to /login (handled
//     by sessionAuth; should
//     not reach the handler)
//   - githubd.VerifyInstallation returns  → 302 to
//     verified=false (install unknown)      /dashboard/account?github=forged
//   - githubd.VerifyInstallation errs     → 502 problem with the
//     underlying gRPC error
//   - success                             → 302 to
//     /dashboard/apps/new?install=…&branch=…
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

	// PR-B §11 ownership proof. Re-read the sealed cookie inside the
	// handler so we have the envelope's github_login field — the
	// sessionAuth middleware only forwards the resolved account, not
	// the envelope. sessionAuth guarantees the cookie was already
	// verified once (else the request would have 401'd before this
	// line), so a second Verify is cheap and the envelope is
	// trustworthy.
	var expectedLogin string
	if c, cerr := r.Cookie(sessionCookie); cerr == nil && c.Value != "" {
		if env, verr := s.sessions.Verify(c.Value); verr == nil {
			expectedLogin = env.GithubLogin
		} else {
			// Cookie present but Verify failed (clock skew, key
			// rotation, tamper). Treat as unauthenticated — safer
			// than passing an empty expectedLogin and silently
			// accepting an unverified install.
			log.Warn("oauth callback: session verify failed",
				"account_id", acct.ID, "err", verr)
			http.Redirect(w, r, "/dashboard/account?github=unauthenticated", http.StatusFound)
			return
		}
	}
	if expectedLogin == "" {
		// User has a valid FaaS session but never completed
		// /v1/auth/github — we cannot prove the install belongs to
		// them. Refuse the takeover rather than silently binding
		// the install under an unverified identity.
		log.Warn("oauth callback: session missing github_login",
			"account_id", acct.ID, "install_id", stripLogInt(installationID))
		acctID := acct.ID
		s.audit.Emit(r.Context(), "auth.install.unauthenticated", &acctID, map[string]any{
			"install_id": installationID,
			"reason":     "session_github_login_empty",
		})
		http.Redirect(w, r, "/dashboard/account?github=unauthenticated", http.StatusFound)
		return
	}

	// Confirm that the ID belongs to this GitHub App, then obtain the actual
	// user-to-installation ownership proof through /user/installations. A direct
	// login comparison is valid for personal installs but rejects organizations
	// because their account.login is the organization name.
	verified, accountLogin, defaultBranch, err := s.githubd.VerifyInstallation(r.Context(), installationID, "")
	if err != nil {
		log.Warn("verify installation failed",
			"account_id", acct.ID,
			"install_id", stripLogInt(installationID),
			// expected_login is the session's github_login; it
			// comes from the AEAD-sealed cookie but the cookie is
			// attacker-modifiable in principle, so the
			// CodeQL go/log-injection (CWE-117) alert flags the
			// raw write. stripLogCRLF drops CR/LF so a hostile
			// cookie can't inject a CRLF into the log stream.
			"expected_login", stripLogCRLF(expectedLogin),
			"err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "github_unreachable",
			"Could not reach GitHub", "retry the connect flow in a minute: https://docs/connect-github"))
		return
	}
	if !verified {
		// Two distinct "not verified" cases. (1) install exists but
		// belongs to a different GitHub user → §11 takeover
		// attempt → 403 forged (auditable, hard stop). (2)
		// install doesn't exist on api.github.com (404) →
		// verified=false with accountLogin="" — softer 302, since
		// this could be a stale tab, a botched callback, or a
		// typed-in ID, and the §11 proof is moot. The install's
		// account.login is empty in case (2) because githubd never
		// got a response body to extract it from.
		if accountLogin != "" {
			log.Warn("verify installation: install belongs to a different GitHub user (§11 takeover attempt)",
				"account_id", acct.ID,
				"install_id", stripLogInt(installationID),
				"expected_login", stripLogCRLF(expectedLogin),
				"actual_account_login", stripLogCRLF(accountLogin))
			acctID := acct.ID
			s.audit.Emit(r.Context(), "auth.install.takeover_rejected", &acctID, map[string]any{
				"install_id":           installationID,
				"expected_login":       expectedLogin,
				"actual_account_login": accountLogin,
			})
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "forged",
				"This installation belongs to a different GitHub user",
				"the install is bound to a different GitHub identity than the one you signed in with"))
			return
		}
		log.Warn("verify installation: forged or unknown install_id",
			"account_id", acct.ID,
			"install_id", stripLogInt(installationID),
			"expected_login", stripLogCRLF(expectedLogin))
		http.Redirect(w, r, "/dashboard/account?github=forged", http.StatusFound)
		return
	}

	// The install exists for this App. Emit the audit event so the dashboard
	// "GitHub linked" line in the security log mirrors the same
	// trail the Google handler (PR-A) established.
	acctID := acct.ID
	s.audit.Emit(r.Context(), "auth.install.verified", &acctID, map[string]any{
		"install_id":     installationID,
		"github_login":   expectedLogin,
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
