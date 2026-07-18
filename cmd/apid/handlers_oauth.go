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
//  3. Call githubd.VerifyInstallation over gRPC, which mints the
//     App JWT and confirms the install exists on api.github.com.
//     A 404 → verified=false (forged or stale id); transport
//     errors → non-nil err. Either way we DO NOT persist anything
//     the customer didn't authorize.
//  4. On verified=true: hand off to the dashboard via a 302 to
//     /dashboard/apps/new?install=<id>&default_branch=<branch>
//     so the user picks which app + repo to bind to this install.
//     The actual apps.github_install_id write happens at the bind
//     step (cmd/apid/handlers_dashboard.go bindAppRoute), which
//     also re-verifies and re-checks uniqueness.
//
// Mounted on the dashboard router so it shares the §11 middleware
// stack (RequestID + Recovery) but NOT behind s.auth (which is
// API-key auth, not session-cookie auth). The sessionAuth middleware
// gives us the right auth shape for cookie-bearing browsers.
package main

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

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
// Failure surfaces:
//   - missing/invalid installation_id     → 400 problem
//   - account suspended                   → 302 to /login (handled
//                                           by sessionAuth; should
//                                           not reach the handler)
//   - githubd.VerifyInstallation returns  → 302 to
//     verified=false                        /dashboard/account?github=forged
//   - githubd.VerifyInstallation errs     → 500 problem with the
//                                           underlying gRPC error
//   - success                             → 302 to
//                                           /dashboard/apps/new?install=…&branch=…
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
	log.Info("oauth callback received",
		"account_id", acct.ID,
		"installation_id", installationID,
		"setup_action", setupAction)

	verified, defaultBranch, err := s.githubd.VerifyInstallation(r.Context(), installationID)
	if err != nil {
		log.Warn("verify installation failed", "account_id", acct.ID, "install_id", installationID, "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "github_unreachable",
			"Could not reach GitHub", "retry the connect flow in a minute: https://docs/connect-github"))
		return
	}
	if !verified {
		log.Warn("verify installation: forged or unknown install_id",
			"account_id", acct.ID, "install_id", installationID)
		http.Redirect(w, r, "/dashboard/account?github=forged", http.StatusFound)
		return
	}

	// Successful verify — hand the user to the bind picker. We
	// deliberately don't persist anything yet: the apps row gets
	// the github_install_id write at the bind step (which also
	// re-runs the verify-then-uniqueness check via the same Store
	// method). That way a stale dashboard tab can't accidentally
	// bind an install the user revoked between the callback and
	// the bind click.
	//
	// Build the redirect target from the parsed int64
	// (installationID), not the raw query string — that way a
	// crafted URL like ?installation_id=1%26setup_action=… can't
	// smuggle an extra query param into the redirect (gosec
	// G710 open-redirect taint). The dashboard bind picker also
	// re-validates installationID is a positive int before it
	// persists anything, defense in depth.
	q := url.Values{}
	q.Set("install", strconv.FormatInt(installationID, 10))
	if defaultBranch != "" {
		q.Set("default_branch", defaultBranch)
	}
	http.Redirect(w, r, "/dashboard/apps/new?"+q.Encode(), http.StatusFound)
}

// _ keeps slog import live for future structured logging added
// alongside the bind-picker redirect (today slog is used via s.log).
var _ = slog.Default