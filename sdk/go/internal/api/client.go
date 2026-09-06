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
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

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
	// token is guarded by tokenMu so long-lived daemons can rotate
	// the bearer via SetToken without racing concurrent request
	// builders. RWMutex because reads dominate writes — every
	// outbound request reads; rotation is rare.
	tokenMu sync.RWMutex
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

// Token returns the bearer token the client was constructed with
// (empty for anonymous clients). The returned value is the raw
// secret; do NOT log it, surface it in errors, or persist it. SDK
// callers that need to forward the token to other surfaces should
// copy it into a local variable scoped to the request.
//
// Safe for concurrent use; takes c.tokenMu read-lock.
func (c *Client) Token() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.token
}

// SetToken rotates the bearer token used for subsequent requests
// without reconstructing the Client. Useful for long-lived daemons
// that mint short-lived session tokens via the SDK (issue #560 /
// ADR-080 follow-up: faas.WithToken now wires through to this).
// An empty token suppresses the Authorization header on subsequent
// requests (useful for falling back to the anonymous device-code
// flow mid-session).
//
// Safe for concurrent use; takes c.tokenMu write-lock. In-flight
// requests built before SetToken continues to use the prior token;
// new requests see the new token.
func (c *Client) SetToken(token string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.token = token
}

// uploadHTTP returns the upload client or falls back to the default.
func (c *Client) uploadHTTP() *http.Client {
	if c.deployHTTP != nil {
		return c.deployHTTP
	}
	return c.http
}

// addAuthHeader sets Authorization: Bearer <token> on req when the
// Client holds a non-empty token. Reads c.tokenMu so it sees the
// latest value set by SetToken (issue #560 / ADR-080 follow-up:
// rotation without Client reconstruction).
func (c *Client) addAuthHeader(req *http.Request) {
	c.tokenMu.RLock()
	token := c.token
	c.tokenMu.RUnlock()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
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
	c.addAuthHeader(req)
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
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if !success(resp) {
		var p Problem
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			return &APIError{Problem: p}
		}
		return fmt.Errorf("API error: %s", resp.Status)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && out != nil && len(data) > 0 {
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
	if idempotencyKey == "" {
		idempotencyKey = newUUIDv4()
	}
	req, err := http.NewRequestWithContext(ctx, "DELETE", c.baseURL+"/v1/account", nil)
	if err != nil {
		return AccountDeletionResponse{}, err
	}
	c.addAuthHeader(req)
	req.Header.Set("Idempotency-Key", idempotencyKey)
	var out AccountDeletionResponse
	return out, c.doReq(c.http, req, &out)
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

// GetDeploymentScan returns the per-deploy grype CVE scan
// payload for one deployment (issue #464 / ADR-055). The
// handler returns a 404 in three cases — the deployment
// row doesn't exist, the deployment belongs to a different
// account (IDOR-safe), or no scan has run yet — and the
// SDK surfaces all three via the same ErrorCode wrapping
// `errors.Is(err, api.ErrNotFound)` callers already branch
// on. The Status field is the closed enum
// (complete|failed|skipped); see pkg/api.ScanResult for the
// full wire shape.
func (c *Client) GetDeploymentScan(ctx context.Context, id string) (ScanResult, error) {
	var out ScanResult
	return out, c.do(ctx, "GET", "/v1/deployments/"+id+"/scan", nil, &out)
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
	c.addAuthHeader(req)
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
// with CodeAdmissionRefused (HTTP 402).
func (c *Client) RaiseOverageCap(ctx context.Context, overageCapCents *int64) (AccountResponse, error) {
	body := map[string]any{"overage_cap_cents": overageCapCents}
	var out AccountResponse
	return out, c.do(ctx, "POST", "/v1/account/overage-cap", body, &out)
}

// GetEgressAllowlistExtra returns the per-account additive budget
// on top of the plan's apps.egress_allowlist cap (issue #679 /
// PR-B / ADR-082). The response carries the live value plus the
// plan cap and the global ceiling so the CLI can render the
// "Override: N / Plan cap: 16 / Max extra: 1024" trio without
// a second round-trip.
//
// Admin scope + MFA are required (the client passes the same
// auth as for RaiseOverageCap and ChangePlan).
func (c *Client) GetEgressAllowlistExtra(ctx context.Context) (AccountEgressAllowlistExtraResponse, error) {
	var out AccountEgressAllowlistExtraResponse
	return out, c.do(ctx, "GET", "/v1/account/egress_allowlist_extra", nil, &out)
}

// SetEgressAllowlistExtra sets the per-account additive budget
// (issue #679 / PR-B / ADR-082). Pass 0 to clear the override (the
// plan cap is authoritative again). Negative values or values
// above the global ceiling are rejected with
// CodeAccountEgressAllowlistExtraOutOfRange (HTTP 400).
//
// Admin scope + MFA are required.
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
// `<Method><PathSegments>`). The CLI's cmdTrafficSet routes through
// this entry point.
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

// RestartApp queues a fresh snapshot restart and returns its wake correlation
// id. The API performs the park and replacement wake asynchronously.
func (c *Client) RestartApp(ctx context.Context, slug string) (AppRestartResponse, error) {
	var out AppRestartResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/restart", nil, &out)
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

// DomainDoctor (ADR-120) returns the 5-check doctor report for a
// domain — see Client.DomainDoctor in pkg/api/client.go for the
// full docstring. Backed by GET /v1/domains/{domain}/doctor.
func (c *Client) DomainDoctor(ctx context.Context, domain string) (DomainDoctorReport, error) {
	var out DomainDoctorReport
	return out, c.do(ctx, "GET", "/v1/domains/"+domain+"/doctor", nil, &out)
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

// --- Jobs (issue #1184 Workstream A) ----------------------------------------
// Methods mirror the /v1/jobs surface added in M11.4. Mirrors
// the canonical client (pkg/api/client.go). Routes are keyed on
// the customer's slug (`name`) for create/list/update/delete;
// runs + tasks use the opaque run id (uuid).

// ListJobs returns the account-scoped list of jobs.
func (c *Client) ListJobs(ctx context.Context) (ListJobsResponse, error) {
	var out ListJobsResponse
	return out, c.do(ctx, "GET", "/v1/jobs", nil, &out)
}

// CreateJob creates a new job under the calling account.
func (c *Client) CreateJob(ctx context.Context, req CreateJobRequest) (JobResponse, error) {
	var out JobResponse
	return out, c.do(ctx, "POST", "/v1/jobs", req, &out)
}

// GetJob returns one job by name.
func (c *Client) GetJob(ctx context.Context, name string) (JobResponse, error) {
	var out JobResponse
	return out, c.do(ctx, "GET", "/v1/jobs/"+name, nil, &out)
}

// UpdateJob patches a job's image_ref / command / env_overrides /
// ram_mb / task_timeout_sec / max_parallelism / retry_max / status.
func (c *Client) UpdateJob(ctx context.Context, name string, req UpdateJobRequest) (JobResponse, error) {
	var out JobResponse
	return out, c.do(ctx, "PATCH", "/v1/jobs/"+name, req, &out)
}

// DeleteJob soft-deletes a job. Returns 409 CodeJobHasLiveInstances
// when live instances exist.
func (c *Client) DeleteJob(ctx context.Context, name string) (JobDeletedResponse, error) {
	var out JobDeletedResponse
	return out, c.do(ctx, "DELETE", "/v1/jobs/"+name, nil, &out)
}

// CreateJobRun fan-outs N tasks for the given job.
func (c *Client) CreateJobRun(ctx context.Context, name string, req CreateJobRunRequest) (JobRunResponse, error) {
	var out JobRunResponse
	return out, c.do(ctx, "POST", "/v1/jobs/"+name+"/runs", req, &out)
}

// ListJobRuns returns a page of the job's run history.
func (c *Client) ListJobRuns(ctx context.Context, name string) (ListJobRunsResponse, error) {
	var out ListJobRunsResponse
	return out, c.do(ctx, "GET", "/v1/jobs/"+name+"/runs", nil, &out)
}

// GetJobRun returns one run by id (uuid).
func (c *Client) GetJobRun(ctx context.Context, name, runID string) (JobRunResponse, error) {
	var out JobRunResponse
	return out, c.do(ctx, "GET", "/v1/jobs/"+name+"/runs/"+runID, nil, &out)
}

// CancelJobRun cancels a run.
func (c *Client) CancelJobRun(ctx context.Context, name, runID string) (JobRunCancelledResponse, error) {
	var out JobRunCancelledResponse
	return out, c.do(ctx, "POST", "/v1/jobs/"+name+"/runs/"+runID+"/cancel", nil, &out)
}

// ListJobRunTasks returns a page of the run's task rows.
func (c *Client) ListJobRunTasks(ctx context.Context, name, runID string) (ListJobTasksResponse, error) {
	var out ListJobTasksResponse
	return out, c.do(ctx, "GET", "/v1/jobs/"+name+"/runs/"+runID+"/tasks", nil, &out)
}

// GetJobTaskLogs tails the task's stdout/stderr via vmmd's tail endpoint.
func (c *Client) GetJobTaskLogs(ctx context.Context, name, runID string, taskIndex int) (JobTaskLogResponse, error) {
	var out JobTaskLogResponse
	return out, c.do(ctx, "GET", "/v1/jobs/"+name+"/runs/"+runID+"/tasks/"+strconv.Itoa(taskIndex)+"/logs", nil, &out)
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
// the stateless-advisory audit emit. appID filters the overscan
// window to events whose data.app_id matches (the dashboard's per-app
// drill-down).
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

// PostAuthSignup is the JSON-only POST /v1/auth/signup. Returns
// the ProgrammaticAuthResponse payload (mirrors PasswordSignup with
// the api_key surfaced). The Gregale CLI uses this on the
// `gregale signup` interactive path so the bearer-key token lands
// in ~/.config/faas/auth.json without a dashboard round-trip.
//
// Method name follows deriveMethodName (cmd/sdk-coverage/main.go) —
// POST + auth/signup → PostAuthSignup.
//
// The plaintext is returned ONCE; callers must persist it in the
// same call frame.
func (c *Client) PostAuthSignup(ctx context.Context, email, password string) (ProgrammaticAuthResponse, error) {
	var out ProgrammaticAuthResponse
	return out, c.do(ctx, "POST", "/v1/auth/signup",
		PasswordSignupRequest{Email: email, Password: password}, &out)
}

// PostAuthLogin is the JSON-only POST /v1/auth/login. Same response
// shape as PostAuthSignup so the CLI reuses a single unmarshaler.
// Anti-enumeration posture mirrors /login: Argon2id pad on the
// no-row branch, identical 401 on wrong-password vs unbound.
//
// Method name follows deriveMethodName — POST + auth/login →
// PostAuthLogin.
func (c *Client) PostAuthLogin(ctx context.Context, email, password string) (ProgrammaticAuthResponse, error) {
	var out ProgrammaticAuthResponse
	return out, c.do(ctx, "POST", "/v1/auth/login",
		PasswordLoginRequest{Email: email, Password: password}, &out)
}

// PostAuthSignupMagicLink is the JSON-only POST
// /v1/auth/signup/magic-link. The server always returns 200 with
// the same body regardless of whether the email is bound, unbound,
// malformed, or missing. The signup link is mailed via the
// platform's mailer when the address is recognised (or could be
// registered).
//
// Method name follows deriveMethodName — POST +
// auth/signup/magic-link → PostAuthSignupMagic-link (the dash
// survives because deriveMethodName only title-cases the first byte
// of each segment).
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
	return NewProblem(http.StatusForbidden, CodeUnsupportedByCLI,
		"endpoint requires the dashboard session cookie",
		"use SetPasswordWithSession with the faas_sid and faas_csrf cookies").WithDocs(
		docsBase + "/cli/cookie-only-routes")
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
// (same wire shape as pre-PR-B callers — pre-PR-B code paths are
// preserved by the scope="" branch). Pass a scope name to read
// or write a specific row; the SDK appends ?scope=<name> to the
// path. SetSecretsWithScope / UnsetSecretWithScope /
// RotateSecretWithScope are the canonical typed surfaces; the
// pre-PR-B SetSecret/UnsetSecret/RotateSecret stay as scope=""
// wrappers for backward-compat.
//
// Once SDK regen (C6) lands and the openapi.yaml gains the scope
// query parameter, the generator will emit a single typed method
// per verb (SetSecret(ctx, slug, key, value, scope) — *string)
// and these hand-rolled siblings will be removed. Until then
// the gregale CLI uses these directly to avoid an extra
// openapi.yaml + regen round-trip mid-PR.
func (c *Client) ListSecrets(ctx context.Context, slug string) (AppSecretListResponse, error) {
	return c.ListSecretsWithScope(ctx, slug, "")
}

// ListSecretsWithScope is the scope-aware sibling of ListSecrets.
// scope="" reads from the default scope (flat `secrets` array);
// scope="__all__" returns the nested `secrets_by_scope` map
// (ADR-092, mirror of ADR-090 D3's env_by_scope).
func (c *Client) ListSecretsWithScope(ctx context.Context, slug, scope string) (AppSecretListResponse, error) {
	var out AppSecretListResponse
	path := c.scopeQuery("/v1/apps/"+slug+"/secrets", scope)
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) SetSecret(ctx context.Context, slug, key, value string) error {
	return c.SetSecretWithScope(ctx, slug, key, value, "")
}

// SetSecretWithScope is the scope-aware sibling of SetSecret. The
// reserved sentinel "__all__" is rejected by the server with
// 400 env_scope_reserved; the SDK doesn't pre-validate so the
// error envelope reaches the caller verbatim.
func (c *Client) SetSecretWithScope(ctx context.Context, slug, key, value, scope string) error {
	path := c.scopeQuery("/v1/apps/"+slug+"/secrets/"+key, scope)
	return c.do(ctx, "PUT", path, PutAppSecretRequest{Value: value}, nil)
}

// SetAppEvictionPriority (issue #475) PATCHes the per-app eviction
// tier on the PATCH /v1/apps/{slug} endpoint. The priority argument
// is the closed enum 'best_effort' or 'reserved' (mirrors
// api.EvictionPriority in pkg/api/dto.go). The plan gate (Free +
// reserved) and the per-account cap (Plan.ReservedConcurrencyPerAccount)
// are enforced server-side; this helper is a thin one-liner so
// customer code never builds the UpdateAppRequest struct directly
// for the eviction-tier field. The response body is the updated
// AppResponse (matches SetSecret's no-return convention — the caller
// can GET the app if they need the post-PATCH projection).
func (c *Client) SetAppEvictionPriority(ctx context.Context, slug, priority string) error {
	return c.do(ctx, "PATCH", "/v1/apps/"+slug,
		UpdateAppRequest{EvictionPriority: &priority}, nil)
}
func (c *Client) UnsetSecret(ctx context.Context, slug, key string) error {
	return c.UnsetSecretWithScope(ctx, slug, key, "")
}

// UnsetSecretWithScope is the scope-aware sibling of UnsetSecret.
// Same reserved-sentinel posture as SetSecretWithScope.
func (c *Client) UnsetSecretWithScope(ctx context.Context, slug, key, scope string) error {
	path := c.scopeQuery("/v1/apps/"+slug+"/secrets/"+key, scope)
	return c.do(ctx, "DELETE", path, nil, nil)
}

// RotateSecret is the pre-PR-B wrapper that calls RotateSecret
// at the default scope. Mirrors SetSecret / UnsetSecret's pre-PR-B
// surface so callers that haven't migrated to scope still link.
func (c *Client) RotateSecret(ctx context.Context, slug, key, value string) (RotateAppSecretResponse, error) {
	return c.RotateSecretWithScope(ctx, slug, key, value, "")
}

// RotateSecretWithScope is the scope-aware sibling of RotateSecret.
// Same reserved-sentinel posture as SetSecretWithScope.
func (c *Client) RotateSecretWithScope(ctx context.Context, slug, key, value, scope string) (RotateAppSecretResponse, error) {
	var out RotateAppSecretResponse
	path := c.scopeQuery("/v1/apps/"+slug+"/secrets/"+key+"/rotate", scope)
	return out, c.do(ctx, "POST", path, RotateAppSecretRequest{Value: value}, &out)
}

// scopeQuery appends "?scope=<name>" to path when scope is
// non-empty. Empty scope returns the path unchanged (pre-PR-B
// callers see no behaviour change). Lives as a method on Client
// rather than a package-level helper so the SDK's other
// scope-aware methods can call it without an extra import.
// url.Values.Encode handles percent-encoding of edge cases
// (e.g. a future scope name with a reserved char — not currently
// possible given the api.EnvScopePattern regex, but defensive).
func (c *Client) scopeQuery(path, scope string) string {
	if scope == "" {
		return path
	}
	v := url.Values{}
	v.Set("scope", scope)
	return path + "?" + v.Encode()
}

// Private-registry Basic Auth (issue #461 / ADR-062). Password is
// sealed at rest server-side; the SDK only carries plaintext in the
// PUT body and never sees the ciphertext. Hosts MUST be supplied with
// an explicit "https://" prefix; apid rejects schemeless / http://
// inputs with 400 invalid_registry_host.
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

// Usage.
//
// GetUsage returns per-app usage rows for the given month — the wire
// shape is an ARRAY of UsageResponse objects, not a single struct.
// Mirrors the canonical Go SDK in pkg/api/client.go. See memory:
// getusage-wire-shape-mismatch for the history of the array contract.
func (c *Client) GetUsage(ctx context.Context, month string) ([]UsageResponse, error) {
	var out []UsageResponse
	return out, c.do(ctx, "GET", "/v1/usage?month="+month, nil, &out)
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

// Org surface (issue #190 / IAM-6 / ADR-061, PR 5). The 11 methods
// below mirror the spec routes documented under api/openapi.yaml
// paths /v1/orgs*, /v1/invitations/{token}. Each maps 1:1 to a
// spec route so the sdk-coverage gate (cmd/sdk-coverage) doesn't
// false-positive on drift. Bearer-auth only; account-scoped routes
// (`ListOrgs`, `CreateOrg`) skip the X-Active-Org hint, path-scoped
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
// to Acme Inc. as developer" before the invitee accepts. The accept
// flow lands in PR 8.
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
// The outbound webhook surface mirrors the apid routes
// under /v1/apps/{slug}/webhooks[/...]. Eight endpoints: list,
// create, get, update, delete, rotate-secret, list-deliveries,
// retry-delivery. The wire shape (Create/Update AppWebhookRequest,
// AppWebhookResponse, AppWebhookDeliveryResponse) is sourced from
// pkg/api/webhooks.go and is also embedded as DTOs via the sdk-gen
// aggregator; this file is hand-curated for the Go SDK. See
// memory `sdk-go-errors-hand-curated-subset` for the related
// ErrXxx mirror pattern.

// ListAppWebhooks returns the per-app webhook subscriptions.
func (c *Client) ListAppWebhooks(ctx context.Context, slug string) ([]AppWebhookResponse, error) {
	var out []AppWebhookResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/webhooks", nil, &out)
}

// CreateAppWebhook subscribes a target URL to events on the app.
// The plaintext WebhookSecret is sent over the wire and is NEVER
// logged client-side; the response carries only the masked
// constant `***` for WebhookSecretSealedMasked.
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
// secret. The plaintext is server-side and never crosses the wire;
// the response carries only the masked constant and the rotated_at
// timestamp. Subsequent reads of the row return the masked constant.
func (c *Client) RotateAppWebhookSecret(ctx context.Context, slug, id string) (RotateAppWebhookSecretResponse, error) {
	var out RotateAppWebhookSecretResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/webhooks/"+id+"/rotate-secret", nil, &out)
}

// ListAppWebhookDeliveries paginates the per-subscription delivery
// ledger. `status` is one of pending|in_flight|succeeded|failed|dead
// or empty (all statuses). pageSize caps the response; pageToken is
// the opaque cursor returned by the previous call.
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

// --- Triggers (issue #757 / ADR-100) ----------------------------------------
// Unified event-source-mapping primitive. The trigger API matches
// `api/openapi.yaml` operations 1:1; method names follow the convention
// pinned by cmd/sdk-coverage/main.go::methodRouteMap (the spec-coverage
// gate keeps the verb ↔ route mapping in lock-step).

// GetTriggers lists every trigger owned by the calling account,
// optionally filtered by app_id and/or kind. Newest-first by
// created_at; the typical account has well under 200.
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
// auto-minted for the POST (TestDo_MutatingCallsCarryIdempotencyKey),
// so replays with identical bodies do not double-create.
func (c *Client) PostTriggers(ctx context.Context, req CreateTriggerRequest) (Trigger, error) {
	var out Trigger
	return out, c.do(ctx, "POST", "/v1/triggers", req, &out)
}

// GetTriggersId returns one trigger by id.
func (c *Client) GetTriggersId(ctx context.Context, id string) (Trigger, error) {
	var out Trigger
	return out, c.do(ctx, "GET", "/v1/triggers/"+id, nil, &out)
}

// PatchTriggersId is a partial PATCH; nil fields are left unchanged.
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

// GetTriggersIdRecords returns records for one trigger, newest-first.
// state filter is optional — passing "" omits the query param.
func (c *Client) GetTriggersIdRecords(ctx context.Context, id, state string) (ListTriggerRecordsResponse, error) {
	path := "/v1/triggers/" + id + "/records"
	if state != "" {
		path += "?state=" + state
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

// GetTriggersIdDlq returns rows from trigger_dead_letter for the
// given trigger, newest-first.
func (c *Client) GetTriggersIdDlq(ctx context.Context, id, reason string) (ListTriggerDeadLetterResponse, error) {
	path := "/v1/triggers/" + id + "/dlq"
	if reason != "" {
		path += "?reason=" + reason
	}
	var out ListTriggerDeadLetterResponse
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetTriggersIdMetrics returns the per-state count roll-up. Not a
// Prometheus surface; /v1/metrics is the metrics surface.
func (c *Client) GetTriggersIdMetrics(ctx context.Context, id string) (TriggerMetricsResponse, error) {
	var out TriggerMetricsResponse
	return out, c.do(ctx, "GET", "/v1/triggers/"+id+"/metrics", nil, &out)
}

// PostInvocationsDispatchBatch posts a closed batch envelope to the
// gateway synth plane (issue #757). The function under the trigger
// responds with `{"batchItemFailures":[{"itemIdentifier":"..."}]}`;
// empty / missing ⇒ full success. Internal-only route — schedd
// uses this on dispatch tick closure.
func (c *Client) PostInvocationsDispatchBatch(ctx context.Context, triggerID, appID string, kind TriggerKind, records []map[string]any) error {
	body := map[string]any{
		"trigger_id": triggerID,
		"app_id":     appID,
		"kind":       string(kind),
		"records":    records,
	}
	return c.do(ctx, "POST", "/v1/invocations:dispatch_batch", body, nil)
}

// PostTriggersBatchCreate applies a gregale.yaml triggers fragment in
// one transaction (dashboard-only shortcut).
func (c *Client) PostTriggersBatchCreate(ctx context.Context, req CreateTriggerBatchRequest) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, "POST", "/v1/triggers:batch_create", req, &out)
}

// CancelDeployment flips a deployment in {pending, building, imaging,
// snapshotting} to "cancelled" and cascades the cancel to its
// in-flight builds. Live deployments return 409 with code
// "deployment_cancel_live_forbidden" and the canonical hint pointing
// at `gregale deploys rollback` — the per-deployment contract from
// ADR-118 / ADR-124. Optional reason values are the closed set
// "user" | "auto_quota" | "auto_health" | "system"; an empty string
// defaults to "user" server-side.
//
// Returns the typed CancelDeploymentResponse so callers can read
// CancelReason (the reason the server recorded on the row) and
// CancelledBuilds (the cascade-cancelled build IDs).
func (c *Client) CancelDeployment(ctx context.Context, appSlug, id string, reason string) (CancelDeploymentResponse, error) {
	var out CancelDeploymentResponse
	body := CancelDeploymentRequest{Reason: reason}
	return out, c.do(ctx, "POST", "/v1/apps/"+appSlug+"/deployments/"+id+"/cancel", body, &out)
}

// ReorderDeployment updates the priority of a still-pending deployment.
// 0 = "deploy immediately" (top of queue), 100 = FIFO default, 1000 =
// background rebuild. Plan-gated (Hobby/Pro/Scale only); Free returns
// 402 "plan_reorder_disabled". Returns ErrReorderNotPending (409) when
// the deployment has already moved off the pending queue.
//
// Returns the typed ReorderDeploymentResponse so callers can confirm
// the server-applied priority (the response echoes the value).
func (c *Client) ReorderDeployment(ctx context.Context, id string, newPriority int) (ReorderDeploymentResponse, error) {
	var out ReorderDeploymentResponse
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
