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
	"net/http"
	"time"
)

// Client is a typed wrapper over the v1 REST API. Construct with
// NewClient (30s default timeout) or NewClientWithDeployTimeout
// (longer upload timeout). Pass-through to net/http for SSE streams is
// configured internally; see logs.go.
type Client struct {
	baseURL string
	token   string

	http       *http.Client // 30s default — used for every JSON call
	deployHTTP *http.Client // optional, used by DeployMultipart
}

// NewClient builds a client for baseURL with the given bearer token.
// An empty token disables Authorization (useful for the anonymous
// device-code endpoints).
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
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

// Token returns the bearer token (empty for anonymous clients).
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
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
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
	var out AccountDeletionResponse
	req, _ := http.NewRequestWithContext(ctx, "DELETE", c.baseURL+"/v1/account", nil)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	} else {
		req.Header.Set("Idempotency-Key", newUUIDv4())
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return out, fmt.Errorf("could not reach the API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var p Problem
		if json.Unmarshal(body, &p) == nil && p.Code != "" {
			return out, &APIError{Problem: p}
		}
		return out, fmt.Errorf("API error: %s", resp.Status)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

// RestoreAccount cancels a pending deletion (spec §17 G6).
func (c *Client) RestoreAccount(ctx context.Context) (AccountResponse, error) {
	var out AccountResponse
	return out, c.do(ctx, "POST", "/v1/account/restore", nil, &out)
}

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
	// DeployMultipart bypasses Client.do; auto-mint Idempotency-Key here
	// so retry-safe semantics still hold. The file-open guard (if any)
	// runs at the caller before this mint, so a rejected path never
	// produces an Idempotency-Key on the wire.
	req.Header.Set("Idempotency-Key", newUUIDv4())
	resp, err := c.uploadHTTP().Do(req)
	if err != nil {
		return DeploymentResponse{}, fmt.Errorf("could not reach the API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var p Problem
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			return DeploymentResponse{}, &APIError{Problem: p}
		}
		return DeploymentResponse{}, fmt.Errorf("API error: %s", resp.Status)
	}
	var out DeploymentResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return DeploymentResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
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

// ChangePlan changes the account's subscription tier.
func (c *Client) ChangePlan(ctx context.Context, plan string) (AccountResponse, error) {
	var out AccountResponse
	return out, c.do(ctx, "PATCH", "/v1/account/plan",
		map[string]string{"plan": plan}, &out)
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

// Crons.
func (c *Client) ListCrons(ctx context.Context, slug string) ([]CronResponse, error) {
	var out []CronResponse
	return out, c.do(ctx, "GET", "/v1/crons?slug="+slug, nil, &out)
}
func (c *Client) CreateCron(ctx context.Context, slug string, req CreateCronRequest) (CronResponse, error) {
	var out CronResponse
	return out, c.do(ctx, "POST", "/v1/crons", req, &out)
}
func (c *Client) DeleteCron(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/crons/"+id, nil, nil)
}

// API keys.
func (c *Client) ListKeys(ctx context.Context) ([]APIKeyResponse, error) {
	var out []APIKeyResponse
	return out, c.do(ctx, "GET", "/v1/keys", nil, &out)
}
func (c *Client) CreateKey(ctx context.Context, label string) (APIKeyResponse, error) {
	var out APIKeyResponse
	return out, c.do(ctx, "POST", "/v1/keys", map[string]string{"label": label}, &out)
}
func (c *Client) DeleteKey(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/keys/"+id, nil, nil)
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

// Secrets (spec §11/G2). Plaintext VALUE never leaves the caller
// except via SetSecret's body.
func (c *Client) ListSecrets(ctx context.Context, slug string) (AppSecretListResponse, error) {
	var out AppSecretListResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/secrets", nil, &out)
}
func (c *Client) SetSecret(ctx context.Context, slug, key, value string) error {
	return c.do(ctx, "PUT", "/v1/apps/"+slug+"/secrets/"+key,
		PutAppSecretRequest{Value: value}, nil)
}
func (c *Client) UnsetSecret(ctx context.Context, slug, key string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/secrets/"+key, nil, nil)
}

// Usage.
func (c *Client) GetUsage(ctx context.Context, month string) (UsageResponse, error) {
	var out UsageResponse
	return out, c.do(ctx, "GET", "/v1/usage?month="+month, nil, &out)
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
