// CLI auth device-code handlers (spec §2.2).
//
// The flow:
//
//  1. POST /v1/cli-auth/code      — CLI mints a code anonymously. Server
//     returns {code, url, expires_at}.
//  2. GET  /cli-auth?code=…        — The public console forwards this
//     request to apid. A normal dashboard session is required before
//     apid renders the authorization form.
//  3. POST /cli-auth               — The authenticated dashboard claims
//     the code for its own account, fires NotifyCliAuthCodeActivated,
//     and redirects to /dashboard/account.
//  4. POST /v1/cli-auth/exchange   — CLI polls this every second until
//     the user approves in the browser. On approval the server mints
//     a fresh API key (api.GenerateAPIKey) and returns the plaintext
//     exactly once. 404 cli_auth_code_pending → keep polling. 410
//     cli_auth_code_unavailable → stop with a clear error.
//
// Step 1 is anonymous on purpose: the CLI hasn't logged in yet. It
// uses its own per-IP rate-limit bucket (s.cliAuthLimiter, see
// server.go::cliAuthChain) so a brute-force on codes cannot starve the
// dashboard /login bucket or the bearer-token auth surface.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// cliAuthCodeTTL is how long a minted code stays claimable. UX
	// §2.2 promises 5 minutes; spec §11 limits the lifetime of any
	// session-init artifact.
	cliAuthCodeTTL = 5 * time.Minute
	// cliAuthPath is the dashboard-side route that mirrors the
	// existing magic-link /login surface.
	cliAuthPath = "/cli-auth"
	// cliAuthDashboard is where /cli-auth POST redirects after a
	// successful claim. The user lands on their account page so the
	// browser is logged in alongside the CLI.
	cliAuthDashboard = "/dashboard/account"
)

// cliAuthHandlers is the sibling of authHandlers, scoped narrowly to
// the device-code flow so the auth surface doesn't grow into one
// 600-line file. Holds the same server reference; no new state.
type cliAuthHandlers struct {
	srv     *server
	log     *slog.Logger
	domain  string // apps base URL — same as authHandlers.domain
	urlBase string // public web origin forwarding the /cli-auth route
}

// mintCliAuthCode handles POST /v1/cli-auth/code. Returns 200 with
// {code, url, expires_at} on success.
//
// Anti-enumeration: a probing client cannot tell whether the row
// was successfully persisted — internal errors return the same
// shape as a fresh code. The only 4xx is 429 from the cliAuthLimiter
// (cliAuthChain, server.go).
func (h *cliAuthHandlers) mintCliAuthCode(w http.ResponseWriter, r *http.Request) {
	code, hash, err := mintCliAuthCodeRaw()
	if err != nil {
		h.log.Error("cli_auth.mint", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not mint code"))
		return
	}
	expiresAt := time.Now().Add(cliAuthCodeTTL)
	if err := h.srv.store.IssueCliAuthCode(r.Context(), hash, expiresAt); err != nil {
		h.log.Error("cli_auth.issue", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not persist code"))
		return
	}
	url := strings.TrimRight(h.urlBase, "/") + cliAuthPath + "?code=" + code
	writeJSON(w, http.StatusOK, api.CliAuthCodeResponse{
		Code:      code,
		URL:       url,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
	})
}

// exchangeCliAuthCode handles POST /v1/cli-auth/exchange. The CLI
// polls this every second until the user approves the code in the
// browser. Outcomes:
//
//	404 cli_auth_code_pending     — keep polling
//	410 cli_auth_code_unavailable — expired, already used, or unknown
//
// On approval the server mints a fresh API key (api.GenerateAPIKey)
// and writes the plaintext exactly once in the response body. The
// CLI writes the plaintext to disk and never asks again.
func (h *cliAuthHandlers) exchangeCliAuthCode(w http.ResponseWriter, r *http.Request) {
	var req api.CliAuthExchangeRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid request", "body must be {\"code\":\"ABCD-1234\"}"))
		return
	}
	hash, ok := normalizeCliAuthCode(req.Code)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid code", "code must be 8 alphanumeric characters (XXXX-NNNN)"))
		return
	}

	status, accountID, err := h.srv.store.ConsumeCliAuthCode(r.Context(), hash)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusGone, api.CodeCliAuthUnavailable,
				"Code unavailable", "code is expired, already used, or unknown"))
			return
		}
		h.log.Error("cli_auth.consume", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not consume code"))
		return
	}
	if status == api.CliAuthStatusPending {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeCliAuthPending,
			"Awaiting approval", "open the URL in your browser to continue"))
		return
	}

	// status == consumed. Mint an API key bound to accountID.
	plaintext, keyHash, err := api.GenerateAPIKey()
	if err != nil {
		h.log.Error("cli_auth.generate_key", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not generate key"))
		return
	}
	k, err := h.srv.store.CreateAPIKey(r.Context(), accountID, keyHash, "cli-login", api.ScopesAdminOnly)
	if err != nil {
		h.log.Error("cli_auth.create_key", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not persist key"))
		return
	}
	_ = h.srv.notif.Notify(r.Context(), db.NotifyKeyChanged,
		`{"kind":"created","account":"`+accountID+`","key":"`+k.ID+`"}`)
	h.log.Info("cli_auth.exchanged", "account", accountID, "key", k.ID)

	// IAM-4 (ADR-035): record the auto-minted key. subject =
	// account_id (the owner of the key), not the key id — matches
	// the existing NotifyKeyChanged payload convention.
	h.srv.audit.Emit(r.Context(), "key.created", &accountID, map[string]any{
		"key_id": k.ID,
		"scopes": api.ScopesAdminOnly,
	})
	// IAM-4 (ADR-035): the exchange itself is an auth.login
	// success from the customer's perspective. The CLI never set a
	// cookie; the key is the credential that follows.
	h.srv.audit.Emit(r.Context(), "auth.login", &accountID, map[string]any{
		"method": "cli_code",
	})

	acct, err := h.srv.store.AccountByID(r.Context(), accountID)
	if err != nil {
		h.log.Error("cli_auth.account_lookup", "err", err)
		api.WriteProblem(w, api.ErrCapacity("account lookup failed"))
		return
	}
	writeJSON(w, http.StatusOK, api.CliAuthExchangeResponse{
		Plaintext: plaintext,
		Account:   h.srv.accountResponse(r.Context(), acct, r),
	})
}

// renderCliAuthPage handles GET /cli-auth?code=…. The route is mounted
// behind sessionAuth, so only a signed-in dashboard session reaches this
// function. It renders the authorization page without accepting an email
// address from the browser; the session account is the only principal that
// can claim the code.
//
// CSRF (review finding A1): on a valid code we mint a sealed CSRF
// token bound to (action="cli-auth", subject=<account ID>) using the
// shared session.Manager and set it as the authenticated CSRF cookie.
// The same token is rendered into the form's csrf_token hidden field.
// postCliAuthPage verifies the cookie against the form value with
// the matching action + subject. A cross-site POST lacks the cookie
// and is rejected; a forged token cannot be opened without the
// 32-byte session key.
func (h *cliAuthHandlers) renderCliAuthPage(w http.ResponseWriter, r *http.Request) {
	hash, ok := normalizeCliAuthCode(r.URL.Query().Get("code"))
	if !ok {
		h.renderCliAuthError(w, r, "Code looks malformed", "Codes are 8 characters, like ABCD-1234.")
		return
	}
	status, _, err := h.srv.store.PeekCliAuthCode(r.Context(), hash)
	if err != nil || status != api.CliAuthStatusPending {
		h.renderCliAuthError(w, r, "Code unavailable", "This code is expired or already used.")
		return
	}
	acct, ok := AccountFrom(r.Context())
	if !ok || acct.ID == "" {
		h.log.Error("cli_auth.missing_session")
		h.renderCliAuthError(w, r, "Sign-in required", "Sign in to Gregale before authorizing the CLI.")
		return
	}
	// The hidden field carries the normalized (dash-less) RAW code so
	// the POST handler can hash it. normalizeCliAuthCode strips the
	// dash and uppercases; we put the hex back together.
	raw := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(r.URL.Query().Get("code")), "-", ""))

	// Mint the CSRF token + authenticated cookie bound to this account.
	token, err := middleware.IssueForAuthenticated(h.srv.sessions, "cli-auth", acct.ID)
	if err != nil {
		h.log.Error("cli_auth.csrf_issue", "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.CookieNameAuthenticated,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.domain != "",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(middleware.DefaultCSRFTTL.Seconds()),
	})

	page := dashboard.Page{
		Title:   "Authorize CLI session",
		Body:    "cli_auth",
		Account: dashboardAccountView(acct, 0),
		Data: map[string]any{
			"Code":         raw,
			"CSRFToken":    token,
			"AccountEmail": acct.Email,
		},
	}
	if err := dashboard.Render(w, h.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		h.log.Error("cli_auth.render", "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// postCliAuthPage handles POST /cli-auth (form submit from the
// dashboard). The route is mounted behind sessionAuth, so the code is
// always claimed for the already-authenticated session account. No email
// address is accepted from the form and this path never creates accounts.
// New users must complete the normal signup flow before authorizing a CLI.
// After the atomic claim it fires NotifyCliAuthCodeActivated and redirects
// to /dashboard/account.
//
// CSRF (review finding A1): verify the authenticated CSRF cookie + form
// token are bound to (action="cli-auth", subject=<account ID>). A forged
// token or a token rendered for another account cannot authorize the code.
func (h *cliAuthHandlers) postCliAuthPage(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok || acct.ID == "" {
		h.log.Error("cli_auth.missing_session")
		h.renderCliAuthError(w, r, "Sign-in required", "Sign in to Gregale before authorizing the CLI.")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	// Resolve the code first so we can pass it as the CSRF subject.
	rawCode := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(r.FormValue("code")), "-", ""))
	if rawCode == "" {
		h.renderCliAuthError(w, r, "Missing code", "The CLI authorization code is required.")
		return
	}
	hash, ok := normalizeCliAuthCode(rawCode)
	if !ok {
		h.renderCliAuthError(w, r, "Invalid code", "The CLI authorization code is not valid.")
		return
	}
	// CSRF guard (review finding A1). Cookie + form value must be a
	// sealed envelope with action=cli-auth and subject=acct.ID. A
	// cross-site POST lacks the cookie and is rejected here before
	// the code claim.
	if err := middleware.VerifyAuthenticated(h.srv.sessions, r, "cli-auth", acct.ID); err != nil {
		h.renderCliAuthError(w, r, "Invalid form", "Please reload the page and try again.")
		return
	}

	// Atomically claim the code for this account. ClaimCliAuthCode
	// transitions pending → consumed + account_id in one SQL.
	if err := h.srv.store.ClaimCliAuthCode(r.Context(), hash, acct.ID); err != nil {
		if errors.Is(err, state.ErrConflict) {
			h.renderCliAuthError(w, r, "Code already used", "Restart 'faas login' to get a new code.")
			return
		}
		if errors.Is(err, state.ErrNotFound) {
			h.renderCliAuthError(w, r, "Code expired", "Restart 'faas login' to get a new code.")
			return
		}
		h.log.Error("cli_auth.claim", "err", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	_ = h.srv.notif.Notify(r.Context(), db.NotifyCliAuthCodeActivated,
		`{"hash":"`+hex.EncodeToString(hash)+`"}`)
	http.Redirect(w, r, cliAuthDashboard, http.StatusFound)
}

// renderCliAuthError renders the cli_auth template with the error
// flag set, so the form is replaced with a banner.
func (h *cliAuthHandlers) renderCliAuthError(w http.ResponseWriter, r *http.Request, title, detail string) {
	page := dashboard.Page{
		Title: title,
		Body:  "cli_auth",
		Flash: detail,
		Data:  map[string]any{"Error": true},
	}
	if err := dashboard.Render(w, h.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		h.log.Error("cli_auth.render_error", "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// mintCliAuthCodeRaw returns (human-readable "XXXX-NNNN", sha256 hash).
// 4 bytes of crypto/rand → 8 hex chars → formatted with a dash after
// the first 4 chars. 32 bits of entropy is fine: the consume path is
// rate-limited to 10/min/IP (cliAuthChain) and the TTL is 5 min, so
// brute-force on the code space is not realistic.
func mintCliAuthCodeRaw() (string, []byte, error) {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	hexStr := hex.EncodeToString(raw) // 8 hex chars
	return hexStr[:4] + "-" + hexStr[4:], api.HashToken(raw), nil
}

// normalizeCliAuthCode strips whitespace + dashes, uppercases, and
// returns the 4-byte hash if the result is a valid 8-hex-char code.
// Returns ok=false on any shape mismatch.
func normalizeCliAuthCode(raw string) ([]byte, bool) {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(raw), "-", ""))
	if len(normalized) != 8 {
		return nil, false
	}
	decoded, err := hex.DecodeString(normalized)
	if err != nil {
		return nil, false
	}
	return api.HashToken(decoded), true
}
