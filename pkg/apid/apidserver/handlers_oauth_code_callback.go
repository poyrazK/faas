// PR-C: GET /oauth/code-callback handler — closes the user-to-server
// OAuth handshake.
//
// Why a second /oauth/code-callback route (sibling of /oauth/callback):
//
//	/oauth/callback (handlers_oauth.go) handles the GitHub App INSTALL
//	callback, where GitHub redirects to
//	  /oauth/callback?installation_id=N&setup_action=install
//	after the customer clicks "Install" on github.com. That branch is
//	"trust on first contact" — apid verifies the install exists and
//	belongs to the dashboard user's GitHub identity, then redirects to
//	the bind picker (/dashboard/apps/new).
//
//	/oauth/code-callback (this file) handles the GitHub App USER OAuth
//	callback, where the dashboard's "Connect GitHub" button sends the
//	user to
//	  https://github.com/login/oauth/authorize?client_id=…&state=…
//	and GitHub redirects back with
//	  /oauth/code-callback?code=…&state=…
//	That branch is the user-to-server OAuth handshake: githubd
//	exchanges the code for an install token, seals it under the host
//	age key, persists to the durable github_installations table, and
//	emits the auth.install.token_sealed audit event. After that, the
//	/v1/install/* and /v1/apps/{slug}/install/bind routes succeed
//	across githubd restarts.
//
// Two callbacks, two URLs, two CSRF tokens — both scoped narrowly
// so a stale tab can't replay one against the other.
//
// CSRF posture: the state token is sealed with the session key to the
// signed-in account, the selected installation and a 5-minute expiry
// (bug hunt 2026-10-28 H64), and also stored in a short-lived cookie
// (host-only `__Host-` over HTTPS). The handler requires the cookie to
// match the query string and the sealed state to name the signed-in
// account, and rejects anything else (with an audit event).
//
// Failure surfaces:
//   - missing code or state query param   → 400 problem
//   - missing or mismatched CSRF cookie   → 403 csrf_rejected + audit
//   - missing GitHub App installation      → redirect to GitHub install flow
//   - installation belongs to another account → 409 github_account_conflict
//   - githubd.ExchangeOAuthCode errs      → 502 problem
//   - success                             → 302 to the saved dashboard return
//     target (or /dashboard/account) with github=connected, install=<id>,
//     and default_branch=<branch>
package apidserver

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
)

const (
	// oauthCodeCallbackPath is the GitHub App user-to-server OAuth
	// callback URL. Sibling of oauthCallbackPath in handlers_oauth.go.
	oauthCodeCallbackPath = "/oauth/code-callback"

	// oauthCodeStateCookie is the CSRF state cookie for the
	// /oauth/code-callback handler. Scoped narrowly to
	// /oauth/code-callback so it cannot be replayed against the
	// /oauth/callback handler (PR-B's existing flow) or the
	// /v1/auth/github/* login flow.
	oauthCodeStateCookie = "faas_oauth_code_state"

	// oauthCodeStateTTL is the cookie + state token lifetime. The
	// GitHub OAuth round-trip is typically well under 60 s; 5
	// minutes is the upper bound for a user completing the dialog.
	oauthCodeStateTTL = 5 * time.Minute

	// defaultGitHubAppInstallURL is the hosted Gregale App's installation
	// endpoint. Self-hosted deployments can override it with
	// FAAS_GITHUB_APP_INSTALL_URL (for a different App slug or GitHub
	// Enterprise host).
	defaultGitHubAppInstallURL = "https://github.com/apps/gregale-platform/installations/new"

	// githubNoAppInstallError is the stable diagnostic currently returned by
	// githubd when the OAuth user has not installed this App yet. The gRPC
	// boundary preserves the diagnostic as an Internal status today, so the
	// apid edge recognizes this narrow condition and resumes the install flow
	// instead of presenting it as a GitHub outage.
	githubNoAppInstallError = "user has no app installations"
)

// githubAppInstallURL adds the OAuth state to the operator-configured GitHub
// App installation URL. The URL is configuration, never request input, but we
// still require an absolute HTTPS URL so a bad deployment cannot turn this
// handler into an open redirect.
func githubAppInstallURL(stateToken string) (string, error) {
	raw := os.Getenv("FAAS_GITHUB_APP_INSTALL_URL")
	if raw == "" {
		raw = defaultGitHubAppInstallURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != schemeHTTPS || u.Host == "" {
		return "", fmt.Errorf("FAAS_GITHUB_APP_INSTALL_URL must be an absolute HTTPS URL")
	}
	query := u.Query()
	query.Set("state", stateToken)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func isGithubAppNotInstalled(err error) bool {
	return err != nil && strings.Contains(err.Error(), githubNoAppInstallError)
}

// issueOAuthCodeState mints a 16-byte CSRF state token and writes it
// to a short-lived cookie scoped to /oauth/code-callback. The
// dashboard's "Connect GitHub" button renderer calls this before
// building the GitHub authorize URL. Random source: crypto/rand
// (not math/rand) — the state is the only thing standing between
// an attacker and a forged user-to-server token exchange.
func (s *server) issueOAuthCodeState(w http.ResponseWriter, r *http.Request) (string, error) {
	return s.issueOAuthCodeStateForInstallation(w, r, 0)
}

func (s *server) issueOAuthCodeStateForInstallation(w http.ResponseWriter, r *http.Request, installationID int64) (string, error) {
	acct, ok := AccountFrom(r.Context())
	if !ok || acct.ID == "" || s.sessions == nil {
		return "", errors.New("oauth code state: no signed-in account or session key")
	}
	stateToken, err := sealOAuthCodeState(s.sessions, acct.ID, installationID)
	if err != nil {
		return "", err
	}
	setOAuthFlowCookie(w, r, oauthCodeStateCookie, stateToken, oauthCodeCallbackPath)
	return stateToken, nil
}

// sealOAuthCodeState seals the Connect GitHub state to the signed-in
// account, the selected installation and an expiry. A cookie alone proved
// nothing: any tenant page on a sibling host could plant a matching cookie
// and send a signed-in customer to the callback with the attacker's GitHub
// code (bug hunt 2026-10-28 H64). githubd still reads the installation
// after the last '.'; base64url never contains one.
func sealOAuthCodeState(sessions *session.Manager, accountID string, installationID int64) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	envelope, err := json.Marshal(oauthCodeStateEnvelope{
		AccountID: accountID, InstallationID: installationID,
		Nonce: hex.EncodeToString(nonce), Expires: time.Now().Add(oauthCodeStateTTL),
	})
	if err != nil {
		return "", err
	}
	stateToken, err := sessions.SealForCSRF(envelope)
	if err != nil {
		return "", err
	}
	if installationID > 0 {
		stateToken += "." + strconv.FormatInt(installationID, 10)
	}
	return stateToken, nil
}

// oauthCodeStateEnvelope is the sealed half of the Connect GitHub state.
type oauthCodeStateEnvelope struct {
	AccountID      string    `json:"a"`
	InstallationID int64     `json:"i,omitempty"`
	Nonce          string    `json:"n"`
	Expires        time.Time `json:"e"`
}

// verifyOAuthCodeState reports whether state was sealed by this server for
// accountID, is unexpired, and names the same installation it carries.
func (s *server) verifyOAuthCodeState(state, accountID string) bool {
	if s.sessions == nil {
		return false
	}
	sealed, suffix, hasSuffix := strings.Cut(state, ".")
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return false
	}
	plaintext, err := s.sessions.OpenForCSRF(raw)
	if err != nil {
		return false
	}
	var env oauthCodeStateEnvelope
	if json.Unmarshal(plaintext, &env) != nil || env.AccountID != accountID || !env.Expires.After(time.Now()) {
		return false
	}
	if hasSuffix {
		return suffix == strconv.FormatInt(env.InstallationID, 10)
	}
	return env.InstallationID == 0
}

// redirectToGitHubAuthorization obtains the user-to-server proof that the
// signed-in GitHub user can access the selected personal or organization App
// installation. The selected ID is bound into the CSRF state cookie.
func (s *server) redirectToGitHubAuthorization(w http.ResponseWriter, r *http.Request, installationID int64) bool {
	clientID := os.Getenv("FAAS_GITHUB_APP_CLIENT_ID")
	if clientID == "" {
		s.log.Error("GitHub App connection is unavailable", "reason", "client ID is not configured")
		api.WriteProblem(w, githubAppUnavailableProblem())
		return false
	}
	stateToken, err := s.issueOAuthCodeStateForInstallation(w, r, installationID)
	if err != nil {
		s.log.Error("issue OAuth state for GitHub App authorization", "err", err)
		api.WriteProblem(w, api.ErrInternal("Gregale could not start the GitHub connection flow.").WithHint("Retry in a moment; if it continues, contact support."))
		return false
	}
	redirectURI := os.Getenv("FAAS_GITHUB_APP_REDIRECT_URI")
	if redirectURI == "" {
		// The API listener may be reached through a separate console proxy.
		// Use its configured public URL rather than the upstream request host.
		redirectURI = normalizeCLIAuthURLBase(s.cliAuthURLBase) + oauthCodeCallbackPath
	}
	u := "https://github.com/login/oauth/authorize?client_id=" + url.QueryEscape(clientID) +
		"&redirect_uri=" + url.QueryEscape(redirectURI) +
		"&state=" + url.QueryEscape(stateToken) + "&scope="
	http.Redirect(w, r, u, http.StatusFound)
	return true
}

func githubAppUnavailableProblem() *api.Problem {
	return api.NewProblem(http.StatusServiceUnavailable, api.CodeOAuthProviderUnavailable,
		"GitHub connection unavailable",
		"GitHub connections are not available for this Gregale installation.").
		WithHint("Try again later, or contact support if you need to connect GitHub.").
		WithDocs("https://gregale.dev/docs/auth")
}

// renderOAuthCodeCallback is the GET /oauth/code-callback handler.
// Mounted in server.go behind sessionAuth so the request already
// carries an authenticated account in the context — the OAuth
// handshake is account-scoped (github_installations.account_id is
// the FK).
func (s *server) renderOAuthCodeCallback(w http.ResponseWriter, r *http.Request) {
	const op = "renderOAuthCodeCallback"
	log := s.log.With("op", op)

	acct, ok := AccountFrom(r.Context())
	if !ok {
		// sessionAuth would have redirected before this; defend
		// against a future refactor that drops the middleware.
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "sign in to connect GitHub"))
		return
	}

	// Read query parameters. We order state check BEFORE code
	// check so a malformed callback can't probe the githubd gRPC
	// surface with bad inputs — the CSRF gate runs first.
	queryState := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if queryState == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_request",
			"Missing state", "/oauth/code-callback requires a state query parameter"))
		return
	}
	if code == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_request",
			"Missing code", "/oauth/code-callback requires a code query parameter"))
		return
	}

	// CSRF check via the narrowly-scoped state cookie.
	stateCookie, err := oauthFlowCookie(r, oauthCodeStateCookie)
	if err != nil || stateCookie.Value == "" {
		log.Warn("oauth code callback: missing CSRF state cookie",
			"account_id", acct.ID)
		acctID := acct.ID
		s.audit.Emit(r.Context(), "auth.install.csrf_rejected", &acctID, map[string]any{
			"reason": "missing_state_cookie",
		})
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "csrf_missing",
			"Missing CSRF state", "the Connect GitHub flow requires a recent 'Connect GitHub' click — restart the flow"))
		return
	}
	// Constant-time compare against the query string. The cookie
	// and query are both 32 hex chars (16 bytes random), so the
	// only sane attack is replay with a leaked cookie — the
	// short TTL + narrow Path scope is what makes that tractable.
	if subtle.ConstantTimeCompare([]byte(stateCookie.Value), []byte(queryState)) != 1 || !s.verifyOAuthCodeState(queryState, acct.ID) {
		log.Warn("oauth code callback: state mismatch",
			"account_id", acct.ID,
			// CodeQL go/log-injection (CWE-117): both halves are
			// already hex-encoded and bounded to 32 chars by the
			// generator, but defense-in-depth keeps the same
			// sanitiser shape every other audit log in this file
			// uses (MEMORY.md/codeql-go-log-injection-sanitisers).
			"cookie_len", len(stateCookie.Value),
			"query_len", len(queryState))
		acctID := acct.ID
		s.audit.Emit(r.Context(), "auth.install.csrf_rejected", &acctID, map[string]any{
			"reason": "state_mismatch",
		})
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "csrf_mismatch",
			"CSRF mismatch", "the OAuth state token does not match the cookie — restart the Connect GitHub flow"))
		return
	}

	// Clear the state cookie immediately. It's single-use: a
	// successful exchange shouldn't leave the token lying around
	// for a redirect-replay to use.
	clearPath := oauthCodeCallbackPath
	if requestIsHTTPS(r) {
		clearPath = "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthFlowCookieName(r, oauthCodeStateCookie),
		Value:    "",
		Path:     clearPath,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	// Hand the code to githubd. githubd mints the install token,
	// seals under the host age key, persists to github_installations,
	// and emits auth.install.token_sealed via its audit callback. The
	// apid-side audit here fires only on the githubd error path; the
	// success path's audit row is the githubd one (sealed-token
	// permanence is the audit-worthy event).
	installID, defaultBranch, err := s.githubd.ExchangeOAuthCode(r.Context(), acct.ID, code, queryState)
	if err != nil {
		if p := api.AsProblem(err); p != nil && p.Code == api.CodeGitHubAccountConflict {
			log.Warn("oauth code callback: GitHub installation belongs to another account", "account_id", acct.ID)
			acctID := acct.ID
			s.audit.Emit(r.Context(), "auth.install.account_conflict", &acctID, map[string]any{
				"reason": api.CodeGitHubAccountConflict,
			})
			api.WriteProblemForRequest(w, r, api.ErrGitHubAccountConflict())
			return
		}
		if isGithubAppNotInstalled(err) {
			log.Warn("oauth code callback: GitHub App installation required",
				"account_id", acct.ID)
			acctID := acct.ID
			s.audit.Emit(r.Context(), "auth.install.install_required", &acctID, map[string]any{
				"reason": "github_app_not_installed",
			})
			if s.redirectToGitHubAppInstall(w, r) {
				return
			}
			return
		}
		log.Error("oauth code callback: githubd ExchangeOAuthCode failed",
			"account_id", acct.ID,
			"err", err)
		acctID := acct.ID
		s.audit.Emit(r.Context(), "auth.install.token_exchange_failed", &acctID, map[string]any{
			"githubd_err": stripLogCRLF(err.Error()),
		})
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "github_unreachable",
			"Could not reach GitHub", "retry the connect flow in a minute: https://docs/connect-github"))
		return
	}

	// PR-C also adds defaultBranch to the redirect query — the
	// dashboard success page uses it to highlight "we picked
	// <branch> as your default" and prime the bind picker with the
	// right branch. Empty is fine (some installs use a non-default
	// branch and the user picks manually).
	q := url.Values{}
	q.Set("github", "connected")
	if installID != "" {
		q.Set("install", installID)
	}
	if defaultBranch != "" {
		q.Set("default_branch", defaultBranch)
	}
	q.Set("connected_at", strconv.FormatInt(time.Now().Unix(), 10))
	returnTo := githubConnectReturnTo(r)
	clearGitHubConnectReturn(w, r)
	http.Redirect(w, r, githubConnectedRedirect(returnTo, q), http.StatusFound)
}

// redirectToGitHubAppInstall starts a fresh state-protected installation
// round-trip when the OAuth callback confirms that GitHub has no installation.
func (s *server) redirectToGitHubAppInstall(w http.ResponseWriter, r *http.Request) bool {
	stateToken, err := s.issueOAuthCodeState(w, r)
	if err != nil {
		s.log.Error("issue oauth code state for GitHub App installation", "err", err)
		api.WriteProblem(w, api.ErrInternal("Gregale could not start the GitHub connection flow.").WithHint("Retry in a moment; if it continues, contact support."))
		return false
	}
	installURL, err := githubAppInstallURL(stateToken)
	if err != nil {
		s.log.Error("build GitHub App installation URL", "err", err)
		api.WriteProblem(w, githubAppUnavailableProblem())
		return false
	}
	http.Redirect(w, r, installURL, http.StatusFound)
	return true
}

// startConnectGitHub (POST /dashboard/install/connect) is the
// dashboard "Connect GitHub" button click handler. It mints a
// narrow CSRF state cookie scoped to /oauth/code-callback and
// redirects the browser to GitHub's user authorization URL. The OAuth callback
// discovers installations on GitHub and starts installation only if none exist.
// A validated local dashboard return target is carried across both callbacks.
//
// POST-only (not GET) so an opportunistic <img src=…> cannot
// mint a state cookie and trip a CSRF path. Same posture as
// handlers_github.go:81 (renderGitHubAuthRedirect is the same
// shape, but the dashboard chrome triggers it on click; here we
// POST because the click is rendered from a form).
//
// GetInstallState only describes a completed Gregale connection, so a missing
// local record cannot prove the App is absent on GitHub. Sending such accounts
// straight to installation strands existing installations on GitHub's settings
// page without an OAuth callback. GitHub's authorization and installation
// approval steps remain explicit.
func (s *server) startConnectGitHub(w http.ResponseWriter, r *http.Request) {
	const op = "startConnectGitHub"
	log := s.log.With("op", op)

	acct, ok := AccountFrom(r.Context())
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "sign in to connect GitHub"))
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, githubConnectAction, acct.ID, githubConnectCSRFCookie); err != nil {
		http.Redirect(w, r, "/dashboard/account?github=connect-forbidden", http.StatusSeeOther)
		return
	}
	setGitHubConnectReturn(w, r, r.FormValue("return_to"))

	clientID := os.Getenv("FAAS_GITHUB_APP_CLIENT_ID")
	if clientID == "" {
		log.Error("FAAS_GITHUB_APP_CLIENT_ID not configured")
		api.WriteProblem(w, githubAppUnavailableProblem())
		return
	}

	if !s.redirectToGitHubAuthorization(w, r, 0) {
		log.Error("redirect to GitHub authorization failed")
	}
}

// stripLogCRLF / stripLogInt64 are defined in handlers_install_github.go
// (same package). Both files share the helpers automatically; see that
// file for the rationale on why CodeQL go/log-injection still flags
// seemingly-safe values — defense in depth.

// _ keeps slog import live for future structured logging.
var _ = slog.Default
