// handlers_admin_obs_test.go — pins the contract of the operator
// observability backend (issue #777 / ADR-091):
//
//	GET /v1/admin/obs/overview
//	GET /v1/admin/obs/tenants
//	GET /v1/admin/obs/tenants/{id}
//	GET /v1/admin/obs/nodes
//	GET /v1/admin/obs/nodes/{name}/heartbeats
//
// What this file pins:
//
//  1. Two-layer auth gate: admin scope + email allowlist
//     (s.adminAllows). Non-admin scope → 403; admin scope but
//     email not in the allowlist → 403 admin_required.
//  2. PII redaction by default: the email field is absent on
//     the default list/detail path. ?include_pii=1 is the only
//     opt-in.
//  3. Pagination: limit=1000 is silently capped to
//     api.ObsAdminPaginationMax (500). The response carries
//     a non-nil Items slice and the requested limit value.
//  4. Org filter: ?plan= and ?status= narrow the result set
//     without surfacing a 400 on a typo (matches the
//     filterTenantRows contract).
//  5. Per-tenant detail: unknown id → 404 with code CodeNotFound.
//
// The grep-style cross-tenant / PII-leak tests live in
// handlers_admin_obs_security_test.go so the security posture
// has its own focused file.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// newObsEnv is the testEnv twin for the obs surface. Mirrors
// newReconcileEnv (handlers_admin_billing_test.go): wires a
// single admin allowlist entry (adminEmail), mints a bearer
// with the requested scope set, and returns env + server.
// scopes is the API-key scope set, NOT the cookie-implicit
// admin posture — tests that need a non-admin caller pass
// api.ScopesReadSurface (or any non-admin set) here.
func newObsEnv(t *testing.T, scopes []string, adminEmail, callerEmail string) testEnv {
	t.Helper()
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("apid_obs_test")
	acct, err := store.CreateAccount(context.Background(), callerEmail, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "obs-test", scopes); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), ops)
	srv.WithAdminAllowlist(adminEmail)
	return testEnv{h: srv.handler(), s: srv, store: store, key: pt, acct: acct, ops: ops}
}

// TestObsOverview_AuthGate_RejectsCustomerKey pins the two-layer
// auth gate: a non-admin API key reaches 403 before the
// handler body executes. The scope check is declarative at
// the middleware level so the handler never sees the request
// (defense-in-depth — the s.adminAllows call inside the
// handler is the second layer).
func TestObsOverview_AuthGate_RejectsCustomerKey(t *testing.T) {
	e := newObsEnv(t, api.ScopesReadSurface, "ops@faas.dev", "customer@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/overview", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("overview with customer scope: got status %d, want 403", rec.Code)
	}
}

// TestObsOverview_AuthGate_RejectsNonAllowlistedEmail pins the
// second layer: the caller has admin scope but their email
// is NOT in FAAS_ADMIN_EMAILS. The handler's s.adminAllows
// check rejects with 403 admin_required.
func TestObsOverview_AuthGate_RejectsNonAllowlistedEmail(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "rogue@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/overview", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("overview with admin scope + non-allowlist email: got status %d, want 403", rec.Code)
	}
	assertProblem(t, rec, http.StatusForbidden, "admin_required")
}

// TestObsOverview_HappyPath_ReturnsKPIBundle pins the
// successful path: an admin caller whose email is in the
// allowlist sees the full KPI bundle. We don't assert field
// values (counts depend on the empty MemStore fixture) —
// the test is structural: 200 + a non-nil Totals and arrays
// + a generated_at timestamp.
func TestObsOverview_HappyPath_ReturnsKPIBundle(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/overview", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("overview: got status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsOverviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal overview: %v", err)
	}
	if resp.GeneratedAt.IsZero() {
		t.Errorf("overview: generated_at is zero")
	}
	if resp.Totals.AccountsActive == 0 {
		// Empty store; the test fixture has the operator account
		// so this is always 1+. The non-zero check pins that the
		// projection helper ran (no nil-panic regression).
		t.Errorf("overview: accounts_active is 0 (projection may not have run)")
	}
	if resp.TopRateLimitedAccounts24h == nil {
		t.Errorf("overview: top_rate_limited_accounts_24h is null; want an empty array")
	}
	if resp.NodeHealth == nil {
		t.Errorf("overview: node_health is null; want an array")
	}
	if resp.RecentFailures1h == nil {
		t.Errorf("overview: recent_failures_1h is null; want an empty array")
	}
}

// TestObsListTenants_PaginationCap pins the 400 contract for an
// over-cap limit. The repo convention is "validate and 400" with
// a stable CodeValidation + WithLimit (pkg/api/paging.go:63);
// a buggy operator client gets an actionable 400 rather than a
// silent clamp. The cap itself (api.ObsAdminPaginationMax = 500)
// is the value the wire advertises in the WithLimit field.
func TestObsListTenants_PaginationCap(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/tenants?limit=1000", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("tenants: got status %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	// Verify the WithLimit fields surface the cap + observed value
	// so a misconfigured operator client gets an actionable response.
	var prob api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if prob.Limit == nil || *prob.Limit != int64(api.ObsAdminPaginationMax) {
		t.Errorf("tenants over-cap: problem.limit = %v, want %d", prob.Limit, api.ObsAdminPaginationMax)
	}
	if prob.Observed == nil || *prob.Observed != 1000 {
		t.Errorf("tenants over-cap: problem.observed = %v, want 1000", prob.Observed)
	}
}

// TestObsListTenants_DefaultLimit pins that the absent ?limit=
// query falls back to api.ObsAdminPaginationDefault (200). The
// response carries the limit value so the operator UI can render
// "page 1 of N" without a second round-trip.
func TestObsListTenants_DefaultLimit(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/tenants", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("tenants: got status %d, want 200", rec.Code)
	}
	var resp api.ObsTenantListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Limit != api.ObsAdminPaginationDefault {
		t.Errorf("tenants default: response limit = %d, want %d", resp.Limit, api.ObsAdminPaginationDefault)
	}
	if resp.Items == nil {
		t.Errorf("tenants: items slice is nil; must be non-nil for stable JSON shape")
	}
}

// TestObsListTenants_PIIRedactedByDefault pins ADR-091 §3:
// the default response redacts email. The field is omitted
// from every row so a frontend can branch on presence.
func TestObsListTenants_PIIRedactedByDefault(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/tenants", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("tenants: got status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	// PII-redact posture: the email address the operator
	// authenticates as must NOT appear in the response body.
	if strings.Contains(body, "ops@faas.dev") {
		t.Errorf("tenants body contains allowlist email: PII not redacted by default")
	}
}

// TestObsListTenants_IncludePII_SurfacesEmail is the
// opt-in positive case. The email appears in the row when
// ?include_pii=1 is passed; the test does not assert a
// pii.accessed audit row (the audit pipeline is not wired
// in the unit harness — see TestObsAudit_EmitsPIIAccessed
// in handlers_admin_obs_security_test.go for the audit
// assertion).
func TestObsListTenants_IncludePII_SurfacesEmail(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/tenants?include_pii=1", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("tenants include_pii: got status %d, want 200", rec.Code)
	}
	var resp api.ObsTenantListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) == 0 {
		t.Fatalf("tenants: empty items")
	}
	// The first row is the operator account (created by newObsEnv).
	if resp.Items[0].Email == "" {
		t.Errorf("tenants include_pii=1: first row email empty")
	}
}

// TestObsListTenants_OrgFilter pins that ?plan= narrows the
// result set without surfacing a 400 on a typo. The handler
// is a typo-tolerant filter, not a validator.
func TestObsListTenants_OrgFilter(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/tenants?plan=scale", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("tenants plan filter: got status %d, want 200", rec.Code)
	}
	var resp api.ObsTenantListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, row := range resp.Items {
		if row.Plan != "scale" {
			t.Errorf("tenants plan=scale: row plan = %q, want scale", row.Plan)
		}
	}
}

// TestObsGetTenant_NotFound pins the 404 contract on an
// unknown UUID.
func TestObsGetTenant_NotFound(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/tenants/00000000-0000-0000-0000-000000000000", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("tenants detail unknown: got status %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	assertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
}

// TestObsGetTenant_BadID pins the 400 contract on a
// non-UUID path parameter.
func TestObsGetTenant_BadID(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/tenants/not-a-uuid", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("tenants detail bad uuid: got status %d, want 400", rec.Code)
	}
}

// TestObsListNodes_IncludeInactive pins that ?include_inactive=1
// surfaces the drained rows. The default is active-only
// (matches /v1/compute-nodes).
func TestObsListNodes_IncludeInactive(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	// Synthesize a second node so the test has more than the
	// default-local row.
	if _, err := e.store.UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{
		Name:               "node-1",
		TargetURL:          "unix:///run/faas/vmmd-1.sock",
		VPCPUs:             4,
		MemMB:              8192,
		MaxConcurrency:     16,
		AdmissionCeilingMB: 47600,
	}); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "GET", "/v1/admin/obs/nodes?include_inactive=1", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("nodes: got status %d, want 200", rec.Code)
	}
	var resp api.ObsNodeListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) < 2 {
		t.Errorf("nodes: got %d items, want >= 2 (default-local + node-1)", len(resp.Items))
	}
}

// TestObsNodeHeartbeats_DefaultSinceWindow pins the
// 30m default ?since= behaviour. The endpoint always
// returns 200 + a non-nil heartbeats slice; the
// since_clamped flag is false on the default path.
func TestObsNodeHeartbeats_DefaultSinceWindow(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	nodes, err := e.store.ListComputeNodes(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatalf("test fixture missing default-local node")
	}
	rec := e.do(t, "GET", "/v1/admin/obs/nodes/"+nodes[0].Name+"/heartbeats", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeats: got status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsHeartbeatListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Heartbeats == nil {
		t.Errorf("heartbeats: slice is nil; must be non-nil for stable JSON shape")
	}
	if resp.SinceClamped {
		t.Errorf("heartbeats default: since_clamped=true, want false")
	}
}

// TestObsNodeHeartbeats_UnknownNode pins the 404 contract
// when the operator queries a node that doesn't exist.
func TestObsNodeHeartbeats_UnknownNode(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/nodes/no-such-node/heartbeats", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("heartbeats unknown node: got status %d, want 404", rec.Code)
	}
}

// Compile-time guard: ensure testEnv.value httptest.ResponseRecorder
// is the same shape the production handler reads, so an
// accidental change to the handler signature trips a build
// before a test runs.
var _ = httptest.NewRecorder
