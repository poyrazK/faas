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
)

// cookieOnlyPathRE matches API routes that are gated server-side to
// the dashboard session cookie. The bearer-key CLI cannot reach
// them — a request would 401 (or 302 to the login page) because the
// session-cookie middleware (cmd/apid/server.go:1097 and
// cmd/apid/handlers_sessions.go:71-77) treats bearer-key callers
// as anonymous. The guard in c.do (below) short-circuits the
// request with CodeUnsupportedByCLI so the failure mode is honest
// ("the CLI cannot reach this route") rather than a confusing
// 401/302. The companion tripwire
// (pkg/api/lint_tripwires_test.go) ensures no other pkg/api file
// composes a path that matches this regex.
var cookieOnlyPathRE = regexp.MustCompile(`^/v1/auth/(sessions|capabilities)(/.*)?$`)

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
		http:    &http.Client{Timeout: 30 * time.Second},
		cache:   NewCompletionCache(),
	}
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
		c.deployHTTP = &http.Client{Timeout: deployTimeout}
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
	// Cookie-only-route guard — reject paths the bearer-key CLI cannot
	// reach before allocating anything. The regex matches the closed
	// set /v1/auth/sessions and /v1/auth/capabilities (with optional
	// trailing subpath). The status is 403 because the call is
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
	if method != http.MethodGet && method != http.MethodHead && req.Header.Get("Idempotency-Key") == "" {
		req.Header.Set("Idempotency-Key", newUUIDv4())
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
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var p Problem
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			return &APIError{Problem: p}
		}
		return fmt.Errorf("API error: %s", resp.Status)
	}
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
func (c *Client) DeployMultipart(ctx context.Context, slug string, source io.Reader, sourceName, runtime, handler string, dockerfile bool) (DeploymentResponse, error) {
	var b bytes.Buffer
	w := newMultipartWriter(&b, slug, dockerfile, runtime, handler)
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

// ScanProject ships a source tarball to the dry-run endpoint. The
// response carries the discovered workloads, managed services,
// derived scan_source, and a plan_token that ApplyProjectPlan can
// echo back on the same multipart body to skip the second extract
// in the interactive flow. No writes — POST /v1/projects/scan.
func (c *Client) ScanProject(
	ctx context.Context,
	source io.Reader, sourceName, projectSlug, productionBranch string,
	installID int64, only []string,
) (PlanResponse, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if err := writeProjectMultipartFields(w, source, sourceName, projectSlug, productionBranch, installID, only); err != nil {
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
	installID int64, only []string,
) (ApplyResponse, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if err := writeProjectMultipartFields(w, source, sourceName, projectSlug, productionBranch, installID, only); err != nil {
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

// writeProjectMultipartFields serializes the multipart body shared
// by ScanProject + ApplyProjectPlan. The fields exactly mirror the
// OpenAPI ProjectScanRequest schema (the spec-compliance AST gate
// enforces the field-for-field mapping).
func writeProjectMultipartFields(
	w *multipart.Writer, source io.Reader, sourceName, projectSlug,
	productionBranch string, installID int64, only []string,
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
	path := "/v1/instances"
	if before != "" || limit > 0 {
		path += "?"
		if before != "" {
			path += "before=" + before
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

// ListCrons returns every cron on the account when slug is empty,
// or every cron for the given app when slug is non-empty. The slug
// filter is added to the wire only when non-empty so the request
// matches the spec (zero documented parameters) and the server-side
// listCrons handler returns 200 with the full account-scoped list.
func (c *Client) ListCrons(ctx context.Context, slug string) ([]CronResponse, error) {
	path := "/v1/crons"
	if slug != "" {
		path += "?slug=" + slug
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

// SetPassword updates the password on the currently authenticated
// account. Reachable only after Bearer auth (the dashboard session
// cookie is interchangeable with the bearer token via
// sessionAuthFor). Used by OAuth-only customers to opt into password
// login.
func (c *Client) SetPassword(ctx context.Context, password string) error {
	return c.do(ctx, "POST", "/dashboard/account/set-password",
		SetPasswordRequest{Password: password}, nil)
}

// Logout clears the dashboard session. Idempotent — clearing a
// non-existent session is a no-op.
func (c *Client) Logout(ctx context.Context) error {
	return c.do(ctx, "POST", "/logout", nil, nil)
}

// Secrets (spec §11/G2). Plaintext VALUE never leaves the caller
// except via SetSecret's body.
func (c *Client) ListSecrets(ctx context.Context, slug string) (AppSecretListResponse, error) {
	var out AppSecretListResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/secrets", nil, &out)
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
	return c.do(ctx, "PUT", "/v1/apps/"+slug+"/secrets/"+key,
		PutAppSecretRequest{Value: value}, nil)
}
func (c *Client) UnsetSecret(ctx context.Context, slug, key string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/secrets/"+key, nil, nil)
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
	return out, c.do(ctx, "GET", "/v1/usage?month="+month, nil, &out)
}

// GetAppMetrics returns the per-app metrics snapshot for slug over
// the named range window. rng is one of "5m", "15m", "1h", "6h",
// "24h", "7d", "15d" — empty falls back to the server's default
// (5m). Issue #273 / ADR-042.
func (c *Client) GetAppMetrics(ctx context.Context, slug, rng string) (AppMetricsResponse, error) {
	var out AppMetricsResponse
	path := "/v1/apps/" + slug + "/metrics"
	if rng != "" {
		path += "?range=" + rng
	}
	return out, c.do(ctx, "GET", path, nil, &out)
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
		path += "?range=" + rng
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
		path += "?window=" + window
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
		path += "?window=" + window
	}
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
		path += "?month=" + month
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// UsageDaily returns the per-(app, day) rollup rows the meterd rollup
// loop populated into usage_daily (ADR-048 §5). day is "YYYY-MM-DD"
// and is required; the server 400s on empty so callers don't get
// ambiguous "current day" semantics from the SDK side.
func (c *Client) UsageDaily(ctx context.Context, day string) (DailyUsageListResponse, error) {
	var out DailyUsageListResponse
	return out, c.do(ctx, "GET", "/v1/usage/daily?day="+day, nil, &out)
}

// StorageUsage returns the per-(app, day) snapshot+layer byte rollup
// (ADR-049 §B.3). day is "YYYY-MM-DD" and is required; the server
// 400s on empty. Informational only — not billed today.
func (c *Client) StorageUsage(ctx context.Context, day string) (StorageUsageListResponse, error) {
	var out StorageUsageListResponse
	return out, c.do(ctx, "GET", "/v1/usage/storage?day="+day, nil, &out)
}

// ListDeployments returns a single page of deployments with a
// "next_before" cursor (RFC3339Nano). Use ListDeploymentsAll (added in
// commit 2) to walk every page automatically.
func (c *Client) ListDeployments(ctx context.Context, before string, limit int) (DeploymentListResponse, error) {
	var out DeploymentListResponse
	path := "/v1/deployments?"
	if before != "" {
		path += "before=" + before + "&"
	}
	if limit > 0 {
		path += "limit=" + fmt.Sprintf("%d", limit)
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetBillingPortal returns the operator-configured Stripe billing
// portal URL for the authenticated account (issue #253). Empty string
// means the box has FAAS_BILLING_PORTAL_URL unset — the CLI prints a
// friendly hint instead of opening the browser to "". The endpoint
// is authenticated via the standard Bearer / API-key chain (same
// surface as usage reads).
func (c *Client) GetBillingPortal(ctx context.Context) (string, error) {
	var out BillingPortalResponse
	if err := c.do(ctx, "GET", "/v1/billing/portal", nil, &out); err != nil {
		return "", err
	}
	return out.URL, nil
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
	if before != "" || limit > 0 {
		path += "?"
		sep := ""
		if before != "" {
			path += "before=" + before
			sep = "&"
		}
		if limit > 0 {
			path += sep + "limit=" + strconv.Itoa(limit)
		}
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
// Four methods backing the operator-facing CLI subcommands:
//
//	ListPaddleCatalog  — GET    /v1/admin/billing-paddle-catalog
//	SyncPaddleCatalog  — POST   /v1/admin/billing-paddle-catalog/sync
//	ResetPaddleCatalog — DELETE /v1/admin/billing-paddle-catalog
//	ReconcileAccount   — POST   /v1/admin/billing-reconcile/{id}
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
