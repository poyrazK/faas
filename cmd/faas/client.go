package main

import (
	"bytes"
	"context"
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

// Client is a thin typed wrapper over the v1 REST API. It renders the API's
// RFC 7807 problems into the CLI's three-line error shape (UX §3.3) rather than
// inventing copy.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient builds a client for baseURL with a bearer token.
func NewClient(baseURL, token string) *Client {
	return &Client{baseURL: baseURL, token: token, http: &http.Client{Timeout: 30 * time.Second}}
}

// APIError carries a server problem for the CLI to render.
type APIError struct{ Problem api.Problem }

func (e *APIError) Error() string {
	p := e.Problem
	if p.DocsURL != "" {
		return fmt.Sprintf("%s\n  %s\n  → %s", p.Title, p.Detail, p.DocsURL)
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

// DeployTarball ships a source tarball (with optional runtime + handler) to
// the multi-part deploy endpoint. The apid handler validates the archive and
// emits `pg_notify('build_queued', ...)` for imaged to pick up.
func (c *Client) DeployTarball(ctx context.Context, slug, path, runtime, handler string, dockerfile bool) (api.DeploymentResponse, error) {
	f, err := os.Open(path)
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
	resp, err := c.http.Do(req)
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
