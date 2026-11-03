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
package apidserver

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agentconnections"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/logsanitize"
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
	// Browser-authorized CLI sessions are replaceable credentials. A finite
	// lifetime bounds exposure for a lost device even when the user never
	// returns to run `gregale logout`.
	cliAuthKeyTTL = 30 * 24 * time.Hour
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
	_, deviceCodes := h.srv.store.(state.CliAuthDeviceCodeStore)
	code, hash, device, err := mintBrowserAuthCode(r, deviceCodes)
	if errors.Is(err, errBrowserAuthInput) {
		api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid authorization", "purpose must be empty, mcp-read, or mcp-write"))
		return
	}
	if err != nil {
		h.log.Error("cli_auth.mint", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not mint code"))
		return
	}
	expiresAt := time.Now().Add(cliAuthCodeTTL)
	deviceCode, err := h.issueCliAuthCode(r, hash, device, expiresAt)
	if err != nil {
		h.log.Error("cli_auth.issue", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not persist code"))
		return
	}
	url := strings.TrimRight(h.urlBase, "/") + cliAuthPath + "?code=" + code
	writeJSON(w, http.StatusOK, api.CliAuthCodeResponse{
		Code:       code,
		URL:        url,
		ExpiresAt:  expiresAt.UTC().Format(time.RFC3339),
		DeviceCode: deviceCode,
	})
}

// issueCliAuthCode persists a minted code. With device, it also mints a
// 32-byte device code, stores only its hash, and returns it: the row can
// then be exchanged only with that device code, never with the short code
// the user types (bug hunt 2026-10-28 H67). Without device, the code was
// minted long enough to exchange by itself (mintBrowserAuthCode).
func (h *cliAuthHandlers) issueCliAuthCode(r *http.Request, hash []byte, device bool, expiresAt time.Time) (string, error) {
	if !device {
		return "", h.srv.store.IssueCliAuthCode(r.Context(), hash, expiresAt)
	}
	devices, ok := h.srv.store.(state.CliAuthDeviceCodeStore)
	if !ok {
		return "", errors.New("store cannot bind device codes")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	if err := devices.IssueCliAuthDeviceCode(r.Context(), hash, api.HashToken(raw), expiresAt); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// consumeCliAuthExchange resolves an exchange body to the code's state: by
// device code when one is given, else by the short code (legacy CLIs).
// valid is false for a malformed body.
func (h *cliAuthHandlers) consumeCliAuthExchange(r *http.Request, req api.CliAuthExchangeRequest) (status api.CliAuthStatus, accountID string, valid bool, err error) {
	if req.DeviceCode != "" {
		devices, ok := h.srv.store.(state.CliAuthDeviceCodeStore)
		raw := decodeCliDeviceCode(req.DeviceCode)
		if raw == nil || !ok {
			return "", "", false, nil
		}
		status, accountID, err = devices.ConsumeCliAuthDeviceCode(r.Context(), api.HashToken(raw))
		return status, accountID, true, err
	}
	hash, ok := normalizeCliAuthCode(req.Code)
	if !ok {
		return "", "", false, nil
	}
	// A short code is minted only bound to a device code, so it never
	// exchanges alone: answer as for an unknown code without a lookup.
	if isShortCliAuthCode(req.Code) {
		return api.CliAuthStatusExpired, "", true, state.ErrNotFound
	}
	status, accountID, err = h.srv.store.ConsumeCliAuthCode(r.Context(), hash)
	return status, accountID, true, err
}

// decodeCliDeviceCode decodes a hex device code, or returns nil for a
// malformed one.
func decodeCliDeviceCode(deviceCode string) []byte {
	raw, err := hex.DecodeString(strings.TrimSpace(deviceCode))
	if err != nil || len(raw) != 32 {
		return nil
	}
	return raw
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
	status, accountID, ok, err := h.consumeCliAuthExchange(r, req)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid code", "code must be 16 hex characters (XXXX-XXXX-XXXX-XXXX), or device_code 64 hex characters"))
		return
	}
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
	label, scopes, ttl := browserAuthCredential(req.Code)
	expiresAt := time.Now().UTC().Add(ttl)
	k, scopes, p := h.createBrowserAuthKey(r, req, accountID, keyHash, label, scopes, expiresAt)
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	h.srv.recordKeyDisplayPrefix(r.Context(), k.ID, plaintext)
	_ = h.srv.notif.Notify(r.Context(), db.NotifyKeyChanged,
		`{"kind":"created","account":"`+accountID+`","key":"`+k.ID+`"}`)
	h.log.Info("cli_auth.exchanged", "account", accountID, "key", k.ID)

	// IAM-4 (ADR-035): record the auto-minted key. subject =
	// account_id (the owner of the key), not the key id — matches
	// the existing NotifyKeyChanged payload convention.
	h.srv.audit.Emit(r.Context(), "key.created", &accountID, map[string]any{
		"key_id": k.ID,
		"scopes": scopes,
	})
	// IAM-4 (ADR-035): the exchange itself is an auth.login
	// success from the customer's perspective. The CLI never set a
	// cookie; the key is the credential that follows.
	h.srv.audit.Emit(r.Context(), "auth.login", &accountID, map[string]any{
		"method": browserAuthAuditMethod(req.Code),
	})

	acct, err := h.srv.store.AccountByID(r.Context(), accountID)
	if err != nil {
		h.log.Error("cli_auth.account_lookup", "err", err)
		api.WriteProblem(w, api.ErrCapacity("account lookup failed"))
		return
	}
	writeJSON(w, http.StatusOK, api.CliAuthExchangeResponse{
		Plaintext: plaintext,
		KeyID:     k.ID,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		Account:   h.srv.accountResponse(r.Context(), acct, r),
	})
}

// createBrowserAuthKey mints the exchanged key. A hosted MCP sign-in bound to
// a shared organization at approval gets that organization's key, holding
// only the scopes the organization's agent policy allows
// (ADR-20261101225122).
func (h *cliAuthHandlers) createBrowserAuthKey(r *http.Request, req api.CliAuthExchangeRequest, accountID string, keyHash []byte, label string, scopes []string, expiresAt time.Time) (state.APIKey, []string, *api.Problem) {
	ip, ua := clientIPFromRequest(r), logsanitize.Field(r.UserAgent())
	orgID, err := h.browserAuthOrg(r, req)
	if err != nil {
		h.log.Error("cli_auth.code_org", "err", err)
		return state.APIKey{}, nil, api.ErrCapacity("could not read the sign-in's organization")
	}
	if orgID == "" {
		k, err := h.srv.store.CreateAPIKeyWithExpiryAndProvenance(r.Context(), accountID, keyHash, label, scopes, &expiresAt, ip, ua, nil)
		if err != nil {
			h.log.Error("cli_auth.create_key", "err", err)
			return state.APIKey{}, nil, api.ErrCapacity("could not persist key")
		}
		return k, scopes, nil
	}
	policy, err := agentconnections.PolicyFor(r.Context(), h.srv.store, orgID, accountID)
	if errors.Is(err, agentconnections.ErrNotAgentMember) || errors.Is(err, agentconnections.ErrAgentsNotAllowed) {
		return state.APIKey{}, nil, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Agents can't act in this organization", err.Error()+"; connect the agent again and choose another organization")
	}
	acct, aerr := h.srv.store.AccountByID(r.Context(), accountID)
	if err != nil || aerr != nil {
		h.log.Error("cli_auth.org_policy", "err", errors.Join(err, aerr))
		return state.APIKey{}, nil, api.ErrCapacity("could not check the organization's agent policy")
	}
	scopes = agentconnections.CapScopes(policy, scopes)
	activity := newOrgAPIKeyActivity(r, acct, orgID, "api_key.created", map[string]any{"scopes": scopes, "reason": "mcp_sign_in"})
	k, err := h.srv.createOrgAPIKeyWithActivity(r.Context(), acct, orgID, keyHash, label, scopes, &expiresAt, ip, ua, nil, activity)
	if err != nil {
		h.log.Error("cli_auth.create_org_key", "err", err)
		return state.APIKey{}, nil, api.ErrCapacity("could not persist key")
	}
	return k, scopes, nil
}

// browserAuthOrg is the shared organization a hosted MCP sign-in's code was
// bound to at approval, or "" for the personal one.
func (h *cliAuthHandlers) browserAuthOrg(r *http.Request, req api.CliAuthExchangeRequest) (string, error) {
	store, ok := h.srv.store.(state.OrgAgentStore)
	hash, valid := normalizeCliAuthCode(req.Code)
	if !ok || !valid || !isMCPAuthCode(req.Code) {
		return "", nil
	}
	orgID, err := store.CliAuthCodeOrg(r.Context(), hash)
	if errors.Is(err, state.ErrNotFound) {
		return "", nil
	}
	return orgID, err
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
	if r.URL.Query().Has("approval") {
		h.renderPromotionApprovalPage(w, r)
		return
	}
	if r.URL.Query().Has("secret") {
		h.renderSecretEntryPage(w, r)
		return
	}
	hash, ok := normalizeCliAuthCode(r.URL.Query().Get("code"))
	if !ok {
		h.renderCliAuthError(w, r, "Code looks malformed", "Use the code your terminal shows, like ABCD-1234.")
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
		renderProblem(w, h.log, err)
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

	title := "Authorize CLI session"
	// A CLI code is typed from the terminal, never carried from the link
	// into the form, so a link someone sent cannot authorize their
	// terminal. MCP codes arrive through the bridge's own OAuth redirect.
	shownCode := ""
	if isMCPAuthCode(raw) {
		title = "Authorize MCP connection"
		shownCode = raw
	}
	page := dashboard.Page{
		Title:   title,
		Body:    "cli_auth",
		Account: dashboardAccountView(acct, 0),
		Data: map[string]any{
			"Code":            shownCode,
			"CSRFToken":       token,
			"AccountEmail":    acct.Email,
			"MCP":             isMCPAuthCode(raw),
			"MCPCapabilities": mcpCapabilities(raw),
			"MCPOrgs":         h.mcpOrgChoices(r, acct.ID, raw),
			// The hosted bridge asks to have the browser back (ADR-20261028205920).
			"ReturnMCP": isMCPAuthCode(raw) && r.URL.Query().Get("return") == "mcp" && h.srv.hostedMCPContinueURL() != "",
		},
	}
	if err := dashboard.Render(w, h.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		h.log.Error("cli_auth.render", "err", err)
		renderProblem(w, h.log, err)
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
		writeDashboardBadRequest(w, r, "The CLI authorization form could not be read.")
		return
	}
	if r.PostForm.Has("approval") {
		h.postPromotionApprovalPage(w, r)
		return
	}
	if r.PostForm.Has("secret") {
		h.postSecretEntryPage(w, r)
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

	if !h.bindMCPOrg(w, r, acct.ID, rawCode, hash) {
		return
	}
	// Atomically claim the code for this account. ClaimCliAuthCode
	// transitions pending → consumed + account_id in one SQL.
	if err := h.srv.store.ClaimCliAuthCode(r.Context(), hash, acct.ID); err != nil {
		if errors.Is(err, state.ErrConflict) {
			h.renderCliAuthError(w, r, "Code already used", "Restart 'gregale login' to get a new code.")
			return
		}
		if errors.Is(err, state.ErrNotFound) {
			h.renderCliAuthError(w, r, "Code expired", "Restart 'gregale login' to get a new code.")
			return
		}
		h.log.Error("cli_auth.claim", "err", err)
		api.WriteProblemForRequest(w, r, api.ErrInternal(
			"Gregale could not authorize this CLI session.",
		).WithHint("Restart `gregale login` and try the new code."))
		return
	}
	_ = h.srv.notif.Notify(r.Context(), db.NotifyCliAuthCodeActivated,
		`{"hash":"`+hex.EncodeToString(hash)+`"}`)
	// An approved hosted MCP code continues on the platform's own MCP
	// server, in this tab (ADR-20261028205920). The target never comes from
	// the request, so the form field cannot redirect anywhere else.
	if continueURL := h.srv.hostedMCPContinueURL(); continueURL != "" && isMCPAuthCode(rawCode) && r.PostForm.Get("return") == "mcp" {
		http.Redirect(w, r, continueURL, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, cliAuthDashboard, http.StatusFound)
}

// hostedMCPContinueURL is where the hosted MCP server continues a connection
// after its code is approved, or "" when no platform domain is configured.
func (s *server) hostedMCPContinueURL() string {
	domain := strings.TrimPrefix(strings.TrimSpace(s.platformHost()), ".")
	if domain == "" {
		return ""
	}
	return "https://api." + domain + "/mcp/oauth/continue"
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
		renderProblem(w, h.log, err)
	}
}

// mintCliAuthCodeRaw returns (human-readable "XXXX-NNNN", sha256 hash) for a
// login code bound to a device code. 4 bytes of crypto/rand → 8 hex chars →
// a dash after the first 4. 32 bits suits a code a person types because the
// short code never exchanges alone: only the 32-byte device code does (H67,
// isShortCliAuthCode).
func mintCliAuthCodeRaw() (string, []byte, error) {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	hexStr := hex.EncodeToString(raw) // 8 hex chars
	return hexStr[:4] + "-" + hexStr[4:], api.HashToken(raw), nil
}

// mintCodeOnlyCliAuthCode returns ("XXXX-XXXX-XXXX-XXXX", sha256 hash) for a
// login code with no device code, which the CLI exchanges by the code alone
// (CLIs before v0.1.18). 64 bits keeps guessing at the exchange worthless
// even from many addresses, where 32 bits behind a per-IP limit was not.
func mintCodeOnlyCliAuthCode() (string, []byte, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	hexStr := hex.EncodeToString(raw) // 16 hex chars
	return hexStr[:4] + "-" + hexStr[4:8] + "-" + hexStr[8:12] + "-" + hexStr[12:], api.HashToken(raw), nil
}

// isShortCliAuthCode reports whether raw is a device-bound 8-character login
// code (mintCliAuthCodeRaw).
func isShortCliAuthCode(raw string) bool {
	return len(strings.ReplaceAll(strings.TrimSpace(raw), "-", "")) == 8
}

// normalizeCliAuthCode strips whitespace + dashes, uppercases, and
// returns the code's hash if the result is an MCP code or an 8- or
// 16-hex-char login code. Returns ok=false on any shape mismatch.
func normalizeCliAuthCode(raw string) ([]byte, bool) {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(raw), "-", ""))
	if isMCPAuthCode(normalized) {
		decoded, err := hex.DecodeString(normalized[4:])
		if err != nil || len(decoded) != 32 {
			return nil, false
		}
		return api.HashToken(append([]byte(normalized[:4]+":"), decoded...)), true
	}
	if len(normalized) != 8 && len(normalized) != 16 {
		return nil, false
	}
	decoded, err := hex.DecodeString(normalized)
	if err != nil {
		return nil, false
	}
	return api.HashToken(decoded), true
}
