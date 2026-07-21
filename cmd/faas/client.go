package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// newUUIDv4 returns an RFC 4122 v4 UUID string. Inline to avoid a uuid
// dependency; spec §4.2 only requires "any v4 UUID" — the shape only
// matters for the server-side Idempotency-Key cache key (apid/server.go).
func newUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	s := hex.EncodeToString(b[:])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

// Client is a thin typed wrapper over the v1 REST API. It renders the API's
// RFC 7807 problems into the CLI's three-line error shape (UX §3.3) rather than
// inventing copy.
type Client struct {
	baseURL    string
	token      string
	http       *http.Client
	deployHTTP *http.Client // nil → uploadHTTP() returns http
}

// NewClient builds a client for baseURL with a bearer token.
func NewClient(baseURL, token string) *Client {
	return &Client{baseURL: baseURL, token: token, http: &http.Client{Timeout: 30 * time.Second}}
}

// NewClientWithDeployTimeout is like NewClient but configures a longer
// timeout on the upload HTTP client. Used by cmdDeployTarball so a
// multi-MB tarball doesn't trip the 30s default (issue #64 D4). A
// non-positive timeout falls back to the default 30s client.
func NewClientWithDeployTimeout(baseURL, token string, deployTimeout time.Duration) *Client {
	c := NewClient(baseURL, token)
	if deployTimeout > 0 {
		c.deployHTTP = &http.Client{Timeout: deployTimeout}
	}
	return c
}

// uploadHTTP returns the http client for upload (longer timeout) or
// the default one. Used by DeployTarball.
func (c *Client) uploadHTTP() *http.Client {
	if c.deployHTTP != nil {
		return c.deployHTTP
	}
	return c.http
}

// APIError carries a server problem for the CLI to render.
type APIError struct{ Problem api.Problem }

func (e *APIError) Error() string {
	p := e.Problem
	docs := p.DocsURL
	// UX §3.3 requires three lines always. Synthesise the docs URL from
	// the stable Code when the server omits DocsURL (which happens for
	// problems reconstructed from a bare gRPC status, or codes the
	// constructor chain doesn't decorate with WithDocs).
	if docs == "" && p.Code != "" {
		docs = docsURLForCode(p.Code)
	}
	if docs != "" {
		return fmt.Sprintf("%s\n  %s\n  → %s", p.Title, p.Detail, docs)
	}
	return fmt.Sprintf("%s\n  %s", p.Title, p.Detail)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
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
	// or double-creates. The server middleware at apid/server.go dedupes
	// on the header when present (24h replay). We never override an
	// explicit key the caller already set.
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
		var p api.Problem
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			return &APIError{Problem: p}
		}
		return fmt.Errorf("API error: %s", resp.Status)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Whoami returns the authenticated account.
func (c *Client) Whoami(ctx context.Context) (api.AccountResponse, error) {
	var out api.AccountResponse
	return out, c.do(ctx, "GET", "/v1/account", nil, &out)
}

// ExportAccount downloads the GDPR export bundle (spec §17 G6).
// outPath is the file to write; includeSecrets=false drops the
// ciphertext slice. Streams the response body straight to disk so a
// large bundle doesn't load into memory.
func (c *Client) ExportAccount(ctx context.Context, outPath string, includeSecrets bool) error {
	path := "/v1/account/export"
	if !includeSecrets {
		path += "?include_secrets=false"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		var p api.Problem
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if json.Unmarshal(body, &p) == nil && p.Code != "" {
			return &APIError{Problem: p}
		}
		return fmt.Errorf("API error: %s", resp.Status)
	}
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("could not open output file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("write failed: %w", err)
	}
	return nil
}

// DeleteAccount schedules the account for deletion (spec §17 G6).
// idempotencyKey is forwarded as Idempotency-Key so retry-safe
// clients (CI, dashboard) get the same envelope back across retries.
func (c *Client) DeleteAccount(ctx context.Context, idempotencyKey string) (api.AccountDeletionResponse, error) {
	var out api.AccountDeletionResponse
	req, _ := http.NewRequestWithContext(ctx, "DELETE", c.baseURL+"/v1/account", nil)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	} else {
		// Caller didn't supply a key — auto-mint so the server middleware
		// can dedupe retries (issue #64 D3). The explicit-key path keeps
		// the `cli-delete-…` prefix for traceability.
		req.Header.Set("Idempotency-Key", newUUIDv4())
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return out, fmt.Errorf("could not reach the API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var p api.Problem
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

// RestoreAccount cancels a pending deletion (spec §17 G6). Returns
// the refreshed AccountResponse so the CLI can print "Welcome back
// to the <plan> plan".
func (c *Client) RestoreAccount(ctx context.Context) (api.AccountResponse, error) {
	var out api.AccountResponse
	return out, c.do(ctx, "POST", "/v1/account/restore", nil, &out)
}

// ListApps returns the account's apps.
func (c *Client) ListApps(ctx context.Context) ([]api.AppResponse, error) {
	var out []api.AppResponse
	return out, c.do(ctx, "GET", "/v1/apps", nil, &out)
}

// CreateApp creates an app.
func (c *Client) CreateApp(ctx context.Context, req api.CreateAppRequest) (api.AppResponse, error) {
	var out api.AppResponse
	return out, c.do(ctx, "POST", "/v1/apps", req, &out)
}

// Deploy creates a deployment for an app slug.
func (c *Client) Deploy(ctx context.Context, slug string, req api.CreateDeploymentRequest) (api.DeploymentResponse, error) {
	var out api.DeploymentResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/deployments", req, &out)
}

// GetDeployment returns a deployment by ID. Used by cmdDeployTarball
// to poll terminal status after the SSE log stream closes (issue #64 D4).
func (c *Client) GetDeployment(ctx context.Context, id string) (api.DeploymentResponse, error) {
	var out api.DeploymentResponse
	return out, c.do(ctx, "GET", "/v1/deployments/"+id, nil, &out)
}

// DeployTarball ships a source tarball (with optional runtime + handler) to
// the multi-part deploy endpoint. The apid handler validates the archive and
// emits `pg_notify('build_queued', ...)` for imaged to pick up.
//
// Refuses to open symlinks or non-regular files (see openCustomerFile in
// commands5.go); a rejected path returns an error before any wire traffic
// is generated, so a symlinked tarball cannot exfiltrate bytes the
// customer did not intend to ship.
func (c *Client) DeployTarball(ctx context.Context, slug, path, runtime, handler string, dockerfile bool) (api.DeploymentResponse, error) {
	f, err := openCustomerFile(path)
	if err != nil {
		return api.DeploymentResponse{}, err
	}
	defer func() { _ = f.Close() }()

	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	_ = w.WriteField("slug", slug)
	if dockerfile {
		_ = w.WriteField("dockerfile", "true")
	}
	if runtime != "" {
		_ = w.WriteField("runtime", runtime)
	}
	if handler != "" {
		_ = w.WriteField("handler", handler)
	}
	fw, err := w.CreateFormFile("source", filepath.Base(path))
	if err != nil {
		return api.DeploymentResponse{}, err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return api.DeploymentResponse{}, err
	}
	_ = w.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/apps/"+slug+"/deployments", &b)
	if err != nil {
		return api.DeploymentResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", w.FormDataContentType())
	// DeployTarball bypasses Client.do; auto-mint Idempotency-Key here
	// so retry-safe semantics still hold (issue #64 D3). The file-open
	// guard (openCustomerFile in commands5.go) runs above this mint, so
	// a rejected symlink never produces an Idempotency-Key on the wire.
	req.Header.Set("Idempotency-Key", newUUIDv4())
	// Use the longer-timeout client for uploads so a multi-MB tarball
	// doesn't trip the 30s default (issue #64 D4). uploadHTTP falls back
	// to the regular client when no deploy timeout was configured.
	resp, err := c.uploadHTTP().Do(req)
	if err != nil {
		return api.DeploymentResponse{}, fmt.Errorf("could not reach the API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var p api.Problem
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			return api.DeploymentResponse{}, &APIError{Problem: p}
		}
		return api.DeploymentResponse{}, fmt.Errorf("API error: %s", resp.Status)
	}
	var out api.DeploymentResponse
	return out, json.Unmarshal(data, &out)
}

// GetApp returns the app metadata for a slug.
func (c *Client) GetApp(ctx context.Context, slug string) (api.AppResponse, error) {
	var out api.AppResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug, nil, &out)
}

// UpdateApp applies a partial update to an app.
func (c *Client) UpdateApp(ctx context.Context, slug string, req api.UpdateAppRequest) (api.AppResponse, error) {
	var out api.AppResponse
	return out, c.do(ctx, "PATCH", "/v1/apps/"+slug, req, &out)
}

// RenameApp swaps an app's slug atomically (issue #63). The server
// returns 409 CodeAppRenameFailed on slug collisions; client.do
// surfaces that as APIError so the CLI can render the RFC 7807 body.
func (c *Client) RenameApp(ctx context.Context, oldSlug, newSlug string) (api.AppResponse, error) {
	var out api.AppResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+oldSlug+"/rename",
		api.RenameAppRequest{NewSlug: newSlug}, &out)
}

// DeleteApp removes an app.
func (c *Client) DeleteApp(ctx context.Context, slug string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug, nil, nil)
}

// ChangePlan changes the account's subscription tier (issue #63). The
// endpoint is account-scoped (PATCH /v1/account/plan); the CLI exposes
// it as a top-level `faas plan <plan>` because plan changes are not
// per-app (see ux_spec §3.1).
func (c *Client) ChangePlan(ctx context.Context, plan string) (api.AccountResponse, error) {
	var out api.AccountResponse
	return out, c.do(ctx, "PATCH", "/v1/account/plan",
		map[string]string{"plan": plan}, &out)
}

// GetStatusSLO fetches the public SLO snapshot (issue #63). The route
// is unauthenticated by design — the CLI still sends the bearer token
// if present (apid ignores it on this route).
func (c *Client) GetStatusSLO(ctx context.Context) (api.StatusPage, error) {
	var out api.StatusPage
	return out, c.do(ctx, "GET", "/status/slo.json", nil, &out)
}

// Rollback re-promotes the most recent superseded deployment.
func (c *Client) Rollback(ctx context.Context, slug string) (api.DeploymentResponse, error) {
	var out api.DeploymentResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/rollback", nil, &out)
}

// Park and Wake toggle the app between cold-parked and live (spec §4.3).
func (c *Client) Park(ctx context.Context, slug string) error {
	return c.do(ctx, "POST", "/v1/apps/"+slug+"/park", nil, nil)
}
func (c *Client) Wake(ctx context.Context, slug string) error {
	return c.do(ctx, "POST", "/v1/apps/"+slug+"/wake", nil, nil)
}
func (c *Client) ListInstances(ctx context.Context, slug string) ([]api.InstanceResponse, error) {
	var out []api.InstanceResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/instances", nil, &out)
}

// Domains
func (c *Client) ListDomains(ctx context.Context) ([]api.CustomDomainResponse, error) {
	var out []api.CustomDomainResponse
	return out, c.do(ctx, "GET", "/v1/domains", nil, &out)
}
func (c *Client) CreateDomain(ctx context.Context, req api.CreateCustomDomainRequest) (api.CustomDomainResponse, error) {
	var out api.CustomDomainResponse
	return out, c.do(ctx, "POST", "/v1/domains", req, &out)
}
func (c *Client) DeleteDomain(ctx context.Context, domain string) error {
	return c.do(ctx, "DELETE", "/v1/domains/"+domain, nil, nil)
}

// Crons
func (c *Client) ListCrons(ctx context.Context, slug string) ([]api.CronResponse, error) {
	var out []api.CronResponse
	return out, c.do(ctx, "GET", "/v1/crons?slug="+slug, nil, &out)
}
func (c *Client) CreateCron(ctx context.Context, slug string, req api.CreateCronRequest) (api.CronResponse, error) {
	var out api.CronResponse
	return out, c.do(ctx, "POST", "/v1/crons", req, &out)
}
func (c *Client) DeleteCron(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/crons/"+id, nil, nil)
}

// API keys
func (c *Client) ListKeys(ctx context.Context) ([]api.APIKeyResponse, error) {
	var out []api.APIKeyResponse
	return out, c.do(ctx, "GET", "/v1/keys", nil, &out)
}
func (c *Client) CreateKey(ctx context.Context, label string) (api.APIKeyResponse, error) {
	var out api.APIKeyResponse
	return out, c.do(ctx, "POST", "/v1/keys", map[string]string{"label": label}, &out)
}
func (c *Client) DeleteKey(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/keys/"+id, nil, nil)
}

// CLI auth device-code flow (spec §2.2). Both endpoints are
// anonymous — the CLI hasn't logged in yet, so the client is built
// with token="". The mint is a single round-trip with no body; the
// exchange is the CLI's polling endpoint.

// MintCliAuthCode anonymously mints a fresh device code. The
// returned URL is what the CLI opens in the browser; the code is
// the human-readable fallback for paste-mode (no browser).
func (c *Client) MintCliAuthCode(ctx context.Context) (api.CliAuthCodeResponse, error) {
	var out api.CliAuthCodeResponse
	return out, c.do(ctx, "POST", "/v1/cli-auth/code", struct{}{}, &out)
}

// ExchangeCliAuthCode polls the server for the user's approval. The
// caller treats a 404 cli_auth_code_pending response as "keep
// waiting" — the server-side helper keeps the connection short and
// signals via stable RFC 7807 code (api.CodeCliAuthPending) so the
// CLI can switch on it without parsing prose.
func (c *Client) ExchangeCliAuthCode(ctx context.Context, code string) (api.CliAuthExchangeResponse, error) {
	var out api.CliAuthExchangeResponse
	return out, c.do(ctx, "POST", "/v1/cli-auth/exchange",
		api.CliAuthExchangeRequest{Code: code}, &out)
}

// Secrets (spec §11/G2). Plaintext VALUE never leaves the CLI except via
// the PUT body; the LIST response carries key names + timestamps only.
func (c *Client) ListSecrets(ctx context.Context, slug string) (api.AppSecretListResponse, error) {
	var out api.AppSecretListResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/secrets", nil, &out)
}
func (c *Client) SetSecret(ctx context.Context, slug, key, value string) error {
	return c.do(ctx, "PUT", "/v1/apps/"+slug+"/secrets/"+key,
		api.PutAppSecretRequest{Value: value}, nil)
}
func (c *Client) UnsetSecret(ctx context.Context, slug, key string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/secrets/"+key, nil, nil)
}

// Usage
func (c *Client) GetUsage(ctx context.Context, month string) (api.UsageResponse, error) {
	var out api.UsageResponse
	return out, c.do(ctx, "GET", "/v1/usage?month="+month, nil, &out)
}
