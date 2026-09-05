// Package api is the one-box FaaS platform's wire contract. It holds:
//   - DTOs for every v1 REST request/response (this file + dto.go)
//   - RFC 7807 Problem envelope and error constructors (errors.go)
//   - The typed Go SDK clients use against apid (Client below)
//
// Client is the public SDK surface. New customers should:
//
//	c := api.NewClient("https://api.example.com", os.Getenv("FAAS_TOKEN"))
//	app, err := c.GetApp(ctx, "hello-world")
//	apps, err := c.ListApps(ctx)
//
// All methods are safe for concurrent use; the underlying HTTP
// transport is shared and the only mutable state is via the per-call
// context. Conventions:
//
//   - Auth — every method sends Authorization: Bearer <token> when the
//     Client was constructed with a non-empty token. Tokenless clients
//     are useful for the anonymous device-code flow only (MintCliAuthCode,
//     ExchangeCliAuthCode).
//
//   - Idempotency — non-GET/HEAD calls auto-mint an Idempotency-Key
//     header (UUIDv4) on the way out when the caller didn't supply one.
//     The server's replay middleware (apid/server.go::idempotent) keeps
//     responses for 24h; SDK callers who want deterministic retry
//     semantics should pass their own key. DeleteAccount accepts an
//     explicit key argument for this reason.
//
//   - Errors — every 4xx/5xx with a Problem-shaped body returns an
//     *APIError wrapping the canonical Problem. Bodies that fail JSON
//     decoding fall through to errors.New("API error: <status>") so
//     non-problem responses (e.g. the authlimiter's plain-text 429)
//     still surface.
//
//   - Timeouts — the default HTTP client has a 30s timeout. SSE
//     streams and tarball uploads use dedicated transports; see
//     NewClientWithDeployTimeout and the *SSE methods (added in
//     commit 2).
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// cookieOnlyPathRE matches routes that are gated server-side to the
// dashboard session cookie. The bearer-key CLI cannot reach
// them — a request would 401 (or 302 to the login page) because the
// session-cookie middleware (cmd/apid/server.go:1097 and
// cmd/apid/handlers_sessions.go:71-77) treats bearer-key callers
// as anonymous. The guard in c.do (below) short-circuits the
// request with CodeUnsupportedByCLI so the failure mode is honest
// ("the CLI cannot reach this route") rather than a confusing
// 401/302. The companion tripwire
// (pkg/api/lint_tripwires_test.go) ensures no other pkg/api file
// composes a path that matches this regex.
var cookieOnlyPathRE = regexp.MustCompile(`^(/v1/auth/(sessions|capabilities)(/.*)?|/dashboard/account/set-password)$`)

// Client is a typed wrapper over the v1 REST API. Construct with
// NewClient (30s default timeout) or NewClientWithDeployTimeout
// (longer upload timeout). Pass-through to net/http for SSE streams is
// configured internally; see logs.go.
//
// Path and query parameters are passed verbatim to net/http. The
// OpenAPI spec constrains every path param to a regex that excludes
// URL-unsafe characters (slug = ^[a-z0-9-]+$, id = ^[a-f0-9]{32}$,
// key = ^[A-Z][A-Z0-9_]*$, domain = ^[a-z0-9.\-]+$); apid validates
// input with these patterns, so malformed input surfaces as a 4xx
// Problem rather than a URL-mangled 404. SDK callers that compose
// slugs from user input should validate against the spec pattern
// before calling.
type Client struct {
	baseURL string
	token   string

	http       *http.Client // 30s default — used for every JSON call
	deployHTTP *http.Client // optional, used by DeployMultipart

	// cache powers `gregale completion <shell>` for the per-account
	// positional completion paths (e.g. <slug> in `gregale app <slug>`).
	// Nil → the c.do middleware short-circuits the refresh (preserves
	// the test-suite posture where no on-disk cache should leak between
	// subtests). NewClient wires a fresh cache so completion just works;
	// tests that want hermetic isolation call SetCompletionCache(nil).
	cache *CompletionCache
}

// NewClient builds a client for baseURL with the given bearer token.
// An empty token disables Authorization (useful for the anonymous
// device-code endpoints).
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second, Transport: newClientTransport()},
		cache:   NewCompletionCache(),
	}
}

// newClientTransport returns a private copy of the standard transport. The
// default HTTP transport is process-global; httptest.Server.Close calls
// CloseIdleConnections on it, so sharing it makes independent clients race
// when tests (or applications) close test servers while another request is
// in flight. Clone preserves the standard proxy, dial, TLS, and idle-pool
// settings without coupling this client to that global lifecycle.
func newClientTransport() http.RoundTripper {
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		return transport.Clone()
	}
	return http.DefaultTransport
}

// SetCompletionCache wires a (possibly nil) cache. Passing nil
// disables the auto-refresh — useful for tests that don't want
// disk writes leaking between cases. Returns the receiver so the
// call site can chain or discard.
func (c *Client) SetCompletionCache(cache *CompletionCache) *Client {
	c.cache = cache
	return c
}

// CompletionCache returns the current cache. May be nil if the
// client was built before NewCompletionCache existed (binary
// compat — older callers that constructed Client{} directly) or
// after an explicit SetCompletionCache(nil). Completion scripts
// that want to read the cache for TAB-time lookups should call
// this rather than constructing their own cache, so the file
// path stays consistent with whatever the middleware wrote.
func (c *Client) CompletionCache() *CompletionCache {
	return c.cache
}

// NewClientWithDeployTimeout is like NewClient but configures a
// longer upload HTTP client. A non-positive duration falls back to
// the 30s default. Used by SDK consumers uploading multi-MB tarballs
// where the 30s default would otherwise trip.
func NewClientWithDeployTimeout(baseURL, token string, deployTimeout time.Duration) *Client {
	c := NewClient(baseURL, token)
	if deployTimeout > 0 {
		c.deployHTTP = &http.Client{Timeout: deployTimeout, Transport: newClientTransport()}
	}
	return c
}

// HTTPClient returns the underlying JSON HTTP client. Exposed so SDK
// callers can swap transport-level knobs (TLS, retries) without
// depending on a private field.
func (c *Client) HTTPClient() *http.Client { return c.http }

// BaseURL returns the URL prefix the client was constructed with.
func (c *Client) BaseURL() string { return c.baseURL }

// Token returns the bearer token the client was constructed with
// (empty for anonymous clients). The returned value is the raw
// secret; do NOT log it, surface it in errors, or persist it. SDK
// callers that need to forward the token to other surfaces should
// copy it into a local variable scoped to the request.
func (c *Client) Token() string { return c.token }

// uploadHTTP returns the upload client or falls back to the default.
func (c *Client) uploadHTTP() *http.Client {
	if c.deployHTTP != nil {
		return c.deployHTTP
	}
	return c.http
}

// do executes an HTTP request against c.baseURL+path with the SDK's
// standard auth + idempotency conventions. It marshals body as JSON
// when body != nil, decodes non-2xx as Problem, and unmarshals a
// successful response into out when out != nil.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	return c.doWithIdempotencyKey(ctx, method, path, body, out, "")
}

// doWithIdempotencyKey is the same request path as do, with an optional
// caller-supplied key. Safe-release actions use a rollout-scoped key so two
// alert rules cannot repeat the same mutation after a meterd race.
func (c *Client) doWithIdempotencyKey(ctx context.Context, method, path string, body, out any, idempotencyKey string) error {
	// Cookie-only-route guard — reject paths the bearer-key CLI cannot
	// reach before allocating anything. The regex matches the closed
	// set /v1/auth/sessions and /v1/auth/capabilities (with optional
	// trailing subpath), plus the dashboard set-password form. The
	// status is 403 because the call is
	// well-formed but the caller's auth mode does not match the
	// route's policy — semantically a peer of CodeDeploySignatureInvalid
	// (403 for "this caller cannot complete this action"). Docs URL
	// points at the (forthcoming) /cli/cookie-only-routes page; the
	// tripwire enforces the same URL in pkg/api/lint_tripwires_test.go.
	if cookieOnlyPathRE.MatchString(path) {
		return NewProblem(
			http.StatusForbidden,
			CodeUnsupportedByCLI,
			"endpoint requires the dashboard session cookie",
			"the gregale CLI cannot reach this route — use the dashboard at "+docsBase+"/dashboard/sessions",
		).WithDocs(docsBase + "/cli/cookie-only-routes")
	}
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, r)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	// UX §3.2 / impl §4.2: every mutating call carries Idempotency-Key
	// so a retried deploy/park/wake/rollback/etc. never double-charges
	// or double-creates. We never override an explicit key the caller
	// already set.
	if method != http.MethodGet && method != http.MethodHead {
		if idempotencyKey == "" {
			idempotencyKey = newUUIDv4()
		}
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.doReq(c.http, req, out)
}

// doReq executes a prepared request against the given *http.Client
// (default c.http or uploadHTTP for tarball uploads) and applies the
// SDK's standard response handling: 4 MiB body cap, non-2xx → Problem,
// 2xx → unmarshal into out when out != nil. The caller is responsible
// for auth + Idempotency-Key + Content-Type — see do for the standard
// recipe; methods that need a custom header set it on req before
// calling doReq.
func (c *Client) doReq(cli *http.Client, req *http.Request, out any) error {
	return c.doReqWithSuccess(cli, req, out, func(resp *http.Response) bool {
		return resp.StatusCode >= 200 && resp.StatusCode < 300
	})
}

// doReqWithSuccess is doReq with a caller-supplied success predicate.
// Dashboard form mutations use a 302 redirect as their successful
// response, while the generic SDK path treats all 3xx responses as
// errors. Keeping the predicate local prevents that dashboard
// convention from changing response handling for every other method.
func (c *Client) doReqWithSuccess(cli *http.Client, req *http.Request, out any, success func(*http.Response) bool) error {
	// ADR-093 / PR-E: outbound SDK call becomes a child of the
	// inbound budget when one is attached. The CLI / SDK never
	// receives a Budget from the user today — but apid's
	// gatewayd-internal handlers do call into the SDK to issue
	// outbound HTTP (issue #739 / ADR-092 source-ref refresh), so
	// the SDK honours any budget that's on the inbound ctx.
	// childDeadline = min(parentRemaining, cli.Timeout) — the
	// client's Timeout stays as the absolute ceiling; the budget
	// can only tighten it. cli.Timeout == 0 is treated as "no
	// ceiling configured" by WithCeiling (it inherits the
	// parent's remaining time) so a default-constructed
	// http.Client{} doesn't immediately expire; cli.Do then
	// applies no Timeout of its own — the SDK no longer enforces
	// a wall-clock cap on those calls, same as pre-PR-E. When
	// no Budget is on the inbound ctx, cli.Timeout alone bounds
	// the request (the legacy contract).
	if b, ok := reqbudget.FromContext(req.Context()); ok {
		newCtx, cancel, _ := b.WithCeiling(req.Context(), cli.Timeout)
		defer cancel()
		req = req.Clone(newCtx)
	}
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if !success(resp) {
		var p Problem
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			// Copy RFC 7231 §7.1.3 wire headers the server attaches
			// for transient / retryable errors (Retry-After on 503
			// source_ref_unavailable, 429 plan_limit_concurrency,
			// etc.) so callers can branch on Problem.HasHeader
			// without re-reading resp.Header themselves. The SDK
			// already discards the raw http.Response (see do), so
			// this is the load-bearing surface for the backoff
			// hint. Issue #739 / ADR-092: the headless source-ref
			// CLI path relies on this for the 409 backoff message.
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				p = *p.WithHeader("Retry-After", ra)
			}
			return &APIError{Problem: p}
		}
		return fmt.Errorf("API error: %s", resp.Status)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Tier A8 / ADR-083: auto-refresh the completion cache on every
		// 2xx. Runs before the unmarshal so the raw body is still in hand;
		// errors are swallowed inside MaybeRefresh so a broken cache
		// (disk full, permission denied, corrupt JSON) never fails a
		// request. Nil-safe for tests that opt out via SetCompletionCache(nil).
		if c.cache != nil {
			c.cache.MaybeRefresh(req.URL.Path, data)
		}
		if out != nil && len(data) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
		}
	}
	return nil
}

// doBytes executes an HTTP request and returns the raw response body
// verbatim (issue #299, used by GetBuildsIdSbom for the CycloneDX
// JSON document the server streams back). Mirrors do for auth +
// idempotency conventions but skips the JSON unmarshal — the
// caller passes a *[]byte and receives the body untouched. Returns
// (nil, *APIError) on a non-2xx, same shape as do.
func (c *Client) doBytes(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, r)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if method != http.MethodGet && method != http.MethodHead && req.Header.Get("Idempotency-Key") == "" {
		req.Header.Set("Idempotency-Key", newUUIDv4())
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var p Problem
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			// Mirror doReq: copy the Retry-After wire header into
			// the Problem so the 409 / 429 / 503 backoff hint is
			// reachable via Problem.HasHeader after the SDK
			// discards the raw http.Response.
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				p = *p.WithHeader("Retry-After", ra)
			}
			return &APIError{Problem: p}
		}
		return fmt.Errorf("API error: %s", resp.Status)
	}
	if out != nil {
		if bp, ok := out.(*[]byte); ok {
			*bp = data
			return nil
		}
		// Fall through: caller wants JSON-decoded, do the same
		// unmarshal as doReq. Untyped callers will get a decode
		// error if they pass anything other than *[]byte.
		if len(data) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
		}
	}
	return nil
}

// ErrNoBody is returned by helpers that expected a body but got none.
// Errors.Is/As users can match it directly; it's also wrapped inside
// *APIError.Problem paths so callers don't need to import errors.
var ErrNoBody = errors.New("api: response body was empty")

// Whoami returns the authenticated account.
func (c *Client) Whoami(ctx context.Context) (AccountResponse, error) {
	var out AccountResponse
	return out, c.do(ctx, "GET", "/v1/account", nil, &out)
}

// ExportAccount downloads the GDPR export bundle (spec §17 G6) into
// the provided writer. includeSecrets=false drops the ciphertext
// slice. The streamed body is decoded as a single JSON document for
// the SDK caller to inspect, so memory usage scales with bundle size.
func (c *Client) ExportAccount(ctx context.Context, includeSecrets bool) (AccountExportResponse, error) {
	path := "/v1/account/export"
	if !includeSecrets {
		path += "?include_secrets=false"
	}
	var out AccountExportResponse
	return out, c.do(ctx, "GET", path, nil, &out)
}

// DeleteAccount schedules the account for deletion. The server is
// idempotent under Idempotency-Key; callers may pass an explicit
// stable key (CI retries) or "" to auto-mint a UUIDv4 per call.
func (c *Client) DeleteAccount(ctx context.Context, idempotencyKey string) (AccountDeletionResponse, error) {
	if idempotencyKey == "" {
		idempotencyKey = newUUIDv4()
	}
	req, err := http.NewRequestWithContext(ctx, "DELETE", c.baseURL+"/v1/account", nil)
	if err != nil {
		return AccountDeletionResponse{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Idempotency-Key", idempotencyKey)
	var out AccountDeletionResponse
	return out, c.doReq(c.http, req, &out)
}

// RestoreAccount cancels a pending deletion (spec §17 G6).
func (c *Client) RestoreAccount(ctx context.Context) (AccountResponse, error) {
	var out AccountResponse
	return out, c.do(ctx, "POST", "/v1/account/restore", nil, &out)
}

// MFA (IAM-2, issue #186) — TOTP second-factor on the dashboard.
// The server returns the otpauth URL + scratch codes ONCE on
// EnrollMFA; the customer must complete ConfirmMFA before the server
// stamps mfa_enrolled_at. VerifyMFA is the step-up route for an
// already-enrolled customer whose session cookie is mfa_pending.
// RecoverMFA burns a recovery code; DisableMFA clears MFA state
// (re-auth via password or recovery_code). All five require the
// session cookie — API keys bypass MFA per the IAM-2 decision.

// EnrollMFA starts enrollment. The plaintext TOTP secret + QR +
// 10 recovery codes are returned exactly once. The caller is
// responsible for rendering the QR + showing the codes to the
// customer — the server-side blob is sealed at rest under the
// host age key, and subsequent /enroll calls overwrite the
// secret without re-surfacing the plaintexts.
func (c *Client) PostAccountMfaEnroll(ctx context.Context) (MFAEnrollResponse, error) {
	var out MFAEnrollResponse
	return out, c.do(ctx, "POST", "/v1/account/mfa/enroll", MFAEnrollRequest{}, &out)
}

// ConfirmMFA finishes enrollment with the customer's first 6-digit
// TOTP code. On success the server stamps mfa_enrolled_at, clears
// mfa_required, and re-issues the session cookie without
// mfa_pending. Idempotent on retry.
func (c *Client) PostAccountMfaConfirm(ctx context.Context, req MFAConfirmRequest) (MFAConfirmResponse, error) {
	var out MFAConfirmResponse
	return out, c.do(ctx, "POST", "/v1/account/mfa/confirm", req, &out)
}

// VerifyMFA steps up an mfa_pending session for an already-enrolled
// customer. Does NOT re-stamp mfa_enrolled_at — only re-issues the
// session cookie without mfa_pending.
func (c *Client) PostAccountMfaVerify(ctx context.Context, req MFAVerifyRequest) (MFAVerifyResponse, error) {
	var out MFAVerifyResponse
	return out, c.do(ctx, "POST", "/v1/account/mfa/verify", req, &out)
}

// RecoverMFA burns a recovery code to regain access when the
// customer's TOTP device is lost. The matching hash is removed
// from the stored set; subsequent calls with the same code return
// 401. If the burn would consume the last code, the handler refuses
// and the caller should fall back to DisableMFA via password.
func (c *Client) PostAccountMfaRecover(ctx context.Context, req MFARecoverRequest) (MFARecoverResponse, error) {
	var out MFARecoverResponse
	return out, c.do(ctx, "POST", "/v1/account/mfa/recover", req, &out)
}

// DisableMFA opts out of MFA. The request body must include exactly
// one of Password or RecoveryCode — both empty and both set return
// 400 CodeValidation. On success the server clears
// mfa_secret_encrypted + mfa_recovery_codes_hash + mfa_enrolled_at;
// mfa_required is left as-is so the plan-upgrade / 2nd-deploy
// chokepoints can re-arm on the next trigger.
func (c *Client) PostAccountMfaDisable(ctx context.Context, req MFADisableRequest) (MFADisableResponse, error) {
	var out MFADisableResponse
	return out, c.do(ctx, "POST", "/v1/account/mfa/disable", req, &out)
}

// IAM-3 server-side session revocation (ADR-039, issue #187 + #244
// merged). The dashboard's "Active sessions" panel is driven by
// these four endpoints. All four require the session cookie —
// API keys bypass session tracking per the IAM-3 design decision
// (bearer keys never create or query the sessions table).
//
// Each method ignores 204 No Content; the SDK surfaces either a
// structured response (for List + RevokeAll) or just the error
// (for Logout + RevokeSession). The CLI's `faas logout` and
// `faas sessions` subcommands wrap these.

func (c *Client) PostAccountLogout(ctx context.Context) error {
	return c.do(ctx, "POST", "/v1/auth/logout", struct{}{}, nil)
}

// Session + auth-capabilities routes are session-cookie-only endpoints
// (server.go:1085 mounts /v1/auth/capabilities behind sessionAuth; the
// three /v1/auth/sessions handlers at handlers_sessions.go:99/134/174
// read `sessionFrom(r)`, which pkg/auth/middleware/context.go:141
// documents as cookie-only — bearer-key calls return ok=false and the
// handlers reject with 401). The SDK therefore does not expose them;
// they remain dashboard-only until a CLI-friendly auth surface ships.

// ListApps returns the account's apps.
func (c *Client) ListApps(ctx context.Context) ([]AppResponse, error) {
	var out []AppResponse
	return out, c.do(ctx, "GET", "/v1/apps", nil, &out)
}

// CreateApp creates an app.
func (c *Client) CreateApp(ctx context.Context, req CreateAppRequest) (AppResponse, error) {
	var out AppResponse
	return out, c.do(ctx, "POST", "/v1/apps", req, &out)
}

// Deploy creates a deployment for an app slug (JSON variant).
// For tarball / dockerfile deploys use DeployMultipart.
func (c *Client) Deploy(ctx context.Context, slug string, req CreateDeploymentRequest) (DeploymentResponse, error) {
	var out DeploymentResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/deployments", req, &out)
}

// RetryDeploymentFromStage (ADR-117 §Production-ready follow-on,
// C2) inserts a fresh `deployments` row copying the failed
// deployment's input primitives and seeds the new row's
// stage_state.current to fromStage. Returns the new row's typed
// DeploymentResponse.
//
// The fromStage value MUST be one of the closed-6 stage
// vocabulary (source_download | dependency_restore | image_build
// | security_scan | snapshot_prepare | readiness). A typo or
// empty value surfaces as a 400 with an api.Problem whose code
// is api.CodeValidation. Cross-account probes return 404 (the
// same IDOR posture as GetDeployment / GetDeploymentStages).
//
// fromStage=source_download re-runs the entire pipeline
// (intentional — that's how a user "retry from the top" works).
//
// The route is keyed on the deployment id alone — the handler
// resolves the parent app via deployments.app_id, so the SDK
// caller (the CLI's `gregale deploys retry <id>`) doesn't need
// to know the slug.
func (c *Client) RetryDeploymentFromStage(ctx context.Context, id, fromStage string) (DeploymentResponse, error) {
	var out DeploymentResponse
	return out, c.do(ctx, "POST", "/v1/deployments/"+id+"/retry",
		RetryDeploymentRequest{FromStage: fromStage}, &out)
}

// GetDeployment returns a deployment by ID.
func (c *Client) GetDeployment(ctx context.Context, id string) (DeploymentResponse, error) {
	var out DeploymentResponse
	return out, c.do(ctx, "GET", "/v1/deployments/"+id, nil, &out)
}

// GetDeploymentScan returns the per-deploy grype CVE scan
// payload for one deployment (issue #464 / ADR-055). Returns
// the typed api.ScanResult envelope (status, severity counts,
// vulnerabilities, error). The handler returns 404 in three
// cases — deployment row missing, deployment belongs to a
// different account (IDOR-safe), or scan hasn't run yet —
// surfaced via the same ErrNotFound wrapping callers already
// branch on with errors.Is(err, api.ErrNotFound). Status
// is the closed enum (complete|failed|skipped); see
// pkg/api.ScanResult for the full wire shape.
func (c *Client) GetDeploymentScan(ctx context.Context, id string) (ScanResult, error) {
	var out ScanResult
	return out, c.do(ctx, "GET", "/v1/deployments/"+id+"/scan", nil, &out)
}

// GetDeploymentSecretScan returns the per-deploy image-layer
// secret-scan payload (PR-A). Same IDOR posture as
// GetDeploymentScan (404 on missing-deployment OR cross-account
// OR scan-pending). The wire shape is SecretScanResult — a
// closed-form {status, scanned_at, findings[], image_digest}
// envelope where the findings carry the per-finding layer
// label ("app" | "sidecar-<slug>") so the CLI / dashboard can
// attribute findings to the right image segment.
//
// Status is the closed enum (complete | complete_with_redactions)
// — distinct from the grype-side Status field on ScanResult
// because the two pipelines stamp separate audit rows.
func (c *Client) GetDeploymentSecretScan(ctx context.Context, id string) (SecretScanResult, error) {
	var out SecretScanResult
	return out, c.do(ctx, "GET", "/v1/deployments/"+id+"/secret-scan", nil, &out)
}

// GetDeploymentStages returns the closed-6-stage summary for a
// deployment (ADR-117 follow-up). Companion to GetDeployment (which
// returns the typed state.Deployment row) and the /logs SSE stream
// (which emits `event: stage` frames during a live deploy). This
// endpoint serves the post-stream summary use case — `gregale
// deploys show <id>` and the future dashboard widget.
//
// Wire shape: the same `state.StageState` JSON already stored on
// `deployments.stage_state` (migration 00302). The handler does NOT
// add a typed API DTO — the column's jsonb IS the wire. To avoid
// pulling the pkg/state import into pkg/api (which would create a
// cycle: pkg/state/memstore.go imports pkg/api for error codes), the
// SDK returns the raw jsonb; callers that want the typed view
// json.Unmarshal into state.StageState themselves. The closed
// vocabulary (`source_download` / `dependency_restore` / `image_build`
// / `security_scan` / `snapshot_prepare` / `readiness`) is enforced
// at the database layer by
// `deployments_stage_state_current_check`, so a malformed row would
// never reach the wire.
//
// 404 surfaces in three cases — deployment row missing, deployment
// belongs to a different account (IDOR-safe), or the deployment
// predates the migration backfill (unreachable in practice; the
// migration set NOT NULL DEFAULT on every existing row). All three
// are returned via the same ErrNotFound wrapping callers already
// branch on with errors.Is(err, api.ErrNotFound).
func (c *Client) GetDeploymentStages(ctx context.Context, id string) (json.RawMessage, error) {
	var out json.RawMessage
	return out, c.do(ctx, "GET", "/v1/deployments/"+id+"/stages", nil, &out)
}

// GetDeploymentURL (issue #976 / ADR-122 / SAFE-RELEASES-C.3) returns
// the per-deployment preview URL for one deployment. Wire call:
// GET /v1/deployments/{id}/url (returns the typed
// api.DeploymentPreviewURL envelope — Host and URL empty when the
// deployment isn't preview-active or the deployment-preview zone is
// disabled on this platform).
//
// Used by the dashboard's copy-URL chip + `gregale deploys show
// --url` to mint a link to a shareable preview surface without
// round-tripping gatewayd-internal. The 404 envelope mirrors
// GetDeploymentStages (cross-account probes are 404, never 403).
func (c *Client) GetDeploymentURL(ctx context.Context, id string) (DeploymentPreviewURL, error) {
	var out DeploymentPreviewURL
	return out, c.do(ctx, "GET", "/v1/deployments/"+id+"/url", nil, &out)
}

// PatchDeployment sets the per-deployment cold-wake floor override
// (issue #557 closure / ADR-072). MinInstances is the only mutable
// field on a deployment post-create — image / digest / overrides /
// sidecars stay immutable (a new deployment is the canonical way to
// change them). Pass MinInstances=0 to inherit from the parent app's
// floor; a positive value is the deployment's own floor. The handler
// validates against the parent app's plan MaxMinInstances cap.
func (c *Client) PatchDeployment(ctx context.Context, id string, req UpdateDeploymentRequest) (DeploymentResponse, error) {
	var out DeploymentResponse
	return out, c.do(ctx, "PATCH", "/v1/deployments/"+id, req, &out)
}

// GetBuildsIdProvenance returns the ADR-038 build_provenance row for
// a build id. Backs the `faas build provenance <id>` CLI command.
// The backend surfaces a missing row as a 404 with code
// build_provenance_not_found, which the SDK propagates as a
// *APIError — callers should check against apierr.Code() when the
// distinction matters (vs. a hard 404 "no such build").
//
// Method name: the sdk-coverage drift gate
// (cmd/sdk-coverage/main.go::deriveMethodName) auto-derives
// "Get<PathSegments>" from the route; for `GET /v1/builds/{id}/provenance`
// the natural form is `GetBuildsIdProvenance`. Renaming here is
// cheaper than pinning a methodRouteMap row that would diverge from
// every other /v1/{resource}/{id} SDK shape.
func (c *Client) GetBuildsIdProvenance(ctx context.Context, id string) (BuildProvenanceResponse, error) {
	var out BuildProvenanceResponse
	return out, c.do(ctx, "GET", "/v1/builds/"+id+"/provenance", nil, &out)
}

// GetBuildsIdSbom returns the CycloneDX SBOM for a build id
// (issue #299, ADR-038 Phase 3). Backs the `faas build sbom <id>`
// CLI command. The body is the raw CycloneDX 1.5 JSON the imaged
// populator wrote into storage at build-completion time; the SDK
// returns it as []byte (not a typed struct) so callers can hand it
// straight to `cyclonedx-cli validate` or to a dashboard renderer
// without an intermediate decode.
//
// Returns (nil, *APIError) when no SBOM exists for the build id —
// Phase-3 populator hasn't landed yet, the build predates the
// schema column, or the storage backend lost the artifact. The
// caller (CLI) surfaces this as a "no SBOM" hint.
func (c *Client) GetBuildsIdSbom(ctx context.Context, id string) ([]byte, error) {
	var out []byte
	return out, c.doBytes(ctx, "GET", "/v1/builds/"+id+"/sbom", nil, &out)
}

// GetBuildsId returns the lifecycle row for a build id (DEPLOY-PROV-6
// / ADR-089, issue #741). Backs `gregale build status <id>` and the
// CLI's SSE fallback pollBuildStatus loop.
//
// The 404 surface is "no such build" — both for non-existent ids
// and for cross-account probes (the server's IDOR chain collapses
// every negative path to a uniform 404). The SDK propagates the
// server's *APIError; callers can check apierr.Code() against
// CodeBuildNotFound when the distinction matters (e.g. for
// "follow manually" hints in the CLI).
//
// Method name: derived by cmd/sdk-coverage/main.go::deriveMethodName
// from `GET /v1/builds/{id}` → GetBuildsId. Mirrors the existing
// GetBuildsIdProvenance / GetBuildsIdSbom. No methodRouteMap entry
// needed; sdk-check fails if the derived name doesn't match.
func (c *Client) GetBuildsId(ctx context.Context, id string) (BuildResponse, error) {
	var out BuildResponse
	return out, c.do(ctx, "GET", "/v1/builds/"+id, nil, &out)
}

// GetBuilds returns a single page of builds across the
// authenticated account's deployments, ordered started_at DESC
// (nulls last; queued builds stay at the bottom of the first
// page). status="" means "any status". app="" means "any app".
// before is the opaque pagination cursor from a previous
// resp.NextBefore ("" for first page); limit is the page size
// (server clamps at 200, 0 means default).
//
// Cursor shape (post-review fix for issues #74 + #75): the wire
// format is "<started_at>|<id_hex>" — the id is the Build.ID of
// the last row on the previous page. The id tiebreaker solves
// two problems the original single-column cursor had:
//  1. queued builds (started_at IS NULL) had no anchor — the
//     cursor was always the last non-null row, which silently
//     dropped the queued tail across page boundaries.
//  2. whole-second wire precision truncates sub-second DB
//     started_at — two rows in the same wall-clock second
//     were always both bound by the same strict-less-than.
//
// The id tiebreaker makes the keyset comparison deterministic
// regardless of precision loss and lets queued-only pages
// still thread a cursor (the started_at segment is empty).
//
// The cursor is opaque — re-parse it on the wire side via the
// server's `?before=<cursor>` round-trip, not via time.Parse.
// See ADR-091 §3 + the code-review fix.
//
// Backs `gregale build list` and any CI script that wants
// "what's still running for app X" without scraping SSE
// (DEPLOY-PROV-6 follow-up / ADR-091, issue #741 close-out).
//
// Method name: derived by cmd/sdk-coverage/main.go::deriveMethodName
// from `GET /v1/builds` → GetBuilds. Matches the existing
// aggregate-from-all-apps convention (/v1/instances → GetInstances,
// /v1/secrets → GetSecrets, /v1/apps/metrics → GetAppsMetrics —
// see cmd/sdk-coverage/main.go:306-310). No methodRouteMap entry
// needed; sdk-check fails if the derived name doesn't match.
func (c *Client) GetBuilds(ctx context.Context, app, status, before string, limit int) (BuildListResponse, error) {
	var out BuildListResponse
	v := url.Values{}
	if app != "" {
		v.Set("app", app)
	}
	if status != "" {
		v.Set("status", status)
	}
	if before != "" {
		v.Set("before", before)
	}
	if limit > 0 {
		v.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := "/v1/builds"
	if encoded := v.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// DeployMultipart ships a source tarball (with optional runtime +
// handler) to the multipart deploy endpoint. sourceName is the form
// filename apid sees in the multipart "source" part; pass the
// basename of the customer's file. source must implement io.Reader
// (e.g. *os.File, *bytes.Buffer). The caller is responsible for any
// pre-open security validation the surface requires — the SDK makes
// no assumptions about the file backend.
//
// For zero-knowledge of a customer file's provenance (the CLI's
// `faas deploy --tarball` refuses symlinks via openCustomerFile),
// wrap openCustomerFile before calling DeployMultipart.
func (c *Client) DeployMultipart(ctx context.Context, slug string, source io.Reader, sourceName, runtime, handler string, dockerfile bool, ann DeployAnnotations) (DeploymentResponse, error) {
	return c.DeployMultipartWithSourceRoot(ctx, slug, source, sourceName, runtime, handler, dockerfile, "", ann)
}

// DeployMultipartWithSourceRoot is the workspace-aware source upload path.
// sourceRoot is a repository-relative directory inside the uploaded archive;
// an empty value means the archive root. The legacy DeployMultipart method
// delegates here with an empty root so existing callers remain unchanged.
func (c *Client) DeployMultipartWithSourceRoot(ctx context.Context, slug string, source io.Reader, sourceName, runtime, handler string, dockerfile bool, sourceRoot string, ann DeployAnnotations) (DeploymentResponse, error) {
	storedRoot, err := normalizeMultipartSourceRoot(sourceRoot)
	if err != nil {
		return DeploymentResponse{}, fmt.Errorf("invalid source root: %w", err)
	}
	var b bytes.Buffer
	w := newMultipartWriterWithSourceRoot(&b, slug, dockerfile, runtime, handler, storedRoot, ann)
	fw, err := w.CreateFormFile("source", sourceName)
	if err != nil {
		return DeploymentResponse{}, fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(fw, source); err != nil {
		return DeploymentResponse{}, fmt.Errorf("copy source: %w", err)
	}
	if err := w.Close(); err != nil {
		return DeploymentResponse{}, fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/apps/"+slug+"/deployments", &b)
	if err != nil {
		return DeploymentResponse{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	// DeployMultipart bypasses Client.do (multipart Content-Type wins
	// over the JSON default) and routes through the longer-timeout
	// upload client. Auto-mint Idempotency-Key here so retry-safe
	// semantics still hold; the file-open guard (if any) runs at the
	// caller before this mint, so a rejected path never produces an
	// Idempotency-Key on the wire.
	req.Header.Set("Idempotency-Key", newUUIDv4())
	var out DeploymentResponse
	return out, c.doReq(c.uploadHTTP(), req, &out)
}

// DeployFromSourceRef is the headless CI deploy path (issue #739 /
// DEPLOY-PROV-4 / ADR-092). Caller supplies a GitHub repo slug and
// the ref they want to deploy — branch, tag, or 40-char SHA — and
// apid resolves the durable install, mints an install-token via
// githubd, fetches the codeload tarball, spools it under the
// per-plan SourceTarballMaxMB cap, and enqueues a build pinned to
// the resolved commit SHA. The full Idempotency-Key envelope is
// auto-minted by c.do; CI retries with the same key fold into one
// build row.
//
// Format: PR-A only supports "tarball". The field is required by
// the wire shape but the server enforces it; passing an empty
// string keeps callers forward-compat when a future format lands.
func (c *Client) DeployFromSourceRef(ctx context.Context, slug string, req SourceRefDeployRequest) (DeploymentResponse, error) {
	var out DeploymentResponse
	return out, c.do(ctx, "POST",
		"/v1/apps/"+slug+"/deployments/source-ref", req, &out)
}

// DeployFromSourceTarball is the zero-config local-tarball deploy
// path (issue #961 / Mega-A PR-1). The caller (CLI) uploads a tarball
// it produced locally; apid does NOT consult github_installations
// and does NOT attempt a server-side git fetch. The CLI is the trust
// root for this path; see docs/adr/0XX-local-tarball-deploy-trust-
// root.md.
//
// tarballName is the form filename apid sees in the multipart
// "tarball" part (basename is fine). repo + ref are optional
// informational fields shipped as the JSON `sidecar` part; the
// build pipeline does NOT use them to fetch upstream.
//
// Idempotency-Key is auto-minted here so retries fold into one
// build row, mirroring DeployFromSourceRef.
func (c *Client) DeployFromSourceTarball(ctx context.Context, slug string, tarball io.Reader, tarballName string, sidecar SourceTarballDeployRequest) (DeploymentResponse, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	// tarball: file part. The CLI side wraps `packDirToTarGz` so the
	// shape is already §9-valid (≤10k files, no symlinks/.., absolute
	// paths stripped); the server's validateAndSpool validates again
	// before rename so the upload is defence-in-depth.
	fw, err := w.CreateFormFile("tarball", tarballName)
	if err != nil {
		return DeploymentResponse{}, fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(fw, tarball); err != nil {
		return DeploymentResponse{}, fmt.Errorf("copy tarball: %w", err)
	}
	// sidecar: optional JSON. Empty repo+ref → omit the part entirely
	// (the server treats missing sidecar as zero provenance).
	if sidecar.Repo != "" || sidecar.Ref != "" {
		sidecarJSON, err := json.Marshal(sidecar)
		if err != nil {
			return DeploymentResponse{}, fmt.Errorf("marshal sidecar: %w", err)
		}
		if err := w.WriteField("sidecar", string(sidecarJSON)); err != nil {
			return DeploymentResponse{}, fmt.Errorf("write sidecar: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return DeploymentResponse{}, fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/apps/"+slug+"/deployments/source-tarball", &b)
	if err != nil {
		return DeploymentResponse{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	// Auto-mint Idempotency-Key (matches DeployFromSourceRef).
	req.Header.Set("Idempotency-Key", newUUIDv4())

	var out DeploymentResponse
	return out, c.doReq(c.uploadHTTP(), req, &out)
}

// Diff returns a read-only preview of what a deploy would change
// without writing. CI calls this in the same job that calls
// Deploy; non-zero exit (or Blocking=true in the wire) means
// "don't deploy". Mirrors the CLI's `gregale deploy --diff --json`
// output byte-for-byte — a CI consumer parsing either path
// agrees.
//
// Read-only: auth chain on the server is apps:read (no MFA, no
// deploy:write required). The handler does not call CreateApp /
// CreateDeployment / anything in the write path.
func (c *Client) Diff(ctx context.Context, slug string, req DiffRequest) (DiffResponse, error) {
	var out DiffResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/diff", req, &out)
}

// GetApp returns the app metadata for a slug.
func (c *Client) GetApp(ctx context.Context, slug string) (AppResponse, error) {
	var out AppResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug, nil, &out)
}

// UpdateApp applies a partial update to an app.
func (c *Client) UpdateApp(ctx context.Context, slug string, req UpdateAppRequest) (AppResponse, error) {
	var out AppResponse
	return out, c.do(ctx, "PATCH", "/v1/apps/"+slug, req, &out)
}

// RenameApp swaps an app's slug atomically (issue #63).
func (c *Client) RenameApp(ctx context.Context, oldSlug, newSlug string) (AppResponse, error) {
	var out AppResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+oldSlug+"/rename",
		RenameAppRequest{NewSlug: newSlug}, &out)
}

// DeleteApp removes an app.
func (c *Client) DeleteApp(ctx context.Context, slug string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug, nil, nil)
}

// DestroyPreview tears down a preview app (issue #961 Mega-C
// PR-1, leaf 3). Distinct from DeleteApp because the preview
// teardown also stamps apps.preview_pr_state='torn_down' so the
// janitor doesn't re-process the row, and emits a distinct audit
// kind (preview.destroyed_by_customer vs app.deleted). The slug
// must identify a preview app (PreviewOfSlug != "") — a
// production slug returns 404, not 204.
func (c *Client) DestroyPreview(ctx context.Context, slug string) error {
	return c.do(ctx, "POST", "/v1/preview/"+slug+"/destroy", nil, nil)
}

// ScanProject ships a source tarball to the dry-run endpoint. The
// response carries the discovered workloads, managed services,
// derived scan_source, and a plan_token that ApplyProjectPlan can
// echo back on the same multipart body to skip the second extract
// in the interactive flow. No writes — POST /v1/projects/scan.
func (c *Client) ScanProject(
	ctx context.Context,
	source io.Reader, sourceName, projectSlug, productionBranch string,
	installID int64, only, exclude []string, persistExclude bool,
) (PlanResponse, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if err := writeProjectMultipartFields(w, source, sourceName, projectSlug, productionBranch, installID, only, exclude, persistExclude); err != nil {
		return PlanResponse{}, fmt.Errorf("build multipart: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/projects/scan", &b)
	if err != nil {
		return PlanResponse{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	var out PlanResponse
	return out, c.doReq(c.uploadHTTP(), req, &out)
}

// ApplyProjectPlan ships the same multipart body as ScanProject
// plus a plan_token query parameter to /v1/projects. The token is
// optional — pass "" to force a fresh extract + scan + quota check
// on the server. On over-quota the response carries the matching
// 402/403 RFC 7807 problem with zero rows inserted.
func (c *Client) ApplyProjectPlan(
	ctx context.Context,
	planToken string,
	source io.Reader, sourceName, projectSlug, productionBranch string,
	installID int64, only, exclude []string, persistExclude bool,
) (ApplyResponse, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if err := writeProjectMultipartFields(w, source, sourceName, projectSlug, productionBranch, installID, only, exclude, persistExclude); err != nil {
		return ApplyResponse{}, fmt.Errorf("build multipart: %w", err)
	}
	endpoint := c.baseURL + "/v1/projects"
	if planToken != "" {
		endpoint += "?plan_token=" + url.QueryEscape(planToken)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, &b)
	if err != nil {
		return ApplyResponse{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Idempotency-Key", newUUIDv4())
	var out ApplyResponse
	return out, c.doReq(c.uploadHTTP(), req, &out)
}

// DeleteDeploymentScopeExclusion drops a single persisted
// --exclude row from deployment_scope_exclusions (ADR-124
// code-review fix #2). The CLI's `gregale deployments exclude clear
// --slug=...` calls into here as the operator-grade escape hatch
// when a persisted slug no longer exists in the repo and is
// blocking deploys. The server returns 404 with Problem
// code="scope_exclusion_not_found" when no row matches; the CLI
// branches on that via errors.As(&*api.APIError) to render
// "already clear" rather than surface a hard error.
func (c *Client) DeleteDeploymentScopeExclusion(ctx context.Context, projectSlug, slug string) error {
	path := "/v1/projects/" + url.PathEscape(projectSlug) + "/exclusions/" + url.PathEscape(slug)
	return c.do(ctx, "DELETE", path, nil, nil)
}

// writeProjectMultipartFields serializes the multipart body shared
// by ScanProject + ApplyProjectPlan. The fields exactly mirror the
// OpenAPI ProjectScanRequest schema (the spec-compliance AST gate
// enforces the field-for-field mapping).
func writeProjectMultipartFields(
	w *multipart.Writer, source io.Reader, sourceName, projectSlug,
	productionBranch string, installID int64, only, exclude []string,
	persistExclude bool,
) error {
	fw, err := w.CreateFormFile("source", sourceName)
	if err != nil {
		return err
	}
	if _, err := io.Copy(fw, source); err != nil {
		return err
	}
	if projectSlug != "" {
		if err := w.WriteField("project_slug", projectSlug); err != nil {
			return err
		}
	}
	if productionBranch != "" {
		if err := w.WriteField("production_branch", productionBranch); err != nil {
			return err
		}
	}
	if installID > 0 {
		if err := w.WriteField("install_id", fmt.Sprintf("%d", installID)); err != nil {
			return err
		}
	}
	if len(only) > 0 {
		if err := w.WriteField("only", strings.Join(only, ",")); err != nil {
			return err
		}
	}
	// ADR-124: inverse-allowlist. Server treats this as a sibling of
	// `only` (lowercased name match). intersect(only, exclude) is
	// rejected at the server with code='exclude_only_overlap'.
	if len(exclude) > 0 {
		if err := w.WriteField("exclude", strings.Join(exclude, ",")); err != nil {
			return err
		}
	}
	// ADR-124 follow-up #3 (PR-B commit 5): write-side persist
	// flag. Server ignores it on the scan path; on the apply path
	// it triggers CreateDeploymentScopeExclusion per excluded slug
	// on a successful apply. Default OFF — the operator's intent
	// is explicit. The field is emitted only when true to keep
	// existing wire captures stable.
	if persistExclude {
		if err := w.WriteField("persist_exclude", "true"); err != nil {
			return err
		}
	}
	return nil
}

// ChangePlan changes the account's subscription tier.
func (c *Client) ChangePlan(ctx context.Context, plan string) (AccountResponse, error) {
	var out AccountResponse
	return out, c.do(ctx, "PATCH", "/v1/account/plan",
		map[string]string{"plan": plan}, &out)
}

// RaiseOverageCap sets the account's monthly overage cap (issue #561).
// Pass a non-negative int64 to set the cap (0 = "no overage allowed");
// pass nil to clear the cap (NULL round-trip). The server returns the
// updated account state. Caps are enforced by schedd: once the
// current-month overage meets/exceeds the cap, new wakes are refused
// with CodeAdmissionRefused (HTTP 402). The Idempotency-Key header
// is auto-minted by the underlying do() helper unless the caller
// passes an override via c.doRequest.
func (c *Client) RaiseOverageCap(ctx context.Context, overageCapCents *int64) (AccountResponse, error) {
	body := map[string]any{"overage_cap_cents": overageCapCents}
	var out AccountResponse
	return out, c.do(ctx, "POST", "/v1/account/overage-cap", body, &out)
}

// GetEgressAllowlistExtra returns the per-account additive budget on
// top of the plan's apps.egress_allowlist cap (issue #679 / PR-B /
// ADR-082). The response carries the live value plus the plan cap
// and the global ceiling so the CLI can render the trio without a
// second round-trip. Admin scope + MFA are required.
func (c *Client) GetEgressAllowlistExtra(ctx context.Context) (AccountEgressAllowlistExtraResponse, error) {
	var out AccountEgressAllowlistExtraResponse
	return out, c.do(ctx, "GET", "/v1/account/egress_allowlist_extra", nil, &out)
}

// SetEgressAllowlistExtra sets the per-account additive budget
// (issue #679 / PR-B / ADR-082). Pass 0 to clear the override (the
// plan cap is authoritative again). Negative values or values above
// the global ceiling are rejected with
// CodeAccountEgressAllowlistExtraOutOfRange (HTTP 400). Admin scope
// + MFA are required.
func (c *Client) SetEgressAllowlistExtra(ctx context.Context, extra int) (AccountEgressAllowlistExtraResponse, error) {
	var out AccountEgressAllowlistExtraResponse
	return out, c.do(ctx, "PATCH", "/v1/account/egress_allowlist_extra",
		SetAccountEgressAllowlistExtraRequest{Extra: extra}, &out)
}

// GetStatusSLO fetches the public SLO snapshot.
func (c *Client) GetStatusSLO(ctx context.Context) (StatusPage, error) {
	var out StatusPage
	return out, c.do(ctx, "GET", "/status/slo.json", nil, &out)
}

// Rollback re-promotes the most recent superseded deployment.
func (c *Client) Rollback(ctx context.Context, slug string) (DeploymentResponse, error) {
	var out DeploymentResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/rollback", nil, &out)
}

// RollbackTo is the SAFE-RELEASES-G (issue #976) variant of Rollback.
// When targetDeploymentID is empty it degrades to the legacy
// "rollback to most-recent superseded" path (same as Rollback) and
// sends no body. When non-empty, the handler validates that the id
// (a) belongs to the app and (b) has status='superseded', and
// returns a typed error otherwise. Prefer this over Rollback in any
// new code so the future-safe shape is the default; Rollback is
// kept for SDK back-compat (generated SDK consumers don't break).
func (c *Client) RollbackTo(ctx context.Context, slug, targetDeploymentID string) (DeploymentResponse, error) {
	var out DeploymentResponse
	if targetDeploymentID == "" {
		// Match Rollback: no body for the legacy "most-recent
		// superseded" path. Avoids wire noise and keeps the
		// DisallowUnknownFields handler happy when fields are added
		// to RollbackRequest later.
		return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/rollback", nil, &out)
	}
	body := RollbackRequest{TargetDeploymentID: &targetDeploymentID}
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/rollback", body, &out)
}

// RollbackToWithRule (SAFE-RELEASES-OBS PR-D, issue #976 / ADR-122)
// is the variant of RollbackTo that ALSO stamps the
// deployment_audit row's alert_rule_id column with the supplied
// alert rule UUID. Used by the meterd safedeploy ActionDispatcher
// when an alert rule's action=rollback fires and the dispatcher
// triggers the apid rollback; this lets the operator click from
// the audit timeline back to /dashboard/alerts/{id}.
//
// Why a separate method (not just adding an optional parameter to
// RollbackTo): keeping RollbackTo's signature stable preserves the
// generated SDK surface (RollbackTo is a public SDK method). The
// new entry point is an internal seam used only by meterd; SDK
// callers continue to call RollbackTo unchanged.
//
// alertRuleID is the UUID of the alert_rules row that fired;
// pass "" (empty) to fall back to RollbackTo's behaviour with no
// rule stamping. The handler stamps alert_rule_id only when both
// the body's AlertRuleID parses as a UUID AND the rule row
// exists — otherwise the audit row is emitted with alert_rule_id
// NULL (fail-soft; an invalid rule id should not block the
// rollback itself).
func (c *Client) RollbackToWithRule(ctx context.Context, slug, targetDeploymentID, alertRuleID string) (DeploymentResponse, error) {
	return c.RollbackToWithRuleAndIdempotencyKey(ctx, slug, targetDeploymentID, alertRuleID, "")
}

// RollbackToWithRuleAndIdempotencyKey is the safe-release internal variant
// that lets meterd derive one stable mutation key per rollout. An empty key
// preserves the normal SDK auto-mint behavior.
func (c *Client) RollbackToWithRuleAndIdempotencyKey(ctx context.Context, slug, targetDeploymentID, alertRuleID, idempotencyKey string) (DeploymentResponse, error) {
	var out DeploymentResponse
	body := RollbackRequest{TargetDeploymentID: &targetDeploymentID}
	if alertRuleID != "" {
		body.AlertRuleID = &alertRuleID
	}
	return out, c.doWithIdempotencyKey(ctx, "POST", "/v1/apps/"+slug+"/rollback", body, &out, idempotencyKey)
}

// ListDeploymentAudit returns the deployment_audit timeline for
// one deployment (issue #976 / ADR-122 / SAFE-RELEASES-E.2 +
// production-leveling Stream A). The handler
// (cmd/apid/handlers_audit.go::listDeploymentAudit) caps the
// page at listAuditEventsLimitMax (100) — pass a smaller limit
// for paginated iteration. Newest-first ordering matches the
// (deployment_id, at DESC) index on the table
// (migrations/00477_deployment_audit.sql).
func (c *Client) ListDeploymentAudit(ctx context.Context, deploymentID string, limit int) (ListDeploymentAuditResponse, error) {
	var out ListDeploymentAuditResponse
	return out, c.do(ctx, "GET",
		fmt.Sprintf("/v1/deployments/%s/audit?limit=%d", deploymentID, limit),
		nil, &out)
}

// UpdateDeploymentTraffic stamps the per-deployment traffic-split
// weight (issue #556 PR-A). percent must be in [0, 100]; the
// handler enforces the range (422) and the plan gate (403,
// Pro/Scale only). PR-A semantics: zeroing sibling live rows so
// Σ over the app's live rows stays 100 by construction. The
// returned DTO carries the refreshed TrafficPercent field.
//
// Method name matches the OpenAPI operationId-derived SDK alias
// `PatchDeploymentsIdTraffic` (cmd/sdk-coverage derives names via
// `<Method><PathSegments>` — PATCH /v1/deployments/{id}/traffic
// becomes `PatchDeploymentsIdTraffic`). The PascalCase name
// matches the route's verb path, which is how the generated SDK
// names align.
func (c *Client) PatchDeploymentsIdTraffic(ctx context.Context, id string, percent int) (DeploymentResponse, error) {
	var out DeploymentResponse
	return out, c.do(ctx, "PATCH", "/v1/deployments/"+id+"/traffic",
		UpdateDeploymentTrafficRequest{TrafficPercent: percent}, &out)
}

// AdvanceCanary advances exactly one persisted canary step. APID resolves
// the next percentage from the deployment's stored preset and performs the
// expected-step compare-and-swap together with traffic, rollout state, and
// audit writes. A 409 means another worker won the race and the caller should
// re-read the row on its next tick.
func (c *Client) AdvanceCanary(ctx context.Context, id string, expectedStep int) (CanaryAdvanceResponse, error) {
	var out CanaryAdvanceResponse
	return out, c.do(ctx, "POST", "/v1/deployments/"+id+"/canary/advance",
		AdvanceCanaryRequest{ExpectedStep: expectedStep}, &out)
}

// RecoverRollout (issue #976 / ADR-122 / SAFE-RELEASES-R) is the
// operator manual-recovery escape hatch — POST
// /v1/apps/{slug}/rollouts/recover. The CLI subcommand
// `gregale rollouts recover <slug> --action advance|promote|abort
// --reason <text>` is the canonical caller; the SDK method is
// here for the rare operator who scripts recovery directly.
//
// action ∈ {"advance", "promote", "abort"} — the handler does
// the closed-set check (422 ErrInvalidRecoverAction on bad
// input). reason is captured into the deployment_audit row's
// data payload. The returned RolloutTransitionResponse carries
// the post-transition Deployment + the audit row id so the
// operator's terminal can echo the chip.
func (c *Client) RecoverRollout(ctx context.Context, slug, action, reason string) (RolloutTransitionResponse, error) {
	return c.RecoverRolloutAndIdempotencyKey(ctx, slug, action, reason, "")
}

// RecoverRolloutAndIdempotencyKey is the safe-release internal variant that
// lets meterd share a rollout-scoped idempotency key across alert rules.
// An empty key preserves the normal SDK auto-mint behavior.
func (c *Client) RecoverRolloutAndIdempotencyKey(ctx context.Context, slug, action, reason, idempotencyKey string) (RolloutTransitionResponse, error) {
	var out RolloutTransitionResponse
	body := RecoverRolloutRequest{Action: action, Reason: reason}
	return out, c.doWithIdempotencyKey(ctx, "POST", "/v1/apps/"+slug+"/rollouts/recover", body, &out, idempotencyKey)
}

// Park and Wake toggle the app between cold-parked and live.
func (c *Client) Park(ctx context.Context, slug string) error {
	return c.do(ctx, "POST", "/v1/apps/"+slug+"/park", nil, nil)
}
func (c *Client) Wake(ctx context.Context, slug string) error {
	return c.do(ctx, "POST", "/v1/apps/"+slug+"/wake", nil, nil)
}
func (c *Client) ListInstances(ctx context.Context, slug string) ([]InstanceResponse, error) {
	var out []InstanceResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/instances", nil, &out)
}

// GetInstances returns every live instance across the caller's account
// (issue #393). One call replaces N per-app ListInstances calls. The
// cursor / limit semantics mirror /v1/invoices: before is the last
// instance.id from a previous page, limit clamps to 1..100 (default 25).
// Cross-account isolation is a property of the SQL — the SDK doesn't
// need to scope the call. See ADR-045.
func (c *Client) GetInstances(ctx context.Context, before string, limit int) (ListInstancesResponse, error) {
	var out ListInstancesResponse
	q := url.Values{}
	if before != "" {
		q.Set("before", before)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/instances"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// Domains.
func (c *Client) ListDomains(ctx context.Context) ([]CustomDomainResponse, error) {
	var out []CustomDomainResponse
	return out, c.do(ctx, "GET", "/v1/domains", nil, &out)
}
func (c *Client) CreateDomain(ctx context.Context, req CreateCustomDomainRequest) (CustomDomainResponse, error) {
	var out CustomDomainResponse
	return out, c.do(ctx, "POST", "/v1/domains", req, &out)
}
func (c *Client) DeleteDomain(ctx context.Context, domain string) error {
	return c.do(ctx, "DELETE", "/v1/domains/"+domain, nil, nil)
}

// VerifyDomain (issue #961 / Mega-A PR-3) re-runs the DNS + cert
// walk for a custom domain. Idempotent — POSTing twice does not
// change the durable verification state. Backed by
// POST /v1/domains/{domain}/verify; the SDK caller does not need to
// pass an Idempotency-Key (auto-minted by the SDK roundtripper).
func (c *Client) VerifyDomain(ctx context.Context, domain string) (CustomDomainResponse, error) {
	var out CustomDomainResponse
	return out, c.do(ctx, "POST", "/v1/domains/"+domain+"/verify", nil, &out)
}

// GetDomain (issue #961 / Mega-A PR-3) returns a domain's durable
// row + the live cert chain (NotAfter, SANs). Backed by GET
// /v1/domains/{domain}. The cert dial is on-demand; failures to
// reach the cert surface as no cert metadata on the response (the
// next show retry picks up the propagation).
func (c *Client) GetDomain(ctx context.Context, domain string) (CustomDomainResponse, error) {
	var out CustomDomainResponse
	return out, c.do(ctx, "GET", "/v1/domains/"+domain, nil, &out)
}

// DomainDoctor (ADR-120) returns the 5-check doctor report
// for a domain. Backed by GET /v1/domains/{domain}/doctor.
// The handler reads the latest observation row from
// domain_doctor_observations (the dns_poller writes a row
// every 30s); on a stale or missing row the handler
// triggers a synchronous re-probe with a 5s budget and
// returns the refreshed report. Stale=true on the response
// means the cache was older than FAAS_DOMAIN_DOCTOR_TTL_SECONDS
// when the handler ran.
//
// Used by `gregale domains doctor <domain>`. The 503
// CodeDoctorDisabled error is returned when the operator
// hasn't set FAAS_DOMAIN_DOCTOR_ENABLED.
func (c *Client) DomainDoctor(ctx context.Context, domain string) (DomainDoctorReport, error) {
	var out DomainDoctorReport
	return out, c.do(ctx, "GET", "/v1/domains/"+domain+"/doctor", nil, &out)
}

// Tenant surfaces (issue #879 / ADR-100 PR-C). The CLI surface
// (cmd/gregale/commands_tenant_surfaces.go) calls these; the
// HTTP handlers they're backed by live in
// cmd/apid/handlers_tenant_surfaces.go. The ListTenantSurfaces
// path is /v1/apps/{slug}/tenant-surfaces and the SDK signature
// takes the slug so the call site doesn't rebuild the URL.

// ListTenantSurfaces returns every active tenant surface on the
// app the slug belongs to. Soft-deleted surfaces are filtered
// server-side, so the SDK returns the same set the dashboard sees.
func (c *Client) ListTenantSurfaces(ctx context.Context, slug string) ([]TenantSurfaceResponse, error) {
	var out ListTenantSurfacesResponse
	if err := c.do(ctx, "GET", "/v1/apps/"+slug+"/tenant-surfaces", nil, &out); err != nil {
		return nil, err
	}
	return out.Surfaces, nil
}

// CreateTenantSurface attaches a new surface to the app. The
// hostnames list is the seed set the customer wants to certify
// under one SAN bundle; further hostnames can be added via
// AddTenantHostname.
func (c *Client) CreateTenantSurface(ctx context.Context, slug string, req CreateTenantSurfaceRequest) (TenantSurfaceResponse, error) {
	var out TenantSurfaceResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/tenant-surfaces", req, &out)
}

// GetTenantSurface returns one surface by id (UUID).
func (c *Client) GetTenantSurface(ctx context.Context, slug, id string) (TenantSurfaceResponse, error) {
	var out TenantSurfaceResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/tenant-surfaces/"+id, nil, &out)
}

// DeleteTenantSurface soft-deletes the surface and cascades the
// hostnames (server-side). The next attempt to add the same
// hostname to a new surface succeeds because the orphan rows
// are hard-deleted.
func (c *Client) DeleteTenantSurface(ctx context.Context, slug, id string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/tenant-surfaces/"+id, nil, nil)
}

// AddTenantHostname appends a hostname to an existing surface.
// The challenge token is returned in the response so the CLI
// can print the TXT record the customer must publish.
func (c *Client) AddTenantHostname(ctx context.Context, slug, surfaceID string, req AddTenantHostnameRequest) (TenantHostnameResponse, error) {
	var out TenantHostnameResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/tenant-surfaces/"+surfaceID+"/hostnames", req, &out)
}

// RemoveTenantHostname deletes a hostname from a surface. The
// hostname is the lowercased canonical form (the server
// lowercases on the way in).
func (c *Client) RemoveTenantHostname(ctx context.Context, slug, surfaceID, hostname string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/tenant-surfaces/"+surfaceID+"/hostnames/"+hostname, nil, nil)
}

// GetCron returns one cron by id (issue #791 PR-E / ADR-090 closure).
// Backs `gregale crons info <id>`. Wire shape matches CronResponse
// (same projection as ListCrons' per-row). The server returns a
// byte-identical 404 on missing or cross-account so the SDK does not
// invent a local branch that could leak existence.
func (c *Client) GetCron(ctx context.Context, id string) (CronResponse, error) {
	var out CronResponse
	return out, c.do(ctx, "GET", "/v1/crons/"+id, nil, &out)
}

// ListCrons returns every cron on the account when slug is empty,
// or every cron for the given app when slug is non-empty. The slug
// filter is added to the wire only when non-empty so the request
// matches the spec (zero documented parameters) and the server-side
// listCrons handler returns 200 with the full account-scoped list.
func (c *Client) ListCrons(ctx context.Context, slug string) ([]CronResponse, error) {
	path := "/v1/crons"
	if slug != "" {
		q := url.Values{}
		q.Set("slug", slug)
		path += "?" + q.Encode()
	}
	var out []CronResponse
	return out, c.do(ctx, "GET", path, nil, &out)
}
func (c *Client) CreateCron(ctx context.Context, slug string, req CreateCronRequest) (CronResponse, error) {
	var out CronResponse
	return out, c.do(ctx, "POST", "/v1/crons", req, &out)
}

// UpdateCron edits a cron's schedule/path/enabled. Pointer-based
// fields let the caller distinguish "unset" from "explicit zero" —
// matches the partial-update shape of Client.UpdateApp. The wire
// method is PATCH; the idempotency-key auto-mint covers this call
// (TestDo_MutatingCallsCarryIdempotencyKey in client_test.go).
func (c *Client) UpdateCron(ctx context.Context, id string, req UpdateCronRequest) (CronResponse, error) {
	var out CronResponse
	return out, c.do(ctx, "PATCH", "/v1/crons/"+id, req, &out)
}
func (c *Client) DeleteCron(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/crons/"+id, nil, nil)
}

// --- Jobs (issue #1184 Workstream A) ----------------------------------------
// Methods mirror the /v1/jobs surface added in M11.4. Routes are
// keyed on the customer's slug (`name`) for create/list/update/delete;
// runs + tasks use the opaque run id (uuid) so cross-account
// enumeration cannot scrape run ids. Logs are read from vmmd's tail
// endpoint (same path the dashboard uses for live app logs); the
// handler proxies the call to the compute node that owns the
// instance. The CLI surface lives in cmd/gregale/commands_jobs.go.

// ListJobs returns the account-scoped list of jobs (the /v1/jobs
// GET route). Wire shape: ListJobsResponse (jobs[] + limit +
// offset + next_offset + total). Server clamps limit to [1,200].
// Matches the CronList convention: zero query parameters on the
// wire so the spec parity gate (TestSpecCompliance) stays green.
func (c *Client) ListJobs(ctx context.Context) (ListJobsResponse, error) {
	var out ListJobsResponse
	return out, c.do(ctx, "GET", "/v1/jobs", nil, &out)
}

// CreateJob creates a new job under the calling account. Idempotent
// (POST → 201; replay returns the existing JobResponse with the same
// Idempotency-Key header). The handler applies per-plan defaults
// + clamps every numeric field; passing 0 on ram_mb / task_timeout_s
// / max_parallelism / retry_max lets the plan default win.
func (c *Client) CreateJob(ctx context.Context, req CreateJobRequest) (JobResponse, error) {
	var out JobResponse
	return out, c.do(ctx, "POST", "/v1/jobs", req, &out)
}

// GetJob returns one job by name (issue #1184 Workstream A).
// Backs `gregale jobs info <name>`. Wire shape matches JobResponse.
// The server returns a byte-identical 404 on missing or
// cross-account so the SDK does not leak existence (matches
// GetCron's IDOR posture).
func (c *Client) GetJob(ctx context.Context, name string) (JobResponse, error) {
	var out JobResponse
	return out, c.do(ctx, "GET", "/v1/jobs/"+name, nil, &out)
}

// UpdateJob patches a job's image_ref / command / env_overrides /
// ram_mb / task_timeout_sec / max_parallelism / retry_max / status.
// Pointer-based fields let the caller distinguish "unset" from
// "explicit zero". Wire method is PATCH; the idempotency-key
// auto-mint covers this call (TestDo_MutatingCallsCarryIdempotencyKey
// in client_test.go).
func (c *Client) UpdateJob(ctx context.Context, name string, req UpdateJobRequest) (JobResponse, error) {
	var out JobResponse
	return out, c.do(ctx, "PATCH", "/v1/jobs/"+name, req, &out)
}

// DeleteJob soft-deletes a job (issue #1184 Workstream A). Returns
// 204 with a JobDeletedResponse body on success; returns 409
// CodeJobHasLiveInstances when live (kind='job_task', status NOT
// IN ('parked','destroyed')) instances exist. The server check is
// enforced inside the soft_delete_job_if_no_live_instances stored
// function (migrations/00576) so the dispatch tick cannot lose a
// task mid-flight.
func (c *Client) DeleteJob(ctx context.Context, name string) (JobDeletedResponse, error) {
	var out JobDeletedResponse
	return out, c.do(ctx, "DELETE", "/v1/jobs/"+name, nil, &out)
}

// CreateJobRun fan-outs N tasks for the given job (POST
// /v1/jobs/{name}/runs). Atomic via generate_series INSERT inside
// state.PgStore.JobRunCreate (see migrations/00255 + 00574). The
// handler validates Tasks against Plan.JobMaxTasksPerRun
// (Hobby=100, Pro=1000, Scale=5000). Idempotent (Idempotency-Key
// header auto-mint).
func (c *Client) CreateJobRun(ctx context.Context, name string, req CreateJobRunRequest) (JobRunResponse, error) {
	var out JobRunResponse
	return out, c.do(ctx, "POST", "/v1/jobs/"+name+"/runs", req, &out)
}

// ListJobRuns returns a page of the job's run history
// (issue #1184 Workstream A). newest-first by created_at desc.
// Server clamps limit to [1,200] and surfaces a 400 Problem on
// garbage input. For a wider, cross-source view use ListInvocations.
func (c *Client) ListJobRuns(ctx context.Context, name string) (ListJobRunsResponse, error) {
	var out ListJobRunsResponse
	return out, c.do(ctx, "GET", "/v1/jobs/"+name+"/runs", nil, &out)
}

// GetJobRun returns one run by id (uuid). Backs `gregale jobs run
// <name> <id>`. Wire shape matches JobRunResponse.
func (c *Client) GetJobRun(ctx context.Context, name, runID string) (JobRunResponse, error) {
	var out JobRunResponse
	return out, c.do(ctx, "GET", "/v1/jobs/"+name+"/runs/"+runID, nil, &out)
}

// CancelJobRun cancels a run (POST /v1/jobs/{name}/runs/{id}/
// cancel). For queued tasks: JobTaskCancel (status='cancelled',
// instance_id=NULL). For claimed/running tasks: SIGTERM via
// vmmd.SendSignal — the guest's job supervisor handles SIGTERM
// (30s grace), writes job_exit{exit_code=143, error_class=
// 'cancelled'}, then poweroff. Naturally idempotent (a second
// cancel returns the already-cancelled run with the same wire
// shape). Wire shape: JobRunCancelledResponse (run +
// cancelled_at).
func (c *Client) CancelJobRun(ctx context.Context, name, runID string) (JobRunCancelledResponse, error) {
	var out JobRunCancelledResponse
	return out, c.do(ctx, "POST", "/v1/jobs/"+name+"/runs/"+runID+"/cancel", nil, &out)
}

// ListJobRunTasks returns a page of the run's task rows
// (issue #1184 Workstream A). task_index 1..N (1-based; matches
// the server's CTE fan-out). Status is the closed-set {queued,
// claimed, succeeded, failed, timeout, oom, cancelled}. LeaseToken
// is intentionally OMITTED from the wire response (internal
// dispatch primitive).
func (c *Client) ListJobRunTasks(ctx context.Context, name, runID string) (ListJobTasksResponse, error) {
	var out ListJobTasksResponse
	return out, c.do(ctx, "GET", "/v1/jobs/"+name+"/runs/"+runID+"/tasks", nil, &out)
}

// GetJobTaskLogs tails the task's stdout/stderr via vmmd's tail
// endpoint (issue #1184 Workstream A). Wire shape:
// JobTaskLogResponse (task_status + log_content + truncated +
// max_bytes). Truncated=true means the tail was capped at
// MaxBytes; clients should re-fetch with a larger limit to see
// more. Empty LogContent with Truncated=false means the task
// never produced output (common for OOM-killed tasks).
func (c *Client) GetJobTaskLogs(ctx context.Context, name, runID string, taskIndex int) (JobTaskLogResponse, error) {
	var out JobTaskLogResponse
	return out, c.do(ctx, "GET", "/v1/jobs/"+name+"/runs/"+runID+"/tasks/"+strconv.Itoa(taskIndex)+"/logs", nil, &out)
}

// --- Triggers (issue #757 / ADR-100) ----------------------------------------
// Unified event-source-mapping primitive. Method names follow the
// convention pinned by cmd/sdk-coverage/main.go::methodRouteMap.

// GetTriggers lists every trigger owned by the calling account,
// optionally filtered by app_id and/or kind. Newest-first by
// created_at.
func (c *Client) GetTriggers(ctx context.Context, appID string, kind TriggerKind) ([]Trigger, error) {
	path := "/v1/triggers"
	q := url.Values{}
	if appID != "" {
		q.Set("app_id", appID)
	}
	if kind != "" {
		q.Set("kind", string(kind))
	}
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var out []Trigger
	return out, c.do(ctx, "GET", path, nil, &out)
}

// PostTriggers creates a new trigger. The Idempotency-Key header is
// auto-minted (TestDo_MutatingCallsCarryIdempotencyKey).
func (c *Client) PostTriggers(ctx context.Context, req CreateTriggerRequest) (Trigger, error) {
	var out Trigger
	return out, c.do(ctx, "POST", "/v1/triggers", req, &out)
}

// GetTriggersId returns one trigger.
func (c *Client) GetTriggersId(ctx context.Context, id string) (Trigger, error) {
	var out Trigger
	return out, c.do(ctx, "GET", "/v1/triggers/"+id, nil, &out)
}

// PatchTriggersId is a partial update.
func (c *Client) PatchTriggersId(ctx context.Context, id string, req UpdateTriggerRequest) (Trigger, error) {
	var out Trigger
	return out, c.do(ctx, "PATCH", "/v1/triggers/"+id, req, &out)
}

// DeleteTriggersId removes the trigger; ON DELETE CASCADE drops the
// trigger_records and trigger_dead_letter rows.
func (c *Client) DeleteTriggersId(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/triggers/"+id, nil, nil)
}

// PostTriggersIdPause sets enabled=false and emits trigger_changed pg_notify.
func (c *Client) PostTriggersIdPause(ctx context.Context, id string) error {
	return c.do(ctx, "POST", "/v1/triggers/"+id+"/pause", nil, nil)
}

// PostTriggersIdResume sets enabled=true and emits trigger_changed pg_notify.
func (c *Client) PostTriggersIdResume(ctx context.Context, id string) error {
	return c.do(ctx, "POST", "/v1/triggers/"+id+"/resume", nil, nil)
}

// GetTriggersIdRecords returns records for one trigger.
func (c *Client) GetTriggersIdRecords(ctx context.Context, id, state string) (ListTriggerRecordsResponse, error) {
	path := "/v1/triggers/" + id + "/records"
	if state != "" {
		q := url.Values{}
		q.Set("state", state)
		path += "?" + q.Encode()
	}
	var out ListTriggerRecordsResponse
	return out, c.do(ctx, "GET", path, nil, &out)
}

// PostTriggersIdRecordsRidRetry moves a single record from retry/
// dead_letter back to pending. Operator-only scope on the server.
func (c *Client) PostTriggersIdRecordsRidRetry(ctx context.Context, id, recordID string) error {
	return c.do(ctx, "POST", "/v1/triggers/"+id+"/records/"+recordID+"/retry", nil, nil)
}

// PostTriggersIdRecordsRidDrop marks a dead-letter row routed_to=drop
// (already the default; this is the explicit acknowledgement).
func (c *Client) PostTriggersIdRecordsRidDrop(ctx context.Context, id, recordID string) error {
	return c.do(ctx, "POST", "/v1/triggers/"+id+"/records/"+recordID+"/drop", nil, nil)
}

// GetTriggersIdDlq returns rows from trigger_dead_letter for the trigger.
func (c *Client) GetTriggersIdDlq(ctx context.Context, id, reason string) (ListTriggerDeadLetterResponse, error) {
	path := "/v1/triggers/" + id + "/dlq"
	if reason != "" {
		q := url.Values{}
		q.Set("reason", reason)
		path += "?" + q.Encode()
	}
	var out ListTriggerDeadLetterResponse
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetTriggersIdMetrics returns the per-state count roll-up. Not the
// Prometheus surface; /v1/metrics is.
func (c *Client) GetTriggersIdMetrics(ctx context.Context, id string) (TriggerMetricsResponse, error) {
	var out TriggerMetricsResponse
	return out, c.do(ctx, "GET", "/v1/triggers/"+id+"/metrics", nil, &out)
}

// PostInvocationsDispatchBatch is the internal route schedd uses to
// post a closed batch envelope (size / window / 6MB cap). The function
// under the trigger responds with ReportBatchItemFailures verbatim.
func (c *Client) PostInvocationsDispatchBatch(ctx context.Context, body map[string]any) error {
	return c.do(ctx, "POST", "/v1/invocations:dispatch_batch", body, nil)
}

// PostTriggersBatchCreate applies a gregale.yaml triggers fragment in
// one transaction (dashboard-only shortcut).
func (c *Client) PostTriggersBatchCreate(ctx context.Context, req CreateTriggerBatchRequest) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, "POST", "/v1/triggers:batch_create", req, &out)
}

// FireCron manually triggers a cron fire-now (issue #791 PR-C /
// ADR-090). The endpoint is asynchronous: apid inserts a pending row
// into cron_fire_now_requests and emits db.NotifyCronRunNow; schedd
// picks it up and stamps the terminal status. FireCron returns the
// 202 response immediately with the request id — callers that need
// the terminal status poll GET /v1/crons/{id}/runs (PR-A's surface)
// for the matching `cron.fired.manually` audit row, or watch the
// future GET /v1/cron-fire-now-requests/{id} endpoint.
//
// The idempotency-key auto-mint (TestDo_MutatingCallsCarryIdempotencyKey
// in client_test.go) covers this call — replays with the same key
// return the stored 202 without enqueuing a second fire.
func (c *Client) FireCron(ctx context.Context, id string) (FireCronResponse, error) {
	var out FireCronResponse
	return out, c.do(ctx, "POST", "/v1/crons/"+id+"/run", nil, &out)
}

// GetFireCronRequest is the polling surface for the cron fire-now
// read shape (issue #791 PR-D / ADR-090 §Sub-decision 7). Pairs
// with FireCron: clients POST to enqueue, then GET this endpoint
// with the returned request_id until Status reaches a terminal
// value. Read-side scope (ScopesReadSurface) — does NOT require
// deploy:write.
func (c *Client) GetFireCronRequest(ctx context.Context, requestID string) (FireCronRequestResponse, error) {
	var out FireCronRequestResponse
	return out, c.do(ctx, "GET", "/v1/cron-fire-now-requests/"+requestID, nil, &out)
}

// ListCronRuns returns a page of the cron's execution history (issue
// #791 / PR A): newest-first, server-computed duration_ms, outcome
// classification (success/failed/timeout/dead_letter/running).
// before is the LAST run id of the previous page; omit for the most
// recent page. limit is 1..100, default 10 — the handler validates
// and surfaces a 400 Problem with limit + observed on garbage input
// (matches the canonical parseCronRunsLimit helper). For a wider,
// cross-source view use ListInvocations.
func (c *Client) ListCronRuns(ctx context.Context, id, before string, limit int) (ListCronRunsResponse, error) {
	path := "/v1/crons/" + id + "/runs"
	q := url.Values{}
	if before != "" {
		q.Set("before", before)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out ListCronRunsResponse
	return out, c.do(ctx, "GET", path, nil, &out)
}

// --- Alert rules (issue #396 / ADR-045 PR 3) -------------------------------

// ListAlertRules returns every alert rule visible at the given app
// (both app-scoped and account-wide rules — design decision recorded
// in the PR 3 plan). Free plans get 402 plan_alert_rules_not_allowed;
// per-account / per-app cap trips return 403 plan_alert_rule_quota.
func (c *Client) ListAlertRules(ctx context.Context, slug string) ([]AlertRuleResponse, error) {
	var out []AlertRuleResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/alerts", nil, &out)
}

// CreateAlertRule pins a metric to a threshold + comparison + window
// and seals the plaintext webhook_secret. The response carries the
// masked constant ("***") in webhook_secret_sealed_masked; the
// plaintext is never echoed. SSRF block (loopback / metadata) returns
// 403 image_egress_denied.
func (c *Client) CreateAlertRule(ctx context.Context, slug string, req CreateAlertRuleRequest) (AlertRuleResponse, error) {
	var out AlertRuleResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/alerts", req, &out)
}

// GetAlertRule fetches one rule by id. IDOR-safe: a foreign account's
// rule id returns 404, not 403.
func (c *Client) GetAlertRule(ctx context.Context, slug, id string) (AlertRuleResponse, error) {
	var out AlertRuleResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/alerts/"+id, nil, &out)
}

// UpdateAlertRule applies a partial update. Pointer-everything
// optionals let the caller distinguish "omitted" from "explicit zero".
// Metric-family swaps (e.g. error_rate_pct → failed_invocations) are
// rejected with 400 alert_rule_invalid; the caller must delete +
// recreate.
func (c *Client) UpdateAlertRule(ctx context.Context, slug, id string, req UpdateAlertRuleRequest) (AlertRuleResponse, error) {
	var out AlertRuleResponse
	return out, c.do(ctx, "PATCH", "/v1/apps/"+slug+"/alerts/"+id, req, &out)
}

// DeleteAlertRule removes the rule and returns nil on 204.
func (c *Client) DeleteAlertRule(ctx context.Context, slug, id string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/alerts/"+id, nil, nil)
}

// RotateAlertRuleSecret server-mints a fresh 32-byte HMAC secret and
// overwrites the row's sealed ciphertext in place. The plaintext is
// NEVER returned in the response — only the masked constant + a
// rotated_at timestamp. The customer must capture the new secret via
// out-of-band mechanism if they need it on the receiving end; PR 4's
// dashboard adds a one-time-display UX.
func (c *Client) RotateAlertRuleSecret(ctx context.Context, slug, id string) (RotateAlertRuleSecretResponse, error) {
	var out RotateAlertRuleSecretResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/alerts/"+id+"/rotate-secret", nil, &out)
}

// ListAlertRuleDeliveries returns the most-recent alert_deliveries
// rows for one alert rule (ADR-123 PR-D), newest-first. The default
// (includeTest=false) hides rows written by Dispatcher.DispatchTest
// ("send test alert" clicks); flipping includeTest=true surfaces the
// test rows so an operator can verify the customer's webhook is
// wired correctly without polluting the production pane. limit
// clamps to 100 server-side; the SDK passes through unchanged. The
// 404 posture matches GetAlertRule (IDOR-safe — a foreign account's
// rule id is indistinguishable from a missing one).
//
// Closed-set vocabulary: each row's Status is one of
// {pending, delivered, failed}; IsTest is the PR-D discriminator.
func (c *Client) ListAlertRuleDeliveries(ctx context.Context, slug, id string, includeTest bool, limit int) ([]AlertDeliveryResponse, error) {
	var out []AlertDeliveryResponse
	q := url.Values{}
	if includeTest {
		q.Set("include_test", "true")
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/apps/" + slug + "/alerts/" + id + "/deliveries"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// --- Alert presets (ADR-123 / issue #1233) -------------------------------

// ListAlertPresets returns the 8-row alert-preset catalog (issue
// #1233, ADR-123). The catalog is small enough that no pagination
// is needed — the SDK returns the flat slice verbatim. Disabled
// rows are returned with enabled_in_catalog=false (so the CLI
// renders them as "coming soon") and below-minimum-plan rows are
// returned with their minimum_plan field intact (so the CLI /
// dashboard can render an "upgrade to <plan>" hint per row).
func (c *Client) ListAlertPresets(ctx context.Context) ([]AlertPresetResponse, error) {
	var out []AlertPresetResponse
	return out, c.do(ctx, "GET", "/v1/alert-presets", nil, &out)
}

// EnableAlertPreset instantiates a catalog row as a real
// alert_rules row. The (metric, comparison, threshold, window_spec,
// default_cooldown_minutes) quadruple is pre-filled server-side;
// the caller supplies webhook_url + webhook_secret (the delivery
// channel). CooldownMinutes and Enabled are optional overrides
// (catalog defaults win when omitted). Returns 201 with the
// instantiated AlertRuleResponse so the dashboard renders the new
// rule alongside hand-rolled ones.
//
// Plan-tier gate: if the caller's plan is below the preset's
// minimum_plan, the server returns 402
// plan_alert_presets_not_allowed. The SDK surfaces the error
// verbatim; the CLI prints the message and exits 1.
func (c *Client) EnableAlertPreset(ctx context.Context, slug, presetName string, req EnableAlertPresetRequest) (AlertRuleResponse, error) {
	var out AlertRuleResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/alert-presets/"+presetName+"/enable", req, &out)
}

// TestAlertPreset test-fires the customer's instantiated alert
// preset against the webhook URL they configured at enable time
// (issue #1233 / ADR-123 PR-C commit 2). The synthetic event body
// carries payload.test=true so the customer's receiver can branch
// on the discriminator and skip production alert paths (e.g.
// PagerDuty incidents). Returns 200 with the TestAlertPresetResponse
// on success — Status, Test, DeliveryID, Attempts — or 502 when
// the dispatcher's retry budget is exhausted.
//
// Idempotency-Key is intentionally NOT required here: every POST
// mints a fresh 32-char-hex delivery_id, so replay-safety is
// guaranteed per-call rather than via the SDK dedup table. A
// retried POST by the customer is a fresh dispatch attempt, which
// is the right semantic for "is my webhook working?" — the
// customer wants to see the receiver handle the test again.
func (c *Client) TestAlertPreset(ctx context.Context, slug, presetName string) (TestAlertPresetResponse, error) {
	var out TestAlertPresetResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/alert-presets/"+presetName+"/test", nil, &out)
}

// --- Edge rules (ADR-089, planned) ----------------------------------------

// ListEdgeRules returns every edge rule owned by the authenticated
// account across all apps. The dashboard uses this for the "Edge
// Rules" overview pane; the CLI uses it for `gregale edge-rules list`.
// Empty account → empty slice (NOT an error). Free plans only see
// rule kinds their plan unlocks — the server still lists them.
func (c *Client) ListEdgeRules(ctx context.Context) ([]EdgeRuleResponse, error) {
	var out []EdgeRuleResponse
	return out, c.do(ctx, "GET", "/v1/edge-rules", nil, &out)
}

// ListEdgeRulesForApp returns every edge rule bound to one app,
// ordered by priority ASC (gateway match order). Empty list when the
// app has no rules.
func (c *Client) ListEdgeRulesForApp(ctx context.Context, slug string) ([]EdgeRuleResponse, error) {
	var out []EdgeRuleResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/edge-rules", nil, &out)
}

// CreateEdgeRule attaches a new rule to the slug's app. Plan-kind
// gate (jwt|ip → 402 plan_edge_rule_kind_not_allowed) and per-app
// quota (402 plan_limit_edge_rules) surface on this call. Action is
// a kind-tagged json.RawMessage — the SDK doesn't constrain which
// kinds pair with which shapes; that's the server's job. The
// response is the row the gateway matcher will see.
func (c *Client) CreateEdgeRule(ctx context.Context, slug string, req CreateEdgeRuleRequest) (EdgeRuleResponse, error) {
	var out EdgeRuleResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/edge-rules", req, &out)
}

// GetEdgeRule fetches one rule by id. IDOR-safe: a foreign account's
// rule id returns 404, not 403 — same convention as GetAlertRule.
func (c *Client) GetEdgeRule(ctx context.Context, id string) (EdgeRuleResponse, error) {
	var out EdgeRuleResponse
	return out, c.do(ctx, "GET", "/v1/edge-rules/"+id, nil, &out)
}

// UpdateEdgeRule applies a partial update. Pointer-everything
// optionals mirror UpdateAlertRule. Kind is NOT patchable (rotating
// kind mid-life would break the action union); the customer must
// delete + recreate. Action is *json.RawMessage — nil leaves the
// existing jsonb column untouched; non-nil replaces it whole.
func (c *Client) UpdateEdgeRule(ctx context.Context, id string, req UpdateEdgeRuleRequest) (EdgeRuleResponse, error) {
	var out EdgeRuleResponse
	return out, c.do(ctx, "PATCH", "/v1/edge-rules/"+id, req, &out)
}

// DeleteEdgeRule removes the rule and returns nil on 204.
func (c *Client) DeleteEdgeRule(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/edge-rules/"+id, nil, nil)
}

// CreateCORSEdgeRuleOpts is the typed CORS convenience shape used by
// CreateCORSEdgeRule (CORS improvements D5). Every field maps 1:1 to
// an EdgeRuleCORSAction field; the helper below packs them into a
// CreateEdgeRuleRequest with Kind="cors" so the customer-side SDK
// surface doesn't expose the kind/action union. Node + Python SDKs
// get the same shape via `make sdk-gen` (they read the kebab-style
// POST directly from OpenAPI — no parallel typed helper there).
//
// Priority defaults to 100 when zero (the gateway's middle bucket,
// matching the validator-side default on CreateEdgeRuleRequest).
// MaxAgeSeconds defaults to 600 (10 min — within the 24h cap
// EdgeRuleCORSAction.Validate enforces server-side). AllowOrigins
// must be non-empty; passing an empty slice returns an error before
// the HTTP round-trip so the customer gets a synchronous signal
// rather than a 422.
type CreateCORSEdgeRuleOpts struct {
	MatchHost        string
	MatchPath        string
	MatchMethods     []string
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	ExposeHeaders    []string
	AllowCredentials bool
	MaxAgeSeconds    int
}

// CreateCORSEdgeRule attaches a CORS-kind edge rule to the slug's
// app (CORS improvements D5). It is a thin wrapper around
// CreateEdgeRule that builds the EdgeRuleCORSAction JSON payload
// and pins the kind to "cors" so callers don't have to assemble
// the action blob themselves. Plan gate (402 plan_edge_rule_kind_not_allowed)
// and per-app quota (402 plan_limit_edge_rules) still surface
// from the underlying CreateEdgeRule — the helper adds zero
// behaviour beyond action-blob construction.
func (c *Client) CreateCORSEdgeRule(ctx context.Context, slug string, opts CreateCORSEdgeRuleOpts) (EdgeRuleResponse, error) {
	if len(opts.AllowOrigins) == 0 {
		var zero EdgeRuleResponse
		return zero, errors.New("CreateCORSEdgeRule: AllowOrigins must be non-empty")
	}
	if opts.MatchHost == "" {
		var zero EdgeRuleResponse
		return zero, errors.New("CreateCORSEdgeRule: MatchHost is required (use the app's primary domain)")
	}
	priority := 100
	action := EdgeRuleCORSAction{
		AllowOrigins:     opts.AllowOrigins,
		AllowMethods:     opts.AllowMethods,
		AllowHeaders:     opts.AllowHeaders,
		ExposeHeaders:    opts.ExposeHeaders,
		AllowCredentials: opts.AllowCredentials,
		MaxAgeSeconds:    opts.MaxAgeSeconds,
	}
	if action.MaxAgeSeconds == 0 {
		action.MaxAgeSeconds = 600
	}
	raw, err := json.Marshal(action)
	if err != nil {
		var zero EdgeRuleResponse
		return zero, fmt.Errorf("CreateCORSEdgeRule: marshal action: %w", err)
	}
	req := CreateEdgeRuleRequest{
		MatchHost:    opts.MatchHost,
		MatchPath:    opts.MatchPath,
		MatchMethods: opts.MatchMethods,
		Priority:     &priority,
		Kind:         "cors",
		Action:       raw,
	}
	var out EdgeRuleResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/edge-rules", req, &out)
}

// --- Event-driven surface (Move 2) -----------------------------------------
//
// The 10 routes exposed under /v1/apps/{slug}/invoke[/async],
// /v1/apps/{slug}/queues/{send,receive,{id}/ack},
// /v1/apps/{slug}/delayed-tasks, /v1/delayed-tasks/{id}, and
// /v1/invocations[/{id}]. Names follow the spec's natural verb, not
// the route path — see cmd/sdk-coverage/main.go::methodRouteMap for
// the explicit rename table.

// InvokeApp synchronously invokes an app and long-polls for the
// result. Timeout is bounded by the server (5s on Free, 30s on paid
// plans); the call returns 504 long_poll_timeout if the cap elapses
// before the row reaches a terminal state.
func (c *Client) InvokeApp(ctx context.Context, slug string, req InvokeRequest) (InvokeResponse, error) {
	var out InvokeResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/invoke", req, &out)
}

// InvokeAppAsync enqueues the invocation and returns 202 + id + the
// status URL. The drain picks the row up on the next 1s tick.
func (c *Client) InvokeAppAsync(ctx context.Context, slug string, req InvokeRequest) (AsyncInvokeResponse, error) {
	var out AsyncInvokeResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/invoke/async", req, &out)
}

// QueueSend enqueues a payload on the per-app FIFO queue. Cap-checked
// against the plan's MaxQueueDepth at the handler.
func (c *Client) QueueSend(ctx context.Context, slug string, req QueueSendRequest) (QueueSendResponse, error) {
	var out QueueSendResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/queues/send", req, &out)
}

// QueueReceive long-polls for the next dispatched row on the queue.
// 30s server-side cap; on timeout returns (zero, ErrLongPollTimeout)
// — caller is expected to retry. Stays open across the app's
// dispatched rows until one lands or the cap elapses.
func (c *Client) QueueReceive(ctx context.Context, slug string) (QueueReceiveResponse, error) {
	var out QueueReceiveResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/queues/receive", nil, &out)
}

// AckQueueRow is a no-op state change (the row is already completed
// when invocation_done fires) — idempotent; a re-ack returns 204.
func (c *Client) AckQueueRow(ctx context.Context, slug, id string) error {
	return c.do(ctx, "POST", "/v1/apps/"+slug+"/queues/"+id+"/ack", nil, nil)
}

// QueueState returns depth / in-flight / oldest-pending stats for the
// app's queue. Read-only: no lease is acquired, no row is mutated.
// PlanCap is MaxQueueDepth for the app's plan so dashboards can render
// "depth / cap" without a second plan lookup.
func (c *Client) QueueState(ctx context.Context, slug string) (QueueStateResponse, error) {
	var out QueueStateResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/queues/state", nil, &out)
}

// QueuePeek lists up to `limit` pending messages on the app's queue
// without acquiring a lease or incrementing attempts. Repeated calls
// return the same rows in the same order (the server guarantee is
// "byte-identical" — no SQL state changes). Pass `before` (the id
// returned as NextBefore in the previous page) to paginate; empty
// NextBefore means "no more pages".
func (c *Client) QueuePeek(ctx context.Context, slug string, limit int, before string) (QueuePeekResponse, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if before != "" {
		q.Set("before", before)
	}
	path := "/v1/apps/" + slug + "/queues/peek"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out QueuePeekResponse
	return out, c.do(ctx, "GET", path, nil, &out)
}

// QueueDeadLetter lists messages that exhausted the plan's retry
// budget (state='dead_letter'). Read-only: no lease, no mutation.
// Ordered newest-first; same cursor convention as QueuePeek.
func (c *Client) QueueDeadLetter(ctx context.Context, slug string, limit int, before string) (QueueDeadLetterResponse, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if before != "" {
		q.Set("before", before)
	}
	path := "/v1/apps/" + slug + "/queues/dead_letter"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out QueueDeadLetterResponse
	return out, c.do(ctx, "GET", path, nil, &out)
}

// CreateDelayedTask schedules a delayed-task row to fire at the
// given future timestamp. Cap-checked against MaxDelayedTasksPerApp.
func (c *Client) CreateDelayedTask(ctx context.Context, slug string, req DelayedTaskRequest) (DelayedTaskResponse, error) {
	var out DelayedTaskResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/delayed-tasks", req, &out)
}

// GetDelayedTask returns a single delayed-task by id. Account-scoped
// — cross-account reads surface 404, not 200 with a foreign row.
func (c *Client) GetDelayedTask(ctx context.Context, id string) (DelayedTaskResponse, error) {
	var out DelayedTaskResponse
	return out, c.do(ctx, "GET", "/v1/delayed-tasks/"+id, nil, &out)
}

// CancelDelayedTask cancels a pending delayed-task. Idempotent — a
// re-cancel on a terminal row returns 404 invocation_not_found.
func (c *Client) CancelDelayedTask(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/delayed-tasks/"+id, nil, nil)
}

// ListInvocations paginates the account's invocations by `?before=<id>`
// (the LAST id of the returned slice). Defaults to 20 per page.
func (c *Client) ListInvocations(ctx context.Context, before string, limit int) (ListInvocationsResponse, error) {
	var out ListInvocationsResponse
	q := url.Values{}
	if before != "" {
		q.Set("before", before)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/invocations"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetInvocation returns a single invocation by id. Account-scoped.
func (c *Client) GetInvocation(ctx context.Context, id string) (Invocation, error) {
	var out Invocation
	return out, c.do(ctx, "GET", "/v1/invocations/"+id, nil, &out)
}

// ReplayInvocation re-issues a failed invocation. The server
// enqueues a fresh async invocation carrying the original payload,
// headers, method, and path; returns 202 + AsyncInvokeResponse on
// success and 409 if the original is not in a replayable state (the
// handler's allow-list is {failed, dead_letter} — see
// cmd/apid/handlers_invocations.go::replayInvocation for the source
// of truth, issue #315 tier-2 DX).
//
// Account-scoped: a customer can't replay another tenant's
// invocation; the server surfaces ErrInvocationNotFound in that
// case (same IDOR-safe path as GetInvocation).
func (c *Client) ReplayInvocation(ctx context.Context, id string) (AsyncInvokeResponse, error) {
	var out AsyncInvokeResponse
	return out, c.do(ctx, "POST", "/v1/invocations/"+id+"/replay", nil, &out)
}

// QueueDeadLetterReplay resets a dead-letter queue row back to
// 'pending' with attempts=0 so the drain picks it up again. ADR-134
// PR-C closes the previously-missing queue DLQ replay path —
// distinct from ReplayInvocation (which enqueues a NEW row tagged
// Source=InvocationReplay). This endpoint mutates the row in place
// so the dashboard's replay history view can track the chain on a
// single row id.
func (c *Client) QueueDeadLetterReplay(ctx context.Context, slug, id string) (AsyncInvokeResponse, error) {
	var out AsyncInvokeResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/queues/dead_letter/"+id+"/replay", nil, &out)
}

// API keys.
//
// CreateKey accepts an explicit scopes slice. Pass nil to preserve the
// historical "full access" behavior (the server defaults nil to
// ["admin"]). See ADR-034 for the scope vocabulary.
func (c *Client) ListKeys(ctx context.Context) ([]APIKeyResponse, error) {
	var out []APIKeyResponse
	return out, c.do(ctx, "GET", "/v1/keys", nil, &out)
}
func (c *Client) CreateKey(ctx context.Context, label string, scopes []string) (APIKeyResponse, error) {
	var out APIKeyResponse
	return out, c.do(ctx, "POST", "/v1/keys", CreateKeyRequest{Label: label, Scopes: scopes}, &out)
}
func (c *Client) DeleteKey(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/keys/"+id, nil, nil)
}

// RotateKey mints a new key and demotes the old key in a single
// transaction (issue #189 / IAM-5). The new plaintext is returned
// exactly once; the old plaintext is never re-issued.
//
// OldKey.ExpiresAt is the GRACE deadline applied to the old key
// (set to now() when grace_window_days=0, atomic rotation). The
// customer's CI captures the new plaintext at rotation time and
// rolls over before old_key_expires_at.
func (c *Client) RotateKey(ctx context.Context, id string) (RotateKeyResponse, error) {
	var out RotateKeyResponse
	return out, c.do(ctx, "POST", "/v1/keys/"+id+"/rotate", nil, &out)
}

// GetGraceWindow returns the customer's per-account rotation
// grace-window override (issue #189 / IAM-5). days=null means
// "no override" — the rotation handler uses the plan default.
func (c *Client) GetGraceWindow(ctx context.Context) (GraceWindowResponse, error) {
	var out GraceWindowResponse
	return out, c.do(ctx, "GET", "/v1/account/keys/grace_window_days", nil, &out)
}

// SetGraceWindow writes the per-account rotation grace-window
// override. days=0 means atomic rotation; days=nil clears the
// override and falls back to the plan default.
func (c *Client) SetGraceWindow(ctx context.Context, days *int) (GraceWindowResponse, error) {
	var out GraceWindowResponse
	return out, c.do(ctx, "PATCH", "/v1/account/keys/grace_window_days", SetGraceWindowRequest{Days: days}, &out)
}

// PR 6 (issue #190 / IAM-6 / ADR-061) — org-scoped API key surface.
// Every method takes the org slug as the first argument; the wire
// paths under /v1/orgs/{slug}/keys/* are the canonical replacement
// for the legacy /v1/keys/* paths (which stamp org_id = caller's
// personal org but otherwise stay working through PR 9).
//
// The org-scoped and account-scoped surfaces emit the same wire
// shape (APIKeyResponse), so swapping the SDK client call is the
// only change a downstream consumer makes.

// ListOrgAPIKeys returns every API key the org owns, including
// rotated/revoked rows. The active org must be set via the X-Active-Org
// header (the client picks that up from the WithActiveOrg option or
// the request middleware). Returns the response wrapper that includes
// the total list and the per-row org_id.
func (c *Client) ListOrgAPIKeys(ctx context.Context, slug string) (ListOrgAPIKeysResponse, error) {
	var out ListOrgAPIKeysResponse
	return out, c.do(ctx, "GET", "/v1/orgs/"+slug+"/keys", nil, &out)
}

// CreateOrgAPIKey mints a new API key against the org. Returns the
// standard APIKeyResponse — plaintext is present on this single
// response and never returned again. `scopes` is optional; nil
// defaults to ["admin"] on the server side (mirrors CreateKey).
func (c *Client) CreateOrgAPIKey(ctx context.Context, slug, label string, scopes []string) (APIKeyResponse, error) {
	var out APIKeyResponse
	return out, c.do(ctx, "POST", "/v1/orgs/"+slug+"/keys", CreateOrgAPIKeyRequest{Label: label, Scopes: scopes}, &out)
}

// GetOrgAPIKey fetches a single key by id within the org. Cross-org
// probes collapse to 404 server-side (IDOR-safe; matches
// DeleteAppReturning).
func (c *Client) GetOrgAPIKey(ctx context.Context, slug, id string) (APIKeyResponse, error) {
	var out APIKeyResponse
	return out, c.do(ctx, "GET", "/v1/orgs/"+slug+"/keys/"+id, nil, &out)
}

// RevokeOrgAPIKey soft-revokes an API key within the org. Returns no
// body (204); subsequent bearer-auth attempts hit ErrAPIKeyRevoked.
func (c *Client) RevokeOrgAPIKey(ctx context.Context, slug, id string) error {
	return c.do(ctx, "DELETE", "/v1/orgs/"+slug+"/keys/"+id, nil, nil)
}

// RotateOrgAPIKey mints a new key and demotes the predecessor in
// one transaction (mirrors RotateKey semantics on the org-scoped
// path). The new key inherits the predecessor's label when `label`
// is empty. The grace window resolves from the per-account override
// (set via SetGraceWindow), with the api.DefaultAPIKeyGraceWindowDays
// fallback.
func (c *Client) RotateOrgAPIKey(ctx context.Context, slug, id, label string) (RotateOrgAPIKeyResponse, error) {
	var out RotateOrgAPIKeyResponse
	return out, c.do(ctx, "POST", "/v1/orgs/"+slug+"/keys/"+id+"/rotate",
		RotateOrgAPIKeyRequest{Label: label}, &out)
}

// Audit events (IAM-4, ADR-035). The events table is append-only
// (spec §5), so this surface is read-only by design. since and
// kindPrefix are optional — pass empty strings to read the full
// 50-row default window. limit is bounded server-side at 100; values
// larger are silently capped per the same convention as ListSecrets.

// ListWakeTimeline returns the typed wake-stage timeline for a given
// wake_id (issue #517 / PR-C / ADR-064). Oldest-first; the dashboard
// reads it as a forward narrative (queue_accepted → admitted →
// boot_started → boot_completed → readiness_200 → proxy_first_byte).
//
// since is an RFC 3339 timestamp; rows strictly older are skipped
// (the dashboard's "load older" infinite-scroll). limit is bounded
// server-side at 1000; values larger are silently capped per the
// same convention as ListSecrets / ListAuditEvents.
//
// Cross-account visibility is enforced server-side: the slug must
// resolve to the caller's account, and each row's data.app_id is
// forge-checked against the resolved app id. A row that mismatches
// is dropped silently so a malicious admin can't surface a
// foreign-tenant frame in this timeline.
func (c *Client) ListWakeTimeline(ctx context.Context, slug, wakeID, since string, limit int) (WakeTimelineResponse, error) {
	var out WakeTimelineResponse
	q := url.Values{}
	if since != "" {
		q.Set("since", since)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/apps/" + slug + "/wakes/" + wakeID + "/timeline"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// ListAuditEvents returns the caller's auth audit events newest-first.
// includeAnonymous (Wave 0 PR-C / ADR-047) toggles subject=NULL rows —
// the defensive case where the app row was deleted between wake and
// the stateless-advisory audit emit. Default false to match the
// /v1/audit-events route's customer-facing default. appID (Wave 0
// PR-C / ADR-047) filters the overscan window to events whose
// data.app_id matches — the dashboard's per-app drill-down.
func (c *Client) ListAuditEvents(ctx context.Context, since, kindPrefix, appID string, limit int, includeAnonymous bool) (ListAuditEventsResponse, error) {
	var out ListAuditEventsResponse
	q := url.Values{}
	if since != "" {
		q.Set("since", since)
	}
	if kindPrefix != "" {
		q.Set("kind_prefix", kindPrefix)
	}
	if appID != "" {
		q.Set("app_id", appID)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if includeAnonymous {
		q.Set("include_anonymous", "true")
	}
	path := "/v1/audit-events"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetAuditEvent fetches a single auth audit event by id. Cross-account
// lookups 404 the same way unknown ids do, so a caller cannot enumerate
// other accounts' row counts by id-probing.
func (c *Client) GetAuditEvent(ctx context.Context, id string) (AuditEventResponse, error) {
	var out AuditEventResponse
	return out, c.do(ctx, "GET", "/v1/audit-events/"+id, nil, &out)
}

// ListAuditLog returns the caller's audit-log entries newest-first
// (issue #755 / PR-6). Reads the FK-free `audit_log` table
// (migrations/00163_audit_log.sql), distinct from ListAuditEvents
// which reads the live `events` table. Customer-scoped: pinned to
// the calling account's id inside the handler; `account_id IS NULL`
// rows are filtered out server-side.
func (c *Client) ListAuditLog(ctx context.Context, since, kindPrefix string, limit int) (ListAuditLogResponse, error) {
	var out ListAuditLogResponse
	q := url.Values{}
	if since != "" {
		q.Set("since", since)
	}
	if kindPrefix != "" {
		q.Set("kind_prefix", kindPrefix)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/audit-log"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// ListAuditLogAll is the operator-side read of the audit-log
// surface (issue #755 / PR-6). Admin-scoped: reads across accounts
// when `accountID == ""` and surfaces `account_id IS NULL` rows
// when `includeAnonymous == true`. Gated server-side on the
// admin scope; the SDK caller must be holding an admin API key or
// an admin session.
func (c *Client) ListAuditLogAll(ctx context.Context, accountID, since, kindPrefix string, limit int, includeAnonymous bool) (ListAuditLogResponse, error) {
	var out ListAuditLogResponse
	q := url.Values{}
	if accountID != "" {
		q.Set("account_id", accountID)
	}
	if since != "" {
		q.Set("since", since)
	}
	if kindPrefix != "" {
		q.Set("kind_prefix", kindPrefix)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if includeAnonymous {
		q.Set("include_anonymous", "true")
	}
	path := "/v1/audit-log/all"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// CLI auth device-code flow (spec §2.2).

// MintCliAuthCode anonymously mints a fresh device code.
func (c *Client) MintCliAuthCode(ctx context.Context) (CliAuthCodeResponse, error) {
	var out CliAuthCodeResponse
	return out, c.do(ctx, "POST", "/v1/cli-auth/code", struct{}{}, &out)
}

// ExchangeCliAuthCode polls the server for the user's approval.
func (c *Client) ExchangeCliAuthCode(ctx context.Context, code string) (CliAuthExchangeResponse, error) {
	var out CliAuthExchangeResponse
	return out, c.do(ctx, "POST", "/v1/cli-auth/exchange",
		CliAuthExchangeRequest{Code: code}, &out)
}

// Dashboard auth (issue #165, ADR-032 PR #2). The SDK uses these
// against a tokenless Client (NewClient returns one with token="");
// the auth flows issue a session cookie but the SDK does not consume
// it — the dashboard cookie is the only auth artifact on the browser
// side. Programmatic auth stays on the device-code flow above, where
// the customer can mint a real api_key via the dashboard after
// signing in.
//
// PasswordSignup creates an account (if the email is unbound) and
// signs the caller in. The same response shape as PasswordLogin:
// {account_id, plan}, no api_key. Anti-enumeration: a colliding
// signup attempt returns 401 invalid_credentials, not 409 — the
// SDK and the CLI render the same generic "sign in failed" copy.
func (c *Client) PasswordSignup(ctx context.Context, email, password string) (PasswordLoginResponse, error) {
	var out PasswordLoginResponse
	return out, c.do(ctx, "POST", "/signup",
		PasswordSignupRequest{Email: email, Password: password}, &out)
}

// PasswordLogin signs the caller in with email + password. The
// success response does NOT carry an API key — the session cookie is
// the only auth artifact. The SDK does not consume the cookie; the
// caller is expected to follow the 302 redirect or to exchange the
// session via the device-code flow for API access.
func (c *Client) PasswordLogin(ctx context.Context, email, password string) (PasswordLoginResponse, error) {
	var out PasswordLoginResponse
	return out, c.do(ctx, "POST", "/login",
		PasswordLoginRequest{Email: email, Password: password}, &out)
}

// PostAuthSignup creates an account (or signs in an existing one)
// and returns a freshly-minted api_key. Used by `gregale signup` on
// the interactive (password) path. The success body carries an
// api_key.Plaintext — the caller must persist it before returning.
//
// Distinct from PasswordSignup: this hits the JSON-only
// /v1/auth/signup route (no session cookie), and the response
// carries the api_key payload. Both /v1/auth/signup and
// /v1/auth/login return the same ProgrammaticAuthResponse shape.
//
// Method name follows deriveMethodName (cmd/sdk-coverage/main.go) —
// POST + auth/signup → PostAuthSignup. Auto-derived; no pin needed.
func (c *Client) PostAuthSignup(ctx context.Context, email, password string) (ProgrammaticAuthResponse, error) {
	var out ProgrammaticAuthResponse
	return out, c.do(ctx, "POST", "/v1/auth/signup",
		PasswordSignupRequest{Email: email, Password: password}, &out)
}

// PostAuthLogin signs the caller in via email + password and
// returns a freshly-issued api_key. Mirror of PostAuthSignup for the
// "I already have an account" CLI use case. Both endpoints share the
// same wire shape.
//
// Method name follows deriveMethodName — POST + auth/login →
// PostAuthLogin. Auto-derived; no pin needed.
func (c *Client) PostAuthLogin(ctx context.Context, email, password string) (ProgrammaticAuthResponse, error) {
	var out ProgrammaticAuthResponse
	return out, c.do(ctx, "POST", "/v1/auth/login",
		PasswordLoginRequest{Email: email, Password: password}, &out)
}

// PostAuthOidcExchange trades an IdP-issued JWT (e.g. the
// ACTIONS_ID_TOKEN_REQUEST_TOKEN from GitHub Actions) for a
// short-lived opaque bearer (5 min TTL, fp_oidc_ prefix). Used by
// the CI deploy path so customers can swap the long-lived
// FAAS_TOKEN secret for keyless OIDC. The wire contract is
// PR-A (ADR-101 / issue #270); PR-B adds the CLI flag + Action
// input that drives this call.
//
// Method name follows deriveMethodName — POST + auth/oidc/exchange →
// PostAuthOidcExchange. Auto-derived; no pin needed.
func (c *Client) PostAuthOidcExchange(ctx context.Context, req OIDCExchangeRequest) (OIDCExchangeResponse, error) {
	var out OIDCExchangeResponse
	return out, c.do(ctx, "POST", "/v1/auth/oidc/exchange", req, &out)
}

// PostAuthSignupMagicLink asks the server to email a one-time signup
// link. Always returns 200 with an identical body — the response is
// not a promise of email; the CLI prints "Check your email" and
// exits 0 regardless of whether the email is bound (anti-enumeration).
//
// Method name follows deriveMethodName — POST + auth/signup/magic-link
// → PostAuthSignupMagic-link (the dash survives because deriveMethodName
// only title-cases the first byte of each segment).
func (c *Client) PostAuthSignupMagicLink(ctx context.Context, email string) error {
	return c.do(ctx, "POST", "/v1/auth/signup/magic-link",
		MagicLinkSignupRequest{Email: email}, nil)
}

// RequestPasswordReset mints a password-reset email. The server
// always returns 200 with an identical body regardless of whether the
// email is bound to an account, so the surface does not leak account
// presence. The full reset URL is sent via the platform's mailer
// (recorded in mail_wiring_test.go); the SDK caller never sees the
// token.
func (c *Client) RequestPasswordReset(ctx context.Context, email string) error {
	return c.do(ctx, "POST", "/login/forgot",
		PasswordResetRequest{Email: email}, nil)
}

// ConfirmPasswordReset consumes a one-shot reset token and sets the
// new password. The token is the base64url-encoded value from the
// email link (NOT the SHA-256 hash the server stored). A replay
// (already-consumed token) returns 410 reset_token_invalid.
func (c *Client) ConfirmPasswordReset(ctx context.Context, token, newPassword string) error {
	return c.do(ctx, "POST", "/auth/reset",
		PasswordResetConfirm{Token: token, NewPassword: newPassword}, nil)
}

// SetPassword is retained for source compatibility, but the endpoint
// is dashboard-session-only and cannot be called by this bearer-key
// client. Use SetPasswordWithSession when the caller owns the
// dashboard session cookie and the matching CSRF token.
func (c *Client) SetPassword(ctx context.Context, password string) error {
	return c.do(ctx, "POST", "/dashboard/account/set-password",
		SetPasswordRequest{Password: password}, nil)
}

// SetPasswordWithSession updates the password through the
// dashboard-cookie form surface. sessionCookie is the opaque value
// of the faas_sid cookie; input.CSRFToken is sent both as the
// csrf_token form field and as the faas_csrf double-submit cookie.
// The endpoint returns 302 /dashboard/account/ on success, so this
// method accepts only that redirect and does not follow it. A 302 to
// /login (missing or invalid session) remains an error.
func (c *Client) SetPasswordWithSession(ctx context.Context, sessionCookie string, input SetPasswordRequest) error {
	form := url.Values{
		"password":   {input.Password},
		"csrf_token": {input.CSRFToken},
	}
	if input.CurrentPassword != "" {
		form.Set("current_password", input.CurrentPassword)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/dashboard/account/set-password", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Idempotency-Key", newUUIDv4())
	req.AddCookie(&http.Cookie{Name: "faas_sid", Value: sessionCookie})
	req.AddCookie(&http.Cookie{Name: "faas_csrf", Value: input.CSRFToken})

	noRedirect := *c.http
	noRedirect.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return c.doReqWithSuccess(&noRedirect, req, nil, func(resp *http.Response) bool {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return true
		}
		return resp.StatusCode == http.StatusFound && resp.Header.Get("Location") == "/dashboard/account/"
	})
}

// Logout clears the dashboard session. Idempotent — clearing a
// non-existent session is a no-op.
func (c *Client) Logout(ctx context.Context) error {
	return c.do(ctx, "POST", "/logout", nil, nil)
}

// Secrets (spec §11/G2). Plaintext VALUE never leaves the caller
// except via SetSecret's body.
//
// ADR-092 PR-B: every secrets helper gains an optional scope
// argument. Pass "" to write to / read from the default scope
// (same wire shape as pre-PR-B callers — pre-PR-B code paths
// are preserved by the scope="" branch). Pass a scope name to
// read or write a specific row; the client appends ?scope=<name>
// to the path. The pre-PR-B ListSecrets / SetSecret / UnsetSecret /
// RotateSecret stay as scope="" wrappers for backward-compat.
func (c *Client) ListSecrets(ctx context.Context, slug string) (AppSecretListResponse, error) {
	return c.ListSecretsWithScope(ctx, slug, "")
}

// ListSecretsWithScope is the scope-aware sibling of ListSecrets.
// scope="" reads from the default scope (flat `secrets` array);
// scope="__all__" returns the nested `secrets_by_scope` map
// (ADR-092, mirror of ADR-090 D3's env_by_scope).
func (c *Client) ListSecretsWithScope(ctx context.Context, slug, scope string) (AppSecretListResponse, error) {
	var out AppSecretListResponse
	return out, c.do(ctx, "GET", c.scopeQuery("/v1/apps/"+slug+"/secrets", scope), nil, &out)
}

// GetAppEnvDiff returns the (rows × scopes) matrix of env
// vars + secrets for an app (ADR-117 PR-C). The matrix is
// always full — there is no `?scope=` filter in v1. Secret
// cells never carry plaintext on the wire (the EnvDiffCell
// DTO's omitempty on Value for secret cells is the
// load-bearing security property; the renderer in
// cmd/gregale/commands_env_diff.go::renderEnvDiffCell is
// the other half of the same property).
func (c *Client) GetAppEnvDiff(ctx context.Context, slug string) (EnvDiffResponse, error) {
	var out EnvDiffResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/env-diff", nil, &out)
}

// GetSecrets returns every sealed secret across the caller's account
// (issue #393). One call replaces N per-app ListSecrets calls. Each
// row carries app_id and app_slug so the dashboard renders
// "foo-app / DATABASE_URL" without a parallel /v1/apps lookup.
// Ciphertext is the age-sealed envelope (base64); plaintext VALUE
// is never on the wire (same invariant as ListSecrets). Cursor is
// the (app_slug, key) pair, encoded as "<slug>|<key>" — see
// ADR-045.
func (c *Client) GetSecrets(ctx context.Context, before string, limit int) (ListSecretsForAccountResponse, error) {
	var out ListSecretsForAccountResponse
	path := "/v1/secrets"
	if before != "" || limit > 0 {
		path += "?"
		if before != "" {
			path += "before=" + url.QueryEscape(before)
		}
		if limit > 0 {
			if before != "" {
				path += "&"
			}
			path += "limit=" + strconv.Itoa(limit)
		}
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}
func (c *Client) SetSecret(ctx context.Context, slug, key, value string) error {
	return c.SetSecretWithScope(ctx, slug, key, value, "")
}

// SetSecretWithScope is the scope-aware sibling of SetSecret. The
// reserved sentinel "__all__" is rejected by the server with 400
// env_scope_reserved; the client doesn't pre-validate so the
// error envelope reaches the caller verbatim.
func (c *Client) SetSecretWithScope(ctx context.Context, slug, key, value, scope string) error {
	return c.do(ctx, "PUT", c.scopeQuery("/v1/apps/"+slug+"/secrets/"+key, scope),
		PutAppSecretRequest{Value: value}, nil)
}
func (c *Client) UnsetSecret(ctx context.Context, slug, key string) error {
	return c.UnsetSecretWithScope(ctx, slug, key, "")
}

// UnsetSecretWithScope is the scope-aware sibling of UnsetSecret.
// Same reserved-sentinel posture as SetSecretWithScope.
func (c *Client) UnsetSecretWithScope(ctx context.Context, slug, key, scope string) error {
	return c.do(ctx, "DELETE", c.scopeQuery("/v1/apps/"+slug+"/secrets/"+key, scope), nil, nil)
}

// RotateSecret (ADR-089 PR-B) re-seals the (slug, key) row under
// the current host identity. Distinct verb from SetSecret so the
// server can emit the secret.rotated audit kind (vs secret.set).
// Returns the RotateAppSecretResponse so the CLI can render the
// rotated_at timestamp and the kid.
func (c *Client) RotateSecret(ctx context.Context, slug, key, value string) (RotateAppSecretResponse, error) {
	return c.RotateSecretWithScope(ctx, slug, key, value, "")
}

// RotateSecretWithScope is the scope-aware sibling of RotateSecret.
// Same reserved-sentinel posture as SetSecretWithScope.
func (c *Client) RotateSecretWithScope(ctx context.Context, slug, key, value, scope string) (RotateAppSecretResponse, error) {
	var out RotateAppSecretResponse
	return out, c.do(ctx, "POST",
		c.scopeQuery("/v1/apps/"+slug+"/secrets/"+key+"/rotate", scope),
		RotateAppSecretRequest{Value: value}, &out)
}

// scopeQuery appends "?scope=<name>" to path when scope is
// non-empty. Empty scope returns the path unchanged (pre-PR-B
// callers see no behaviour change). url.Values.Encode handles
// percent-encoding of edge cases (e.g. a future scope name with a
// reserved char — not currently possible given the
// api.EnvScopePattern regex, but defensive).
func (c *Client) scopeQuery(path, scope string) string {
	if scope == "" {
		return path
	}
	v := url.Values{}
	v.Set("scope", scope)
	return path + "?" + v.Encode()
}

// Per-app private-registry Basic Auth (issue #461 / ADR-062). Password
// is sealed at rest server-side; the SDK only carries plaintext in the
// PUT body and never sees the ciphertext. Hosts MUST be supplied with
// an explicit "https://" prefix; apid rejects schemeless / http://
// inputs with 400 invalid_registry_host at normalizeRegistryHost.
//
// Method names follow the MethodResource convention the sdk-coverage
// gate expects (ListAppRegistryCredentials / SetAppRegistryCredential /
// DeleteAppRegistryCredential). See cmd/sdk-coverage/main.go::methodRouteMap
// for the pin table.
func (c *Client) ListAppRegistryCredentials(ctx context.Context, slug string) (AppRegistryCredentialListResponse, error) {
	var out AppRegistryCredentialListResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/registry-credentials", nil, &out)
}
func (c *Client) SetAppRegistryCredential(ctx context.Context, slug, registry, username, password string) (AppRegistryCredentialResponse, error) {
	var out AppRegistryCredentialResponse
	return out, c.do(ctx, "PUT", "/v1/apps/"+slug+"/registry-credentials",
		PutAppRegistryCredentialRequest{Registry: registry, Username: username, Password: password}, &out)
}
func (c *Client) DeleteAppRegistryCredential(ctx context.Context, slug, registry string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/registry-credentials?registry="+url.QueryEscape(registry), nil, nil)
}

// Env vars (issue #395 / ADR-045). Plaintext by contract — values are
// non-sensitive runtime config. Value never appears in the GET
// response; only the key set + timestamps do (AppEnvResponse shape).
// PutAppsSlugEnvKey's body is the value path; DeleteAppsSlugEnvKey is
// identity-only. Method names match the sdk-coverage gate's
// MethodResource convention so every spec route ships with a Go SDK
// method (the older secrets surface used helper-style names like
// ListSecrets/SetSecret/UnsetSecret which the gate tolerates as
// pre-existing helpers — new surfaces follow MethodResource).
func (c *Client) GetAppsSlugEnv(ctx context.Context, slug string) (AppEnvListResponse, error) {
	var out AppEnvListResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/env", nil, &out)
}
func (c *Client) PutAppsSlugEnvKey(ctx context.Context, slug, key, value string) error {
	return c.do(ctx, "PUT", "/v1/apps/"+slug+"/env/"+key,
		PutAppEnvRequest{Value: value}, nil)
}
func (c *Client) DeleteAppsSlugEnvKey(ctx context.Context, slug, key string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/env/"+key, nil, nil)
}

// Usage.
//
// GetUsage returns per-app usage rows for the given month — the wire
// shape is an ARRAY of UsageResponse objects, not a single struct.
// The OpenAPI spec (api/openapi.yaml GET /v1/usage), the server handler
// (cmd/apid/handlers_ext.go getUsage), the cross-language fixture
// (sdk/fakeapid/main.go), and the Node/Python SDKs all agree. This
// Go SDK is the sole outlier — see memory: getusage-wire-shape-mismatch.
// Empty month falls back to the server's default (current month).
func (c *Client) GetUsage(ctx context.Context, month string) ([]UsageResponse, error) {
	var out []UsageResponse
	path := "/v1/usage"
	if month != "" {
		q := url.Values{}
		q.Set("month", month)
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetAppMetrics returns the per-app metrics snapshot for slug over
// the named range window. rng is one of "5m", "15m", "1h", "6h",
// "24h", "7d", "15d" — empty falls back to the server's default
// (5m). Issue #273 / ADR-042.
func (c *Client) GetAppMetrics(ctx context.Context, slug, rng string) (AppMetricsResponse, error) {
	var out AppMetricsResponse
	path := "/v1/apps/" + slug + "/metrics"
	if rng != "" {
		q := url.Values{}
		q.Set("range", rng)
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// AppWakeTimelineOptions controls the optional query params for
// GetAppWakeTimeline. Since and Until are RFC3339Nano strings (NOT
// time.Time — the wire form is the canonical string so caller-side
// parse failures surface at the SDK boundary, not inside the
// transport). Empty values fall through to the server's defaults
// (24h trailing window). Per-app observability backend PR series.
type AppWakeTimelineOptions struct {
	Since string
	Until string
}

// GetAppWakeTimeline returns the wire-friendly mirror of the
// per-app dashboard wake-timeline HTML page
// (cmd/apid/handlers_dashboard.go renderAppWakeTimeline). The
// aggregation math (descending-cutoff break, two-denominator rule,
// em-dash policy) is shared with the HTML page via the cmd/apid
// helper; this method is the SDK entry point.
//
// opts.Since / opts.Until default to the server's trailing-24h
// window when empty. Plan-gated Hobby+ — Free gets 402
// plan_per_app_metrics_not_allowed (same code as GetAppMetrics so a
// plan downgrade flips both endpoints at once).
func (c *Client) GetAppWakeTimeline(ctx context.Context, slug string, opts AppWakeTimelineOptions) (AppWakeTimelineResponse, error) {
	var out AppWakeTimelineResponse
	path := "/v1/apps/" + slug + "/wake-timeline"
	q := url.Values{}
	if opts.Since != "" {
		q.Set("since", opts.Since)
	}
	if opts.Until != "" {
		q.Set("until", opts.Until)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// AppUsageSummaryOptions controls the optional query params for
// GetAppUsageSummary. Since and Until are RFC3339Nano strings; empty
// values fall through to the server's defaults (trailing-30d window
// ending at UTC midnight). Caller-side parse failures surface at the
// SDK boundary so the wire form stays canonical. Per-app
// observability backend PR series.
type AppUsageSummaryOptions struct {
	Since string
	Until string
}

// GetAppUsageSummary returns the per-app billing usage rollup for
// slug over the [since, until] window (default: trailing 30d,
// clamped at 90d upper bound). Plan-gated Hobby+ — Free gets 402
// plan_app_usage_summary_not_allowed. The handler computes
// overage_gb_hours as max(0, total_gb_hours - plan_included) so the
// dashboard's red overage chip has no second-roundtrip cost.
func (c *Client) GetAppUsageSummary(ctx context.Context, slug string, opts AppUsageSummaryOptions) (AppUsageSummaryResponse, error) {
	var out AppUsageSummaryResponse
	path := "/v1/apps/" + slug + "/usage"
	q := url.Values{}
	if opts.Since != "" {
		q.Set("since", opts.Since)
	}
	if opts.Until != "" {
		q.Set("until", opts.Until)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetAppThrottleSuggestions returns the per-route throttle
// recommendation payload for slug over the named range window
// (ADR-091 D20.5 amendment, issue #881). The recommender is
// read-only — it never auto-applies — and the suggestion is
// always ≤ the customer's plan ceiling so the customer can act
// on it without a 422 from apid's sub-plan validator.
//
// rng is the same closed vocabulary as GetAppMetrics; empty
// falls back to the server's default (5m). The response is
// HTTP 200 with empty Suggestions on Prometheus failure (the
// dashboard's empty-state branch handles it). RouteMetricsDisabled
// is true when apps.route_metrics_enabled=false (Free plan).
//
// Back-compat shim for callers that don't supply dry-run opts.
func (c *Client) GetAppThrottleSuggestions(ctx context.Context, slug, rng string) (ThrottleSuggestionsResponse, error) {
	return c.GetAppThrottleSuggestionsOpts(ctx, slug, rng, ThrottleSuggestionsOpts{})
}

// ThrottleSuggestionsOpts carries the dry-run preview knobs
// (ADR-104 amendment 5, issue #881 Phase 4 D2). Zero-value (the
// default) reproduces the Phase 1+2+3 wire shape — the server
// treats DryRun=false identically whether CandidateRPS/CandidateBurst
// are set or not.
type ThrottleSuggestionsOpts struct {
	DryRun         bool
	CandidateRPS   float64
	CandidateBurst int
}

// GetAppThrottleSuggestionsOpts returns the per-route throttle
// recommendation payload for slug over the named range window
// with dry-run preview support. When opts.DryRun is true and
// opts.CandidateRPS > 0 the server runs the per-route
// would-have-rejected pass and returns WouldHaveRejected +
// PerConsumerLimitNote on the response (per-Phase 4 D1 wire
// shape). When opts.DryRun is false the function is byte-identical
// to GetAppThrottleSuggestions (no extra query params emitted).
func (c *Client) GetAppThrottleSuggestionsOpts(ctx context.Context, slug, rng string, opts ThrottleSuggestionsOpts) (ThrottleSuggestionsResponse, error) {
	var out ThrottleSuggestionsResponse
	path := "/v1/apps/" + slug + "/throttle-suggestions"
	q := url.Values{}
	if rng != "" {
		q.Set("range", rng)
	}
	if opts.DryRun {
		q.Set("dry_run", "true")
		if opts.CandidateRPS > 0 {
			q.Set("candidate_rps", strconv.FormatFloat(opts.CandidateRPS, 'f', -1, 64))
		}
		if opts.CandidateBurst > 0 {
			q.Set("candidate_burst", strconv.Itoa(opts.CandidateBurst))
		}
	}
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetAppRoutes returns the per-route label snapshot for the named
// app (ADR-093). The bounded label set is served by the
// gatewayd-internal control listener and reverse-proxied by apid;
// each entry is "METHOD /raw/path" with overflow collapsed to
// "__route_other__" when the per-app cap (50) is exceeded. Source
// is "live" on success and "unavailable" when the control
// listener dial failed — callers should render both branches the
// same way (empty list, distinct chip).
//
// CapHit (ADR-093 Tier B item #1) is true iff the app's route
// label set reached RouteMetricsPerAppCap (50) and additional
// routes are collapsing into the reserved __route_other__ bucket.
// When true, len(Routes) == 52 (50 real + reserved empty +
// __route_other__). When false, the dashboard can render "you have
// N admitted routes" without counting. CapHit is the zero value
// (false) on the source: unavailable path — the cap state is
// unknown when the gatewayd-internal dial fails, so the field is
// not part of the unreliable wire.
func (c *Client) GetAppRoutes(ctx context.Context, slug string) (AppRoutesResponse, error) {
	var out AppRoutesResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/routes", nil, &out)
}

// GetAppStreamingStatus returns the per-request streaming
// classification for the named app (ADR-102 D6). The endpoint is
// the SDK-side mirror of pkg/gateway.(*Handler).decideStreaming —
// a customer hitting this endpoint sees exactly what the gateway's
// gate machine would resolve for the next inbound request, with the
// same status enum (api.StreamingStatus*) and the same effective
// cap (plan cap by default; endpoint-rule MaxBodyBytesStreaming if
// a kind=limit edge rule matched).
//
// Use case: a customer evaluating "will my next request stream?"
// fires this endpoint pre-flight instead of probing with a real
// request and reading the Streaming-Status response header. The
// probe does NOT mutate state and does NOT warm a wake — it's a
// pure read against the per-app cache.
func (c *Client) GetAppStreamingStatus(ctx context.Context, slug string) (AppStreamingStatus, error) {
	var out AppStreamingStatus
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/streaming-cap", nil, &out)
}

// GetAppsMetrics returns the account-wide per-app metrics rollup
// (issue #393). One call replaces N per-app GetAppMetrics calls;
// the response is keyed by app_slug so the dashboard renders rows
// without a parallel /v1/apps lookup. rng follows the same closed
// vocabulary as the per-app endpoint. First Prometheus failure
// short-circuits the entire response with source="degraded: …"
// and zeroed apps — see ADR-045.
func (c *Client) GetAppsMetrics(ctx context.Context, rng string) (AppsMetricsResponse, error) {
	var out AppsMetricsResponse
	path := "/v1/apps/metrics"
	if rng != "" {
		q := url.Values{}
		q.Set("range", rng)
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetAppSLO returns the per-app SLO panel for slug over the
// named SLO window. window is one of "1h", "24h", "7d"
// (strict subset of the /metrics vocabulary) — empty
// falls back to the server's default (24h). Issue #696 /
// ADR-082. Distinct from GetAppMetrics (issue #273 /
// ADR-042) which is the 5m-window dashboard panel.
func (c *Client) GetAppSLO(ctx context.Context, slug, window string) (AppSLOResponse, error) {
	var out AppSLOResponse
	path := "/v1/apps/" + slug + "/slo"
	if window != "" {
		q := url.Values{}
		q.Set("window", window)
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetAccountSLO returns the account-wide SLO rollup over
// the named SLO window. Flat scalar response (no per-app
// map). window follows the same closed vocabulary as the
// per-app endpoint. Issue #696 / ADR-082.
func (c *Client) GetAccountSLO(ctx context.Context, window string) (AccountSLOResponse, error) {
	var out AccountSLOResponse
	path := "/v1/account/slo"
	if window != "" {
		q := url.Values{}
		q.Set("window", window)
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// AppErrorsSummaryOptions controls the query for GetAppErrorsSummary.
// Both Since and Until are RFC3339Nano strings (NOT time.Time — the
// wire form is the canonical string so caller-side parse failures
// surface at the SDK boundary, not inside the transport). When Since
// or Until is empty, the server applies its own defaults
// (until=now(), since=until-24h) and may clamp the span to
// AppErrorsWindowMaxHours (168h). Cursor is an opaque base64 token
// from a previous response's NextCursor; empty starts a fresh scan.
// Limit defaults to AppErrorsSummaryDefaultLimit (20) and is capped
// at AppErrorsSummaryMaxLimit (100) by the server. ADR-096 / PR-B.
type AppErrorsSummaryOptions struct {
	Since  string
	Until  string
	Cursor string
	Limit  int
}

// appendAppErrorsSummaryQuery joins since/until/cursor/limit onto a
// path with the right separator. Shared by GetAppErrorsSummary so
// the query-string shape stays consistent regardless of which subset
// of fields the caller populates.
func appendAppErrorsSummaryQuery(path string, opts AppErrorsSummaryOptions) string {
	q := url.Values{}
	if opts.Since != "" {
		q.Set("since", opts.Since)
	}
	if opts.Until != "" {
		q.Set("until", opts.Until)
	}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	if opts.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", opts.Limit))
	}
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

// GetAppErrorsSummary returns the per-app top-N grouped error
// fingerprints for slug over the [since, until] window (Sentry-style
// continuous window, NOT the SLO closed-set ?window= vocabulary). One
// row per (account_id, app_id, fingerprint), sorted by count DESC,
// then last_seen_at DESC, then fingerprint ASC. The server clamps the
// span to AppErrorsWindowMaxHours (168h) and reports
// WindowClamped=true on the response when it does so. ADR-096 /
// PR-B. Sibling surface: GetAppSLO is the latency/error-rate panel
// (ADR-082); GetAppRoutes is the per-route panel (ADR-093).
func (c *Client) GetAppErrorsSummary(ctx context.Context, slug string, opts AppErrorsSummaryOptions) (AppErrorsSummaryResponse, error) {
	var out AppErrorsSummaryResponse
	path := appendAppErrorsSummaryQuery("/v1/apps/"+slug+"/errors/summary", opts)
	return out, c.do(ctx, "GET", path, nil, &out)
}

// ListAppErrorRequests paginates the drill-down rows for one
// fingerprint under GET /v1/apps/{slug}/errors/{fingerprint}. The
// response's NextCursor (when non-empty) feeds the next call's
// cursor arg. limit defaults to AppErrorsSummaryDefaultLimit on the
// server when zero; passing 0 from the SDK hits the same default.
// Returns 404 when the fingerprint has been purged by the retention
// cron or never existed (cross-account slug returns 404 too — the
// IDOR posture is byte-identical). ADR-096 / PR-B.
func (c *Client) ListAppErrorRequests(ctx context.Context, slug, fingerprint, cursor string, limit int) (AppErrorRequestsResponse, error) {
	var out AppErrorRequestsResponse
	path := "/v1/apps/" + slug + "/errors/" + fingerprint
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// ListAppErrorRequestsAll is the cursor walker; NOT a route — the
// route returns a single page, this method pages through to the
// end. Mirrors the ListOrgInvitationsAll / ListAuditLogAll walker
// shape. Returns the accumulated []AppErrorRequestItem slice;
// terminates cleanly when NextCursor is empty. ADR-096 / PR-B.
func (c *Client) ListAppErrorRequestsAll(ctx context.Context, slug, fingerprint string) ([]AppErrorRequestItem, error) {
	var out []AppErrorRequestItem
	cursor := ""
	for {
		page, err := c.ListAppErrorRequests(ctx, slug, fingerprint, cursor, 100)
		if err != nil {
			return out, err
		}
		out = append(out, page.Requests...)
		if page.NextCursor == "" {
			return out, nil
		}
		cursor = page.NextCursor
		if err := ctx.Err(); err != nil {
			return out, err
		}
	}
}

// GetAppErrorSample returns the single sample row for fingerprint
// under GET /v1/apps/{slug}/errors/{fingerprint}/first — the row that
// surfaces the redacted headers_sample + the list of redaction
// pattern names applied (so the dashboard can render "we redacted X
// / Y / Z"). When the fingerprint has been purged the server returns
// 404 (same posture as ListAppErrorRequests). ADR-096 / PR-B.
func (c *Client) GetAppErrorSample(ctx context.Context, slug, fingerprint string) (AppErrorSampleResponse, error) {
	var out AppErrorSampleResponse
	path := "/v1/apps/" + slug + "/errors/" + fingerprint + "/first"
	return out, c.do(ctx, "GET", path, nil, &out)
}

// UsageSummary returns the account-wide monthly roll-up
// (used_gb_hours, included_gb_hours, overage_gb_hours, overage_cents).
// Distinct from GetUsage which returns per-app rows; empty month falls
// back to the server's default (current month).
func (c *Client) UsageSummary(ctx context.Context, month string) (UsageSummaryResponse, error) {
	var out UsageSummaryResponse
	path := "/v1/usage/summary"
	if month != "" {
		q := url.Values{}
		q.Set("month", month)
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// UsageDaily returns the per-(app, day) rollup rows the meterd rollup
// loop populated into usage_daily (ADR-048 §5). day is "YYYY-MM-DD"
// and is required; the server 400s on empty so callers don't get
// ambiguous "current day" semantics from the SDK side.
func (c *Client) UsageDaily(ctx context.Context, day string) (DailyUsageListResponse, error) {
	var out DailyUsageListResponse
	q := url.Values{}
	q.Set("day", day)
	return out, c.do(ctx, "GET", "/v1/usage/daily?"+q.Encode(), nil, &out)
}

// StorageUsage returns the per-(app, day) snapshot+layer byte rollup
// (ADR-049 §B.3). day is "YYYY-MM-DD" and is required; the server
// 400s on empty. Informational only — not billed today.
func (c *Client) StorageUsage(ctx context.Context, day string) (StorageUsageListResponse, error) {
	var out StorageUsageListResponse
	q := url.Values{}
	q.Set("day", day)
	return out, c.do(ctx, "GET", "/v1/usage/storage?"+q.Encode(), nil, &out)
}

// ListDeployments returns a single page of deployments with a
// "next_before" cursor (RFC3339Nano). Use ListDeploymentsAll (added in
// commit 2) to walk every page automatically.
func (c *Client) ListDeployments(ctx context.Context, before string, limit int) (DeploymentListResponse, error) {
	var out DeploymentListResponse
	q := url.Values{}
	if before != "" {
		q.Set("before", before)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/deployments"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetBillingPortal returns the active provider's billing
// portal URL for the authenticated account (issue #253). Empty string
// means the box has FAAS_BILLING_PORTAL_URL unset — the CLI prints a
// friendly hint instead of opening the browser to "". The endpoint
// is authenticated via the standard Bearer / API-key chain (same
// surface as usage reads).
//
// Issue #242: this signature is preserved for callers that only
// care about the URL (cmdBillingPortal). New callers that need
// the payment-method summary should use GetBillingPortalFull.
func (c *Client) GetBillingPortal(ctx context.Context) (string, error) {
	full, err := c.GetBillingPortalFull(ctx)
	if err != nil {
		return "", err
	}
	return full.URL, nil
}

// GetBillingPortalFull returns both the portal URL AND the
// card-on-file summary (issue #242). The CLI's `faas billing
// payment-method` subcommand renders from this method; the
// dashboard's billing page does the same. PaymentMethod is the
// zero value when the account has no card on file — the CLI
// branches on the empty brand to print a "no payment method on
// file" hint.
func (c *Client) GetBillingPortalFull(ctx context.Context) (BillingPortalResponse, error) {
	var out BillingPortalResponse
	if err := c.do(ctx, "GET", "/v1/billing/portal", nil, &out); err != nil {
		return BillingPortalResponse{}, err
	}
	return out, nil
}

// PostBillingRetry retries the latest unpaid invoice / transaction
// for the authenticated account (issue #242). Closes the
// customer-trust lie in pkg/mail/account.go:107,150 — the dunning
// email promises `faas billing retry`; this is what it calls.
// Returns the apId-side attempt id + the provider-side reference
// id + a status string ("pending_provider_confirmation" today).
func (c *Client) PostBillingRetry(ctx context.Context) (BillingRetryResponse, error) {
	var out BillingRetryResponse
	if err := c.do(ctx, "POST", "/v1/billing/retry", nil, &out); err != nil {
		return BillingRetryResponse{}, err
	}
	return out, nil
}

// PostBillingCancel sets cancel_at_period_end on the authenticated
// account's subscription (issue #242). Account keeps running
// until period end then downgrades to Free (spec §4.7). The
// destructive nature is gated on the CLI side (typed-confirm from
// PR #782: "cancel subscription"); apid itself does not gate.
// Returns the effective-at timestamp so the CLI can print
// "your apps will stop on <date>".
func (c *Client) PostBillingCancel(ctx context.Context) (BillingCancelResponse, error) {
	var out BillingCancelResponse
	if err := c.do(ctx, "POST", "/v1/billing/cancel", nil, &out); err != nil {
		return BillingCancelResponse{}, err
	}
	return out, nil
}

// ListInvoices returns a single page of the authenticated account's
// invoices (issue #259). month is "YYYY-MM" or "" for all months;
// before is the RFC3339Nano cursor for the next page ("" for first
// page). limit is clamped server-side at 100. Empty history returns
// a response with Items=nil (or empty) and no error.
func (c *Client) ListInvoices(ctx context.Context, month, before string, limit int) (InvoiceListResponse, error) {
	var out InvoiceListResponse
	v := url.Values{}
	if month != "" {
		v.Set("month", month)
	}
	if before != "" {
		v.Set("before", before)
	}
	if limit > 0 {
		v.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := "/v1/invoices"
	if encoded := v.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// IssueAccountCredit issues a positive-cents credit to the named
// account via POST /v1/admin/accounts/{id}/credits (issue #279).
// accountID is the target account's UUID. idemKey is the
// Idempotency-Key header value; pass an empty string to let the SDK
// auto-UUIDv4 (the typical path for the dashboard) or a stable
// string (the CLI's `cli-admin-credit-…` path) so a flaky-network
// retry returns the same credit_id. reason is operator-supplied
// (3..500 chars; the handler validates client-side).
//
// Auth: requires an admin-scoped API key in c.Token (admin-only
// endpoint, two-layer auth: requireScope(ScopesAdminOnly) +
// adminAllows email allowlist).
func (c *Client) IssueAccountCredit(ctx context.Context, accountID, idemKey string, cents int64, reason string) (AccountCreditResponse, error) {
	if idemKey == "" {
		idemKey = newUUIDv4()
	}
	body, err := json.Marshal(map[string]any{"cents": cents, "reason": reason})
	if err != nil {
		return AccountCreditResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/v1/admin/accounts/"+accountID+"/credits", bytes.NewReader(body))
	if err != nil {
		return AccountCreditResponse{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Idempotency-Key", idemKey)
	req.Header.Set("Content-Type", "application/json")
	var out AccountCreditResponse
	return out, c.doReq(c.http, req, &out)
}

// RefundAccount issues an operator-initiated refund for a local invoice via
// POST /v1/admin/accounts/{id}/refunds. The server resolves the provider order
// from invoiceID and verifies that it belongs to accountID before moving money.
//
// idemKey is forwarded to the provider's native idempotency mechanism. Pass a
// stable key when retrying an ambiguous request; an empty key is auto-generated
// for one-shot SDK calls.
func (c *Client) RefundAccount(ctx context.Context, accountID, invoiceID, idemKey string, amountCents int64, reason string) (AdminRefundResponse, error) {
	if idemKey == "" {
		idemKey = newUUIDv4()
	}
	body, err := json.Marshal(map[string]any{
		"invoice_id":   invoiceID,
		"amount_cents": amountCents,
		"reason":       reason,
	})
	if err != nil {
		return AdminRefundResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/v1/admin/accounts/"+accountID+"/refunds", bytes.NewReader(body))
	if err != nil {
		return AdminRefundResponse{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Idempotency-Key", idemKey)
	req.Header.Set("Content-Type", "application/json")
	var out AdminRefundResponse
	return out, c.doReq(c.http, req, &out)
}

// ConsumeInvoiceCredits drains the account's active credits FIFO
// against an invoice's overage (issue #279 PR-C). Triggered by the
// operator at month-rollover today; the same reducer will be called
// from the PR-B UpsertInvoice webhook Tx and a future meterd cron —
// the HTTP endpoint is the contract the SDK exposes.
//
// invoiceID is the row ID (UUID) from GET /v1/invoices; the reducer
// re-resolves account + period + provider_invoice_id internally.
// idemKey is the Idempotency-Key header value — auto-UUIDv4 by
// default; pass a stable string for retryable ops.
//
// Auth: admin-scoped API key (admin-only endpoint, two-layer auth:
// requireScope(ScopesAdminOnly) + adminAllows email allowlist +
// requireMFA).
func (c *Client) ConsumeInvoiceCredits(ctx context.Context, invoiceID, idemKey string) (ConsumeInvoiceResponse, error) {
	if idemKey == "" {
		idemKey = newUUIDv4()
	}
	body, err := json.Marshal(map[string]any{})
	if err != nil {
		return ConsumeInvoiceResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/v1/invoices/"+invoiceID+"/consume-credits", bytes.NewReader(body))
	if err != nil {
		return ConsumeInvoiceResponse{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Idempotency-Key", idemKey)
	req.Header.Set("Content-Type", "application/json")
	var out ConsumeInvoiceResponse
	return out, c.doReq(c.http, req, &out)
}

// --- cosign trusted-publisher list (issue #472 / ADR-054) -------------------
//
// Four admin-scoped methods. Mounted in apid under the
// authLimited → requireMFA → requireScope(ScopesAdminOnly) chain
// (cmd/apid/server.go). The SDK callers here are operator-side
// (`gregale trusted-publishers add|remove|list`); programmatic
// SDK users reach the surface through the same methods.
//
// ListAppTrustedSigners returns every (name, public_key_pem) row on
// the app. The empty-slice case returns a non-nil empty slice (the
// server uses `make([]TrustedSigner, 0)`), so callers can iterate
// without a nil check.
func (c *Client) ListAppTrustedSigners(ctx context.Context, slug string) (AppTrustedSignerListResponse, error) {
	var out AppTrustedSignerListResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/trusted_signers", nil, &out)
}

// PutAppTrustedSigner uploads a base64-encoded DER SPKI pubkey to
// the per-app trusted-publisher list. The PEM-armoured wrapper is
// stripped on the CLI side (commands_trusted_publishers.go) so the
// wire shape is canonical across operators. Idempotent: re-PUT
// replaces the key material and stamps the calling admin's id on
// added_by_account_id.
func (c *Client) PutAppTrustedSigner(ctx context.Context, slug, name string, req AddTrustedSignerRequest) error {
	return c.do(ctx, "PUT", "/v1/apps/"+slug+"/trusted_signers/"+name, req, nil)
}

// DeleteAppTrustedSigner removes the (slug, name) row. 404 is
// surfaced as a *Problem with code trusted_signer_not_found; the
// CLI treats that as a no-op success (matching the cmdKeys delete
// shape on the customer API-key surface).
func (c *Client) DeleteAppTrustedSigner(ctx context.Context, slug, name string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/trusted_signers/"+name, nil, nil)
}

// UpdateAppSecurity flips the per-app require_signed flag
// (issue #472 / ADR-054). The body is a pointer-to-bool so callers
// can distinguish "don't touch" (nil) from "explicit true/false".
// Admin-scoped via the mount chain — a customer who could set this
// flag could pre-stage the trust list however they wanted, which is
// why the customer PATCH /v1/apps/{slug} endpoint silently drops
// require_signed (see AppResponse.RequireSigned doc on
// api/dto.go::UpdateAppRequest). Audit event: app.security_updated.
func (c *Client) UpdateAppSecurity(ctx context.Context, slug string, req AppSecurityRequest) (AppSecurityResponse, error) {
	var out AppSecurityResponse
	return out, c.do(ctx, "PATCH", "/v1/apps/"+slug+"/security", req, &out)
}

// GetAppStaticEgressIP reads the per-app static egress IP pin
// (ADR-119). Plan-agnostic — returns the current pin status even
// when the plan doesn't allow static egress IPs (plan_allowed=false,
// plan_cap=0 in that case). Customer-scoped (no admin required).
func (c *Client) GetAppStaticEgressIP(ctx context.Context, slug string) (AppStaticEgressIPResponse, error) {
	var out AppStaticEgressIPResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/static-egress-ip", nil, &out)
}

// SetAppStaticEgressIP pins a customer-supplied IPv4 to the app's
// egress traffic (ADR-119). Scale-only — apid returns 402
// plan_static_egress_ip_not_allowed for Free/Hobby/Pro. The handler
// validates family=4 + non-RFC1918 + non-link-local +
// non-multicast before the column write. Set=false with empty
// IP clears the pin. Audit event: app.static_egress_ip_set.
func (c *Client) SetAppStaticEgressIP(ctx context.Context, slug string, req SetAppStaticEgressIPRequest) (AppStaticEgressIPResponse, error) {
	var out AppStaticEgressIPResponse
	return out, c.do(ctx, "PUT", "/v1/apps/"+slug+"/static-egress-ip", req, &out)
}

// ClearAppStaticEgressIP drops the per-app static egress IP pin
// (ADR-119). Convenience wrapper around SetAppStaticEgressIP with
// Set=false. Idempotent — clearing a non-existent pin is a 204.
func (c *Client) ClearAppStaticEgressIP(ctx context.Context, slug string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/static-egress-ip", nil, nil)
}

// SetGithubWebhookSecret sets the per-tenant webhook secret for
// the given installation_id (PR-D / ADR-012 §7 amendment). The
// server hex-decodes SecretHex and writes the raw bytes to
// github_webhook_secrets. ON CONFLICT DO UPDATE so a rotation
// is a single idempotent call — every successful call bumps
// upgraded_at (the audit trail; an operator re-running with
// the same secret is itself a rotation event worth recording).
//
// Auth: admin-scoped API key (ScopesAdminOnly +
// adminAllows email allowlist). The handler also emits
// githubd_webhook_secret_total{status="set"} so a Prometheus
// alert can fire if a tenant rotates unexpectedly often.
func (c *Client) SetGithubWebhookSecret(ctx context.Context, req AdminSetGithubWebhookSecretRequest) (AdminSetGithubWebhookSecretResponse, error) {
	var out AdminSetGithubWebhookSecretResponse
	return out, c.do(ctx, "POST", "/v1/admin/github-webhook-secrets", req, &out)
}

// Org surface (issue #190 / IAM-6 / ADR-061, PR 5). The 11 methods
// below mirror the spec routes documented under api/openapi.yaml
// paths /v1/orgs*, /v1/invitations/{token}. Each maps 1:1 to a
// spec route so the sdk-coverage gate (cmd/sdk-coverage) doesn't
// false-positive on drift. Bearer-auth only; account-scoped routes
// (ListOrgs, CreateOrg) skip the X-Active-Org hint, path-scoped
// routes require it (the apid loadOrg middleware resolves the slug
// from the path and stamps the membership onto the principal).

// ListOrgs returns the orgs the caller has an active membership in
// (the personal org + every shared org the caller belongs to).
// Account-scoped — no X-Active-Org hint needed.
func (c *Client) ListOrgs(ctx context.Context) (OrgListResponse, error) {
	var out OrgListResponse
	return out, c.do(ctx, "GET", "/v1/orgs", nil, &out)
}

// CreateOrg creates a new shared (non-personal) org. The caller
// becomes the first owner. Personal orgs are minted by the PR 3
// backfill and cannot be re-created here.
func (c *Client) CreateOrg(ctx context.Context, req CreateOrgRequest) (OrgResponse, error) {
	var out OrgResponse
	return out, c.do(ctx, "POST", "/v1/orgs", req, &out)
}

// GetOrg returns the active org by slug. Authz: any active member
// (`org.view`); non-members see 403 `org_role_forbidden`. Unknown
// slugs are 404 `org_not_found`.
func (c *Client) GetOrg(ctx context.Context, slug string) (OrgResponse, error) {
	var out OrgResponse
	return out, c.do(ctx, "GET", "/v1/orgs/"+slug, nil, &out)
}

// PatchOrg applies a partial update to the org (name and/or plan).
// Authz routing:
//   - Name → org.manage_billing (owner + billing)
//   - Plan → org.change_plan     (owner only)
//
// Personal orgs are immutable (409 `org_personal_immutable`).
func (c *Client) PatchOrg(ctx context.Context, slug string, req PatchOrgRequest) (OrgResponse, error) {
	var out OrgResponse
	return out, c.do(ctx, "PATCH", "/v1/orgs/"+slug, req, &out)
}

// DeleteOrg soft-deletes the org (sets status=deleted_pending).
// Hard-delete + GDPR purge land in PR 8. Personal orgs are
// immutable.
func (c *Client) DeleteOrg(ctx context.Context, slug string) error {
	return c.do(ctx, "DELETE", "/v1/orgs/"+slug, nil, nil)
}

// ListOrgMembers returns the active member list. Removed rows are
// filtered at the API boundary; live-cap count drops on remove
// even though the row stays for audit.
func (c *Client) ListOrgMembers(ctx context.Context, slug string) (MemberListResponse, error) {
	var out MemberListResponse
	return out, c.do(ctx, "GET", "/v1/orgs/"+slug+"/members", nil, &out)
}

// InviteOrgMember mints a 32-byte plaintext token (returned ONCE
// in the response) and stores only the SHA-256 hash. Token expires
// after 14 days. Role cannot be `owner`.
func (c *Client) InviteOrgMember(ctx context.Context, slug string, req InviteMemberRequest) (InvitationWithTokenResponse, error) {
	var out InvitationWithTokenResponse
	return out, c.do(ctx, "POST", "/v1/orgs/"+slug+"/members", req, &out)
}

// ChangeOrgMemberRole changes a member's role. Owner-only
// (`org.change_role`). Role cannot be `owner`; transfer-ownership is
// the only path to owner.
func (c *Client) ChangeOrgMemberRole(ctx context.Context, slug, accountID string, req ChangeMemberRoleRequest) (OrgMemberResponse, error) {
	var out OrgMemberResponse
	return out, c.do(ctx, "PATCH", "/v1/orgs/"+slug+"/members/"+accountID, req, &out)
}

// RemoveOrgMember removes a member. Owner-only
// (`org.remove_members`). Stamps `removed_at` on the row (the row
// stays for audit; live-cap count drops). Self-removal is rejected
// at the boundary.
func (c *Client) RemoveOrgMember(ctx context.Context, slug, accountID string) error {
	return c.do(ctx, "DELETE", "/v1/orgs/"+slug+"/members/"+accountID, nil, nil)
}

// TransferOrgOwnership atomically promotes new_owner_account_id to
// owner and demotes the caller to admin. The new owner must already
// be an active member of the org.
func (c *Client) TransferOrgOwnership(ctx context.Context, slug string, req TransferOwnershipRequest) (OrgResponse, error) {
	var out OrgResponse
	return out, c.do(ctx, "POST", "/v1/orgs/"+slug+"/transfer_ownership", req, &out)
}

// PeekInvitation is a read-only lookup that returns the invitation
// metadata (email, role, org slug, expires_at) without consuming
// the token. Used by the dashboard to render "you've been invited
// to Acme Inc. as developer" before the invitee accepts.
func (c *Client) PeekInvitation(ctx context.Context, token string) (OrgInvitationResponse, error) {
	var out OrgInvitationResponse
	return out, c.do(ctx, "GET", "/v1/invitations/"+token, nil, &out)
}

// AcceptInvitation consumes the token via Store.ConsumeOrgInvitation
// (the load-bearing cap-in-tx check lives there) and inserts the
// bearer as a new active member. Two audit rows fire post-mutation:
// `org.invitation.accepted` and `org.member.added`. Returns 410
// (`org_invitation_invalid`) on unknown / consumed / revoked /
// expired tokens; 409 (`org_already_member`) if the bearer is
// already a member; 403 (`org_member_cap_exceeded`) at the plan cap.
func (c *Client) AcceptInvitation(ctx context.Context, token string) (OrgMemberResponse, error) {
	var out OrgMemberResponse
	return out, c.do(ctx, "POST", "/v1/invitations/"+token+"/accept", nil, &out)
}

// RevokeInvitation stamps revoked_at on a still-pending invitation.
// Owner + admin only (org.invite_members, symmetric with
// InviteOrgMember). Emits `org.invitation.revoked` with an 8-char
// token-hash prefix (never the full hash).
func (c *Client) RevokeInvitation(ctx context.Context, slug, token string) error {
	return c.do(ctx, "DELETE", "/v1/orgs/"+slug+"/invitations/"+token, nil, nil)
}

// ListOrgInvitationsAll walks the next_before cursor on
// GET /v1/orgs/{slug}/invitations until the server returns an
// empty cursor, returning every invitation in created_at DESC
// order. Useful for the dashboard "Pending invitations" panel
// that wants the full list without forcing the customer to wire
// a loop.
//
// The server caps each page at 100 rows (handled by
// ListOrgInvitations); this method requests max page size when
// walking. Cancelling ctx stops the walk at the next page
// boundary — the current page's rows are returned up to the
// cancellation point.
func (c *Client) ListOrgInvitationsAll(ctx context.Context, slug string) ([]OrgInvitationResponse, error) {
	var out []OrgInvitationResponse
	cursor := ""
	for {
		page, err := c.ListOrgInvitations(ctx, slug, cursor, 100)
		if err != nil {
			return out, err
		}
		out = append(out, page.Invitations...)
		if page.NextBefore == "" {
			return out, nil
		}
		cursor = page.NextBefore
		if err := ctx.Err(); err != nil {
			return out, err
		}
	}
}

// ListOrgInvitations returns a single page of invitations for the
// given org slug, cursor-paginated via ?before=<id>&limit=<n>
// (default 25, max 100). Every role on the org can read (the
// authz gate is OrgActionView, same as GET /v1/orgs/{slug}/members).
// Emits one `org.invitation.viewed` audit row per render.
func (c *Client) ListOrgInvitations(ctx context.Context, slug, before string, limit int) (InvitationListResponse, error) {
	var out InvitationListResponse
	path := "/v1/orgs/" + slug + "/invitations"
	q := url.Values{}
	if before != "" {
		q.Set("before", before)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetOrgSeatUsage returns {used, limit, plan} for the active org.
// `limit` is the plan cap (OrgMembersMax). Free / unknown plans
// return 0 (the fail-closed accessor). Visibility-only — PR 9 ships
// the per-seat pricing cut-over.
func (c *Client) GetOrgSeatUsage(ctx context.Context, slug string) (SeatUsageResponse, error) {
	var out SeatUsageResponse
	return out, c.do(ctx, "GET", "/v1/orgs/"+slug+"/seat_usage", nil, &out)
}

// --- Webhook delivery (issue #476 / ADR-076) -----------------------------
//
// Outbound webhook subscription + delivery ledger. Mirrors the
// pkg/api/webhooks.go DTO definitions and the eight apid routes
// under /v1/apps/{slug}/webhooks[/...]. Same posture as the
// crons surface: `Client` is a thin HTTP wrapper; the wire DTOs
// are exported types in this package.

// ListAppWebhooks returns the per-app webhook subscriptions.
func (c *Client) ListAppWebhooks(ctx context.Context, slug string) ([]AppWebhookResponse, error) {
	var out []AppWebhookResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/webhooks", nil, &out)
}

// CreateAppWebhook subscribes a target URL to events on the app.
// The plaintext WebhookSecret is sent over the wire; the response
// carries only the masked constant `***` for WebhookSecretSealedMasked.
func (c *Client) CreateAppWebhook(ctx context.Context, slug string, req CreateAppWebhookRequest) (AppWebhookResponse, error) {
	var out AppWebhookResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/webhooks", req, &out)
}

// GetAppWebhook returns a single subscription by id.
func (c *Client) GetAppWebhook(ctx context.Context, slug, id string) (AppWebhookResponse, error) {
	var out AppWebhookResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/webhooks/"+id, nil, &out)
}

// UpdateAppWebhook PATCHes the target_url / event_filter /
// retry_policy / enabled triple. Pointer fields let callers
// distinguish "leave as-is" from "set to empty / nil".
func (c *Client) UpdateAppWebhook(ctx context.Context, slug, id string, req UpdateAppWebhookRequest) (AppWebhookResponse, error) {
	var out AppWebhookResponse
	return out, c.do(ctx, "PATCH", "/v1/apps/"+slug+"/webhooks/"+id, req, &out)
}

// DeleteAppWebhook removes the subscription. Pending deliveries
// remain in the ledger but no new ones will be enqueued after
// delete; existing rows drain per their next_attempt_at.
func (c *Client) DeleteAppWebhook(ctx context.Context, slug, id string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/webhooks/"+id, nil, nil)
}

// RotateAppWebhookSecret asks the server to mint a fresh sealed
// secret. The new plaintext is returned ONCE in the response
// (RotateAppWebhookSecretResponse.WebhookSecret); callers MUST
// persist it immediately and MUST NOT log it.
func (c *Client) RotateAppWebhookSecret(ctx context.Context, slug, id string) (RotateAppWebhookSecretResponse, error) {
	var out RotateAppWebhookSecretResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/webhooks/"+id+"/rotate-secret", nil, &out)
}

// ListAppWebhookDeliveries paginates the per-subscription delivery
// ledger. Status is one of pending|in_flight|succeeded|failed|dead
// or empty (all statuses). PageSize caps the response (1..100).
func (c *Client) ListAppWebhookDeliveries(ctx context.Context, slug, id string, opts ListAppWebhookDeliveriesOptions) (AppWebhookDeliveryListResponse, error) {
	var out AppWebhookDeliveryListResponse
	path := "/v1/apps/" + slug + "/webhooks/" + id + "/deliveries"
	if opts.Status != "" || opts.PageSize > 0 || opts.PageToken != "" {
		q := url.Values{}
		if opts.Status != "" {
			q.Set("status", opts.Status)
		}
		if opts.PageSize > 0 {
			q.Set("page_size", strconv.Itoa(opts.PageSize))
		}
		if opts.PageToken != "" {
			q.Set("page_token", opts.PageToken)
		}
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// RetryAppWebhookDelivery moves a `dead` row back to `pending` and
// resets next_attempt_at to now(). Returns the refreshed delivery
// row so callers can show "queued for attempt N+1 at HH:MM:SS".
func (c *Client) RetryAppWebhookDelivery(ctx context.Context, slug, id, deliveryID string) (AppWebhookRetryDeliveryResponse, error) {
	var out AppWebhookRetryDeliveryResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/webhooks/"+id+"/deliveries/"+deliveryID+"/retry", nil, &out)
}

// --- /v1/account/dpa (spec §17 G6) ----------------------------------------
//
// DPA = Data Processing Addendum. Public, no-auth endpoint that
// returns the GDPR DPA template as text/markdown (cmd/apid/handlers_account.go:231).
// Customers pipe the body into their vendor-onboarding automation.
//
// c.do unmarshals JSON; doBytes returns the raw response so the
// caller gets the markdown verbatim.
func (c *Client) GetAccountDPA(ctx context.Context) ([]byte, error) {
	var out []byte
	return out, c.doBytes(ctx, "GET", "/v1/account/dpa", nil, &out)
}

// --- /v1/orgs/me (IAM-6 / ADR-061) ----------------------------------------
//
// Returns the caller's currently-active org + membership role, or
// {"org": null} when neither X-Active-Org nor ?org= was supplied
// (cmd/apid/handlers_org_me.go:59). Drives `gregale orgs me`.
func (c *Client) GetMyOrg(ctx context.Context) (OrgMeResponse, error) {
	var out OrgMeResponse
	return out, c.do(ctx, "GET", "/v1/orgs/me", nil, &out)
}

// --- PR-P3 admin billing surface -------------------------------------------
//
// Five methods backing the operator-facing CLI subcommands:
//
//	ListPaddleCatalog                 — GET    /v1/admin/billing-paddle-catalog
//	SyncPaddleCatalog                 — POST   /v1/admin/billing-paddle-catalog/sync
//	ResetPaddleCatalog                — DELETE /v1/admin/billing-paddle-catalog
//	ReconcileAccount                  — POST   /v1/admin/billing-reconcile/{id}
//	GetBillingPaddleOveragePreflight  — GET    /v1/admin/billing-paddle-overage/preflight (B4 / Tier 1)
//
// Auth: admin-scoped API key + email in FAAS_ADMIN_EMAILS allowlist.
// The handlers return 501 with code billing_op_unsupported when the
// active provider does not implement paddle.OpProvider (Stripe
// today). The client surfaces that as a typed error via c.do's
// Problem unmarshal; callers that care can branch on the code.

// ListPaddleCatalog fetches the cached Paddle price + product
// catalog. Returns 200 + BillingCatalogResponse on success; the
// handler 501s on non-Paddle providers.
func (c *Client) ListPaddleCatalog(ctx context.Context) (BillingCatalogResponse, error) {
	var out BillingCatalogResponse
	return out, c.do(ctx, "GET", "/v1/admin/billing-paddle-catalog", nil, &out)
}

// SyncPaddleCatalog forces an EnsurePlanProducts round-trip and
// returns the post-sync catalog. idemKey is the Idempotency-Key
// header value; pass an empty string to let the SDK auto-UUIDv4
// (the typical CLI path; the dashboard's manual retry uses a
// stable "cli-paddle-sync-<ts>" key).
func (c *Client) SyncPaddleCatalog(ctx context.Context, idemKey string) (BillingCatalogResponse, error) {
	if idemKey == "" {
		idemKey = newUUIDv4()
	}
	req, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/v1/admin/billing-paddle-catalog/sync", nil)
	if err != nil {
		return BillingCatalogResponse{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Idempotency-Key", idemKey)
	var out BillingCatalogResponse
	return out, c.doReq(c.http, req, &out)
}

// ResetPaddleCatalog signals a Paddle catalog reset. The handler
// is a no-op for Paddle (catalog is durable on the platform); the
// CLI prints the "delete products from the Paddle Dashboard, then
// call sync" warning based on the empty entries + empty synced_at
// in the response.
func (c *Client) ResetPaddleCatalog(ctx context.Context) (BillingCatalogResponse, error) {
	var out BillingCatalogResponse
	return out, c.do(ctx, "DELETE", "/v1/admin/billing-paddle-catalog", nil, &out)
}

// ReconcileAccount runs a single-account reconcile against the
// active billing Provider. accountID is the target account UUID.
// Stripe implements this; Paddle returns 501 with code
// billing_reconcile_unsupported.
func (c *Client) ReconcileAccount(ctx context.Context, accountID string) (BillingReconcileResponse, error) {
	var out BillingReconcileResponse
	return out, c.do(ctx, "POST", "/v1/admin/billing-reconcile/"+accountID, nil, &out)
}

// GetBillingPaddleOveragePreflight runs the B4 pre-flight probe
// against the paddle_overage_dedupe table. Returns 200 +
// BillingPaddleOveragePreflightResponse on success; the handler
// is admin-only and the JSON shape always includes the four
// HasX bools + the per-state row counts so the CLI can render
// either a green-light or a "missing column X" hint without a
// second round-trip.
func (c *Client) GetBillingPaddleOveragePreflight(ctx context.Context) (BillingPaddleOveragePreflightResponse, error) {
	var out BillingPaddleOveragePreflightResponse
	return out, c.do(ctx, "GET", "/v1/admin/billing-paddle-overage/preflight", nil, &out)
}

// --- ADR-089 PR-C rekey progress -------------------------------------------
//
// GetRekeyProgress polls the background re-seal runner (ADR-089 PR-C).
// The operator pings this after a host-age rotation to monitor when
// the migration completes; the dashboard renders the cumulative
// (rekeyed+skipped) vs (failed) so an operator can spot stuck or
// errored rows.
//
// 503 + code=rekey_disabled when the runner is not enabled on the
// host (FAAS_REKEY_ENABLED unset). The SDK does not special-case the
// 503 — callers that care can branch on the *Problem's Code field.
// Auth: admin-scoped API key + email in FAAS_ADMIN_EMAILS allowlist
// (two-layer gate, same as every other /v1/admin/* route).
func (c *Client) GetRekeyProgress(ctx context.Context) (RekeyProgress, error) {
	var out RekeyProgress
	return out, c.do(ctx, "GET", "/v1/admin/secrets/rekey-progress", nil, &out)
}

// Data upstreams (ADR-098 §9.A PR-B). The 4 endpoints back
// `gregale upstreams list/get/create/delete`. The wire routes
// are owned by cmd/apid/handlers_upstreams.go; the typed DTOs
// live next to this method in pkg/api/upstreams.go (closed-vocab
// DataUpstreamKind, RFC 952/1123 host regex, port range).
//
// §11 invariant: the DTO surface NEVER includes the plaintext
// host — only host_redacted_hash (sha256(salt||host)) surfaces
// to the caller. A custom POST that includes `host` is rejected
// server-side; the SDK does not special-case that 400 — the
// *Problem's Code field is `upstream_invalid_host`.
func (c *Client) ListAppDataUpstreams(ctx context.Context, slug string) ([]DataUpstreamResponse, error) {
	var out []DataUpstreamResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/upstreams", nil, &out)
}

// ListAppDataUpstreamsWithQuota is the quota-aware sibling of
// ListAppDataUpstreams (issue #952). Decodes the wrapped
// DataUpstreamListResponse envelope so the CLI's
// `gregale inspect <slug> --upstreams` command can render the
// "N/M upstreams" stamp without a second request.
//
// The bare ListAppDataUpstreams is preserved for callers that
// don't need quota (PR #894 e2e tests, future dashboard
// hydration paths) — both methods live side by side and decode
// the same wire surface, just into different shapes.
//
// scope is the optional filter forwarded as ?scope=<scope> in
// the query string. Empty means "all scopes" (handlers_upstreams.go
// treats empty as the all-scopes branch via scopeFromQuery); any
// non-empty value is validated server-side. The CLI itself does
// not impose a closed vocabulary on scope — the SDK mirrors what
// the wire accepts.
//
// §11 invariant: the decoded shape NEVER carries plaintext host —
// only HostRedactedHash + HostLast4 (handlers_upstreams.go:339).
// The CLI renderer in commands_inspect_upstreams.go references
// only those two fields.
func (c *Client) ListAppDataUpstreamsWithQuota(ctx context.Context, slug, scope string) ([]DataUpstreamResponse, int, int, error) {
	path := "/v1/apps/" + slug + "/upstreams"
	if scope != "" {
		q := url.Values{}
		q.Set("scope", scope)
		path += "?" + q.Encode()
	}
	var out DataUpstreamListResponse
	if err := c.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, 0, 0, err
	}
	return out.Upstreams, out.Count, out.Quota, nil
}
func (c *Client) GetAppDataUpstream(ctx context.Context, slug, id string) (DataUpstreamResponse, error) {
	var out DataUpstreamResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/upstreams/"+id, nil, &out)
}
func (c *Client) CreateAppDataUpstream(ctx context.Context, slug string, req PutDataUpstreamRequest) (DataUpstreamResponse, error) {
	var out DataUpstreamResponse
	return out, c.do(ctx, "PUT", "/v1/apps/"+slug+"/upstreams", req, &out)
}
func (c *Client) DeleteAppDataUpstream(ctx context.Context, slug, id string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/upstreams/"+id, nil, nil)
}

// CancelDeployment flips a deployment in {pending, building, imaging,
// snapshotting} to "cancelled" and cascades the cancel to its
// in-flight builds. Live deployments return 409 with code
// "deployment_cancel_live_forbidden" and the canonical hint pointing
// at `gregale deploys rollback` — the per-deployment contract from
// ADR-118 / ADR-124. Optional reason values are the closed set
// "user" | "auto_quota" | "auto_health" | "system" (pkg/state.CancelReason);
// an empty string defaults to "user" server-side.
func (c *Client) CancelDeployment(ctx context.Context, appSlug, id string, reason string) (DeploymentResponse, error) {
	var out DeploymentResponse
	body := CancelDeploymentRequest{Reason: reason}
	return out, c.do(ctx, "POST", "/v1/apps/"+appSlug+"/deployments/"+id+"/cancel", body, &out)
}

// ReorderDeployment updates the priority of a still-pending deployment.
// 0 = "deploy immediately" (top of queue), 100 = FIFO default, 1000 =
// background rebuild. Plan-gated (Hobby/Pro/Scale only); Free returns
// 402 "plan_reorder_disabled". Returns ErrReorderNotPending (409) when
// the deployment has already moved off the pending queue.
func (c *Client) ReorderDeployment(ctx context.Context, id string, newPriority int) (DeploymentResponse, error) {
	var out DeploymentResponse
	body := struct {
		Priority int `json:"priority"`
	}{Priority: newPriority}
	return out, c.do(ctx, "POST", "/v1/deployments/"+id+"/reorder", body, &out)
}

// ClearDeployment soft-deletes one deployment (admin audit trail).
// Live deployments return 409 with the cancel-live hint; the IDOR gate
// is the standard resolveDeploymentAccount helper. Free-allowed.
func (c *Client) ClearDeployment(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/deployments/"+id, nil, nil)
}

// ClearObsoleteDeployments bulk soft-deletes terminal-but-not-current
// rows (status ∈ {superseded, failed, cancelled}) older than olderThan.
// Plan-gated (Free returns 402). The retention cap is enforced inside
// the store so INV 3 (always-current-deployment) stays satisfied.
func (c *Client) ClearObsoleteDeployments(ctx context.Context, appSlug string, olderThan time.Duration) (ClearObsoleteReport, error) {
	var out ClearObsoleteReport
	body := struct {
		OlderThan string `json:"older_than,omitempty"`
	}{OlderThan: olderThan.String()}
	return out, c.do(ctx, "POST", "/v1/apps/"+appSlug+"/deployments/clear-obsolete", body, &out)
}

// GetAppsDeploymentOpenAPIDoc returns the OpenAPI document the
// cold-boot probe captured for a deployment (issue #975 item #1 /
// ADR-122). The probe runs unconditionally; this endpoint surfaces
// the captured body only on paid plans — Free returns 402 +
// openapi_docs_not_allowed. The handler returns 404 when no doc has
// been captured yet (probe hasn't completed) OR when the deployment
// is owned by a different account (IDOR floor), so callers branch
// on errors.Is(err, api.ErrNotFound).
func (c *Client) GetAppsDeploymentOpenAPIDoc(ctx context.Context, slug, deployment string) (OpenAPIDocResponse, error) {
	var out OpenAPIDocResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/deployments/"+deployment+"/openapi", nil, &out)
}

// PatchAppsDeploymentOpenAPIDoc manually uploads (or overwrites) the
// OpenAPI document for a deployment. Body is the raw OpenAPI
// document — the server validates shape against Draft 2020-12 +
// OpenAPI 3.1 schema (vendored jsonschema v6.0.2) before persisting.
// Source must be the closed enum value "manual_upload". Returns 413
// if the doc exceeds Plan.OpenAPIDocMaxBytes() and 402 if the
// per-account Plan.OpenAPIDocsPerAccount() cap has been reached.
func (c *Client) PatchAppsDeploymentOpenAPIDoc(ctx context.Context, slug, deployment string, doc map[string]any, source string) (OpenAPIDocResponse, error) {
	body := map[string]any{"doc": doc, "source": source}
	var out OpenAPIDocResponse
	return out, c.do(ctx, "PATCH", "/v1/apps/"+slug+"/deployments/"+deployment+"/openapi", body, &out)
}

// DeleteAppsDeploymentOpenAPIDoc wipes the captured OpenAPI doc for
// a deployment. The next cold boot of the deployment re-captures a
// fresh body (the probe always runs). 402 on Free plan; 404 when
// the deployment has no captured doc or is owned by a different
// account.
func (c *Client) DeleteAppsDeploymentOpenAPIDoc(ctx context.Context, slug, deployment string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/deployments/"+deployment+"/openapi", nil, nil)
}

// GetAppOpenAPI returns the imported or auto-generated OpenAPI
// document for an app (issue #975 item #2 / ADR-126). The source
// query param selects between the customer's imported doc verbatim
// (`manual_import`, default) and the platform-merged spec
// (`auto` — imported doc ∪ observed routes ∪ existing edge rules).
// The body is the raw OpenAPI document; provenance lives in the
// X-OpenAPI-Doc-Source response header. Limits are abuse-surface
// (every plan including Free), per-account row cap is
// Plan.OpenAPIImportsPerAccount.
func (c *Client) GetAppOpenAPI(ctx context.Context, slug, source string) ([]byte, error) {
	q := url.Values{}
	if source != "" {
		q.Set("source", source)
	}
	u := "/v1/apps/" + slug + "/openapi"
	if encoded := q.Encode(); encoded != "" {
		u += "?" + encoded
	}
	var body []byte
	if err := c.doBytes(ctx, "GET", u, nil, &body); err != nil {
		return nil, err
	}
	return body, nil
}

// ImportAppOpenAPI uploads (or overwrites) the customer's OpenAPI
// document for an app. Body is the raw OpenAPI document; the
// server validates shape (Draft 2020-12 + OpenAPI 3.1 schema) +
// enforces size + endpoint caps before persisting. Returns the
// stored row metadata (uuid + source + version + counts +
// timestamps). 413 if the doc exceeds Plan.OpenAPIImportMaxDocBytes
// (state constant 256 KiB), 422 on validation / endpoint-cap
// failure, 403 on per-account quota.
//
// Wire format is the raw OpenAPI doc (no envelope) — the apid
// handler reads r.Body bytes verbatim and feeds them to
// openapiimport.ValidateImport. Wrapping in {"doc":...} would
// fail the meta-schema compile because the top-level shape
// would no longer be the OAI doc itself.
func (c *Client) ImportAppOpenAPI(ctx context.Context, slug string, doc map[string]any) (AppOpenAPIImportResponse, error) {
	var out AppOpenAPIImportResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/openapi", doc, &out)
}

// DryRunAppOpenAPI previews edge-rule suggestions for a candidate
// OpenAPI doc without persisting it. Same body shape as the import
// endpoint (raw OpenAPI document); returns one EdgeRuleSuggestion
// per (path, method) pair NOT already covered by an existing
// validate edge rule. Empty array when the doc is fully covered.
// Read-only — no pg_notify, no audit emit, no MFA requirement.
//
// Wire format mirrors ImportAppOpenAPI: raw OpenAPI doc, no
// envelope.
func (c *Client) DryRunAppOpenAPI(ctx context.Context, slug string, doc map[string]any) (AppOpenAPIImportDryRunResponse, error) {
	var out AppOpenAPIImportDryRunResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/openapi/dry-run", doc, &out)
}

// DeleteAppOpenAPI wipes the imported OpenAPI document for an app.
// Idempotent: returns 204 even if no row existed. Emits
// app.openapi_import.deleted audit + pg_notify on
// NotifyAppOpenAPIDocChanged so the auto-gen cache flushes.
func (c *Client) DeleteAppOpenAPI(ctx context.Context, slug string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/openapi", nil, nil)
}

// ListAppDebugRequests returns the recent per-app request-telemetry
// rows (status, latency_ms, route, deployment_id, trace_id,
// received_at) for slug (ADR-127 / PR-A). The endpoint is the
// read-side of the production-debugger data plane; the write-side
// (gateway publisher → apid gRPC IncrementRequestTelemetry →
// sqlc INSERT) lands in PR-B.
//
// since is a duration or 'Nd' alias (e.g. "30m", "24h", "3d") and
// is clamped server-side to the plan's DebugTelemetryRetentionDays
// (Free=off / 402; Hobby=3d; Pro=7d; Scale=14d). Empty falls
// back to the server's default (24h). The response envelope's
// `since` echoes the effective window applied, so a customer who
// asks for 30d on Hobby gets 3d back with the same payload.
//
// 402 when the plan gates the feature (DebugTelemetryEnabled=false);
// 404 when the app is owned by a different account (IDOR-safe
// byte-identical-404).
func (c *Client) ListAppDebugRequests(ctx context.Context, slug, since string) (DebugTelemetryListResponse, error) {
	var out DebugTelemetryListResponse
	path := "/v1/apps/" + slug + "/debug/requests"
	if since != "" {
		path += "?since=" + url.QueryEscape(since)
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// ListAppDebugRegressions returns the active regression
// observations for an app (ADR-127 / PR-B). Ordered by
// regression_factor DESC, last_detected_at DESC (worst first).
// `since` is clamped server-side to the plan's
// DebugTelemetryRetentionDays.
//
// 402 when the plan gates the feature (DebugTelemetryEnabled=false).
func (c *Client) ListAppDebugRegressions(ctx context.Context, slug, since string) (DebugRegressionsResponse, error) {
	var out DebugRegressionsResponse
	path := "/v1/apps/" + slug + "/debug/regressions"
	if since != "" {
		path += "?since=" + url.QueryEscape(since)
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// CompareAppDebugDeployments compares two deployments' per-route
// latency distributions in a shared window (ADR-127 / PR-B).
// `route` is optional (empty = all routes). `until` is optional
// (empty = now). The response shape is stable across the dashboard
// / API / CLI surfaces.
func (c *Client) CompareAppDebugDeployments(ctx context.Context, slug, source, mirror, route, since, until string) (DebugCompareResponse, error) {
	var out DebugCompareResponse
	path := "/v1/apps/" + slug + "/debug/compare"
	body := DebugCompareRequest{
		Source: source,
		Mirror: mirror,
		Route:  route,
		Since:  since,
		Until:  until,
	}
	return out, c.do(ctx, "POST", path, body, &out)
}

// ReplayAppDebugRequest queues a replay of a recorded request
// (ADR-127 / PR-B). PR-B returns a "queued" status — the
// mirror invocation pipeline lands in issue #72 PR-A2.
// Customer tooling can wire against the response shape today;
// the actual replay will land when PR-A2 ships.
func (c *Client) ReplayAppDebugRequest(ctx context.Context, slug, reqID string) (DebugReplayResponse, error) {
	var out DebugReplayResponse
	path := "/v1/apps/" + slug + "/debug/requests/" + reqID + "/replay"
	return out, c.do(ctx, "POST", path, nil, &out)
}

// --- Traffic mirroring (issue #72 / ADR-125 PR-A2) ---------------------
//
// Six thin wrappers over the /v1/apps/{slug}/mirrors CRUD surface.
// Method names follow the auto-derived convention cmd/sdk-coverage
// enforces (Verb + PascalCase(path-with-placeholders-stripped)) so
// the wire + SDK name surface stays in lockstep. All methods thread
// the authed client's bearer token through c.do so the server-side
// auth chain is identical to a hand-rolled HTTP call.

// PostAppsSlugMirrors creates a mirror rule on an app. The body is
// the canonical CreateMirrorRuleRequest; the server returns the
// stored MirrorRuleResponse (with id, always_stripped_headers
// manifest, and the CreatedAt/UpdatedAt stamps).
func (c *Client) PostAppsSlugMirrors(ctx context.Context, slug string, req CreateMirrorRuleRequest) (MirrorRuleResponse, error) {
	var out MirrorRuleResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/mirrors", req, &out)
}

// GetAppsSlugMirrors returns every rule on the app (enabled or
// not). At most Limits.MirrorTargetsPerApp rows so no cursor is
// needed in A2 (1-3 rows).
func (c *Client) GetAppsSlugMirrors(ctx context.Context, slug string) (MirrorRuleListResponse, error) {
	var out MirrorRuleListResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/mirrors", nil, &out)
}

// GetAppsSlugMirrorsId loads one rule by id. The server enforces
// the IDOR posture (silent 404 on cross-account); the SDK does not
// translate — the caller sees the raw 404.
func (c *Client) GetAppsSlugMirrorsId(ctx context.Context, slug, id string) (MirrorRuleResponse, error) {
	var out MirrorRuleResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/mirrors/"+id, nil, &out)
}

// PatchAppsSlugMirrorsId applies a partial update. The pointer
// fields on UpdateMirrorRuleRequest let the caller distinguish
// "absent" from "set to zero" — Percent=0 is legal (disable without
// removing) and distinct from omitting the field.
func (c *Client) PatchAppsSlugMirrorsId(ctx context.Context, slug, id string, req UpdateMirrorRuleRequest) (MirrorRuleResponse, error) {
	var out MirrorRuleResponse
	return out, c.do(ctx, "PATCH", "/v1/apps/"+slug+"/mirrors/"+id, req, &out)
}

// DeleteAppsSlugMirrorsId removes the rule. The server returns 204;
// downstream mirror_invocation_results rows cascade via FK ON
// DELETE CASCADE (migration 00384_mirror_rules.sql).
func (c *Client) DeleteAppsSlugMirrorsId(ctx context.Context, slug, id string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/mirrors/"+id, nil, nil)
}

// GetAppsSlugMirrorsIdSummary returns the aggregate drift counts
// over the requested window. windowStr must be one of "1h" / "24h"
// / "7d"; the server returns 422 invalid_mirror_window on anything
// else.
func (c *Client) GetAppsSlugMirrorsIdSummary(ctx context.Context, slug, id, windowStr string) (MirrorSummaryResponse, error) {
	var out MirrorSummaryResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/mirrors/"+id+"/summary?window="+windowStr, nil, &out)
}

// --- CORS presets (issue #975 item #4 / ADR-129) -------------------------
//
// Customer-owned, named, reusable CORS configurations. The data
// model shipped in PR-A (migration 00304_cors_presets.sql); the
// write surface lands in PR-B. A preset is wired into a kind=cors
// edge rule via EdgeRuleCORSAction.cors_preset_id; the gateway
// merge happens at compile time (state.MergeCorsPresetIntoRule).
//
// Plan gates: Free → 402 plan_cors_preset_not_allowed (the cap
// is 0); Hobby/Pro/Scale → 403 plan_cors_preset_quota_reached
// at the per-account / per-app quota. The wire-level codes are
// surfaced verbatim on *api.Problem; the SDK does not retry.

// ListCorsPresets returns every cors_presets row the caller's
// account owns (account-wide + every app-scoped row). The
// optional appID argument scopes the listing to a single app's
// presets; empty = union of account-wide + every app-scoped
// row. No pagination (the per-account quota caps the row count).
func (c *Client) ListCorsPresets(ctx context.Context, appID string) (CorsPresetListResponse, error) {
	var out CorsPresetListResponse
	path := "/v1/cors-presets"
	if appID != "" {
		path += "?app_id=" + url.QueryEscape(appID)
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// CreateCorsPreset attaches a new cors_presets row to the
// caller's account. AppID is *string on the wire (nil =
// account-wide, non-nil = app-scoped); the SDK takes the
// pointer verbatim so JSON null can be distinguished from an
// omitted field.
//
// Pre-loadApp gates fire in this order: 402
// plan_cors_preset_not_allowed on the Free-tier cap-0 → 422
// cors_preset_invalid on body shape → 404 on a cross-tenant
// app_id → 402 plan_cors_preset_quota_reached on the cap → 409
// cors_preset_name_conflict on duplicate
// (account_id, COALESCE(app_id, '00..00'), name).
func (c *Client) CreateCorsPreset(ctx context.Context, req CreateCorsPresetRequest) (CorsPresetResponse, error) {
	var out CorsPresetResponse
	return out, c.do(ctx, "POST", "/v1/cors-presets", req, &out)
}

// GetCorsPreset fetches one cors_presets row by id. IDOR-safe:
// a foreign account's id collapses to 404, not 403 — same
// convention as GetEdgeRule and GetAlertRule.
func (c *Client) GetCorsPreset(ctx context.Context, id string) (CorsPresetResponse, error) {
	var out CorsPresetResponse
	return out, c.do(ctx, "GET", "/v1/cors-presets/"+id, nil, &out)
}

// UpdateCorsPreset applies a partial update (nil-skip
// convention). At-least-one-field is enforced server-side;
// an empty PATCH body returns 422
// cors_preset_update_requires_field. AppID is **string
// tri-state: outer nil = "do not touch", inner nil = "set to
// NULL (account-wide)", inner non-nil = "set to UUID
// (app-scoped)" — the wire-format is the same as the create
// shape so the SDK passes the value through verbatim.
//
// The pgstore trigger fires pg_notify('cors_preset_changed',
// account_id) AFTER the UPDATE commits; gatewayd-internal
// reloads the affected account's preset overlay (ADR-129 D4).
func (c *Client) UpdateCorsPreset(ctx context.Context, id string, req UpdateCorsPresetRequest) (CorsPresetResponse, error) {
	var out CorsPresetResponse
	return out, c.do(ctx, "PATCH", "/v1/cors-presets/"+id, req, &out)
}

// DeleteCorsPreset removes the cors_presets row. The FK ON
// DELETE SET NULL on edge_rules.cors_preset_id clears every
// referencing rule's FK atomically with the preset's deletion;
// the gatewayd-internal compile path fails closed
// (MergeCorsPresetIntoRule returns ErrNotFound) until the
// customer wires a new preset or inlines fallback values.
// Returns nil on 204.
func (c *Client) DeleteCorsPreset(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/cors-presets/"+id, nil, nil)
}

// RunWorkflow (ADR-081) triggers a new workflow execution run for an app.
func (c *Client) RunWorkflow(ctx context.Context, slug, workflowName string, input json.RawMessage) (WorkflowRunResponse, error) {
	var resp WorkflowRunResponse
	path := fmt.Sprintf("/v1/apps/%s/workflows/%s/runs", slug, workflowName)
	err := c.do(ctx, "POST", path, input, &resp)
	return resp, err
}

// ListWorkflowRuns (ADR-081) lists workflow runs for an app.
func (c *Client) ListWorkflowRuns(ctx context.Context, slug string, limit, offset int, status string) (ListWorkflowRunsResponse, error) {
	var resp ListWorkflowRunsResponse
	path := fmt.Sprintf("/v1/apps/%s/workflows/runs?limit=%d&offset=%d", slug, limit, offset)
	if status != "" {
		path += "&status=" + status
	}
	err := c.do(ctx, "GET", path, nil, &resp)
	return resp, err
}

// GetWorkflowRun (ADR-081) retrieves the state of a workflow run.
func (c *Client) GetWorkflowRun(ctx context.Context, runID string) (WorkflowRunResponse, error) {
	var resp WorkflowRunResponse
	err := c.do(ctx, "GET", "/v1/workflows/runs/"+runID, nil, &resp)
	return resp, err
}

// ListWorkflowSteps (ADR-081) lists step records for a workflow run.
func (c *Client) ListWorkflowSteps(ctx context.Context, runID string) (ListWorkflowStepsResponse, error) {
	var resp ListWorkflowStepsResponse
	err := c.do(ctx, "GET", "/v1/workflows/runs/"+runID+"/steps", nil, &resp)
	return resp, err
}

// SendWorkflowEvent (ADR-081) injects an external event into a workflow run.
func (c *Client) SendWorkflowEvent(ctx context.Context, runID, eventName string, payload json.RawMessage) (InjectWorkflowEventResponse, error) {
	var resp InjectWorkflowEventResponse
	req := InjectWorkflowEventRequest{EventName: eventName, Payload: payload}
	err := c.do(ctx, "POST", "/v1/workflows/runs/"+runID+"/events", req, &resp)
	return resp, err
}

// CancelWorkflowRun (ADR-081) cancels an in-flight workflow run.
func (c *Client) CancelWorkflowRun(ctx context.Context, runID string) (WorkflowRunResponse, error) {
	var resp WorkflowRunResponse
	err := c.do(ctx, "POST", "/v1/workflows/runs/"+runID+"/cancel", nil, &resp)
	return resp, err
}
