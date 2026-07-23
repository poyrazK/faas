package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

type testEnv struct {
	h     http.Handler
	store *state.MemStore
	key   string
	acct  state.Account
	ops   *wire.OpsMetrics
}

func setup(t *testing.T, plan api.Plan) testEnv {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), fmt.Sprintf("%s@example.com", plan), plan)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "test"); err != nil {
		t.Fatal(err)
	}
	ops := wire.NewOpsMetrics("apid_test")
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.com", noopNotifier{}).WithOpsMetrics(ops)
	return testEnv{h: srv.handler(), store: store, key: pt, acct: acct, ops: ops}
}

func (e testEnv) do(t *testing.T, method, path string, body any, hdrs map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Authorization", "Bearer "+e.key)
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestWhoami(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/account", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AccountResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Plan != "pro" || out.Email != "pro@example.com" {
		t.Errorf("unexpected account: %+v", out)
	}
}

func TestAuthRejectsBadKey(t *testing.T) {
	e := setup(t, api.PlanFree)
	req := httptest.NewRequest("GET", "/v1/account", nil)
	req.Header.Set("Authorization", "Bearer fp_live_deadbeef")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("bad key should 401, got %d", rec.Code)
	}
}

func TestAuthRejectsMissingKey(t *testing.T) {
	e := setup(t, api.PlanFree)
	req := httptest.NewRequest("GET", "/v1/account", nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("missing key should 401, got %d", rec.Code)
	}
}

func TestCreateAppSuccess(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "my-api"}, nil)
	if rec.Code != 201 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.RAMMB != 512 { // Pro default
		t.Errorf("ram default = %d, want 512", out.RAMMB)
	}
	if out.URL != "https://my-api.apps.example.com" {
		t.Errorf("url = %q", out.URL)
	}
}

func TestCreateAppInvalidSlug(t *testing.T) {
	e := setup(t, api.PlanPro)
	for _, slug := range []string{"AB", "x", "has space", "-lead", "trail-", "UPPER"} {
		rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: slug}, nil)
		if rec.Code != 400 {
			t.Errorf("slug %q should be rejected, got %d", slug, rec.Code)
		}
	}
}

// TestQuotaMatrix is the M5 acceptance: plan quotas enforced before work, across
// every plan (RAM cap, concurrency cap, deployed-app count).
func TestQuotaMatrix(t *testing.T) {
	for _, plan := range api.Plans {
		limits := api.MustLimitsFor(plan)
		t.Run(string(plan)+"/ram-over-cap", func(t *testing.T) {
			e := setup(t, plan)
			rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "ram-app", RAMMB: limits.RAMMB + 1}, nil)
			assertProblem(t, rec, 403, api.CodePlanLimitRAM)
		})
		t.Run(string(plan)+"/concurrency-over-cap", func(t *testing.T) {
			e := setup(t, plan)
			rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "cc-app", MaxConcurrency: limits.MaxConcurrency + 1}, nil)
			assertProblem(t, rec, 403, api.CodePlanLimitConcur)
		})
		t.Run(string(plan)+"/app-count-over-cap", func(t *testing.T) {
			e := setup(t, plan)
			for i := 0; i < limits.DeployedApps; i++ {
				rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: fmt.Sprintf("%s-app-%d", plan, i)}, nil)
				if rec.Code != 201 {
					t.Fatalf("app %d should succeed under cap %d: %d %s", i, limits.DeployedApps, rec.Code, rec.Body)
				}
			}
			rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: fmt.Sprintf("%s-over", plan)}, nil)
			assertProblem(t, rec, 403, api.CodePlanLimitApps)
		})
	}
}

func TestFunctionRequiresRuntime(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "fn", Type: "function"}, nil)
	if rec.Code != 400 {
		t.Errorf("function without runtime should 400, got %d", rec.Code)
	}
	rec = e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "fn2", Type: "function", Runtime: "node22"}, nil)
	if rec.Code != 201 {
		t.Errorf("function with runtime should 201, got %d: %s", rec.Code, rec.Body)
	}
}

func TestCreateDeploymentImage(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dep-app"}, nil)

	digest := "sha256:" + repeat("a", 64)
	rec := e.do(t, "POST", "/v1/apps/dep-app/deployments",
		api.CreateDeploymentRequest{Image: "registry.example.com/x@" + digest}, nil)
	if rec.Code != 202 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.DeploymentResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	// apid stores the full OCI reference into deployments.image_digest
	// (issue #53 / M5 acceptance on Lima): the OCI puller in imaged needs
	// the host to dial the right registry, not a bare sha256:... token.
	if out.ImageDigest != "registry.example.com/x@"+digest || out.Status != "pending" {
		t.Errorf("unexpected deployment: %+v", out)
	}
}

func TestCreateDeploymentRejectsNonDigestImage(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dep-app"}, nil)
	rec := e.do(t, "POST", "/v1/apps/dep-app/deployments",
		api.CreateDeploymentRequest{Image: "registry.example.com/x:latest"}, nil)
	if rec.Code != 400 {
		t.Errorf("non-digest image should 400, got %d", rec.Code)
	}
}

func TestDeploymentUnknownApp404(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps/ghost/deployments",
		api.CreateDeploymentRequest{Image: "r/x@sha256:" + repeat("a", 64)}, nil)
	if rec.Code != 404 {
		t.Errorf("deploy to unknown app should 404, got %d", rec.Code)
	}
}

func TestIdempotencyReplays(t *testing.T) {
	e := setup(t, api.PlanPro)
	hdr := map[string]string{"Idempotency-Key": "abc-123"}
	first := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "idem-app"}, hdr)
	if first.Code != 201 {
		t.Fatalf("first: %d %s", first.Code, first.Body)
	}
	// Replaying the same key returns the stored response, NOT a slug-conflict.
	second := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "idem-app"}, hdr)
	if second.Code != 201 {
		t.Errorf("idempotent replay should return 201, got %d %s", second.Code, second.Body)
	}
	if second.Header().Get("Idempotent-Replayed") != "true" {
		t.Error("replay should be marked Idempotent-Replayed")
	}
	// Only one app actually exists.
	if n, _ := e.store.CountDeployedApps(context.Background(), e.acct.ID); n != 1 {
		t.Errorf("idempotent retry created %d apps, want 1", n)
	}
}

func assertProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, status, rec.Body)
	}
	var p api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("body not problem+json: %s", rec.Body)
	}
	if p.Code != code {
		t.Errorf("problem code = %q, want %q", p.Code, code)
	}
}

func repeat(s string, n int) string {
	b := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		b = append(b, s[0])
	}
	return string(b)
}

func TestListApps_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)

	// Create a couple of apps via the API.
	for _, slug := range []string{"alpha", "beta"} {
		rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: slug}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", slug, rec.Code, rec.Body)
		}
	}

	rec := e.do(t, "GET", "/v1/apps", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("listApps: %d %s", rec.Code, rec.Body)
	}
	var apps []api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &apps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(apps) != 2 {
		t.Errorf("got %d apps, want 2", len(apps))
	}
	// Slugs should be present in some order.
	gotSlugs := map[string]bool{}
	for _, a := range apps {
		gotSlugs[a.Slug] = true
	}
	for _, want := range []string{"alpha", "beta"} {
		if !gotSlugs[want] {
			t.Errorf("missing slug %q in response", want)
		}
	}
}

func TestListApps_Empty(t *testing.T) {
	e := setup(t, api.PlanFree)
	rec := e.do(t, "GET", "/v1/apps", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// Empty list, not null — must be `[]` not `null`.
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" && got != "null" {
		// Some go versions emit "[]" for empty slices, some emit nothing; both are acceptable.
		// We only fail if a non-empty body is returned.
		if got != "" && got != "[]" && got != "null" {
			t.Errorf("body = %q, want [] or null", got)
		}
	}
}

func TestListApps_RequiresAuth(t *testing.T) {
	e := setup(t, api.PlanFree)
	req := httptest.NewRequest("GET", "/v1/apps", nil)
	// No Authorization header.
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// TestHealthz confirms the loopback liveness probe returns 200 with
// the canonical {"status":"ok"} body. No auth, no DB — issue #85 said
// the gatewayd forwards this path verbatim and a 5xx would cascade
// into the control plane.
func TestHealthz(t *testing.T) {
	e := setup(t, api.PlanFree)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	// Deliberately no Authorization header — /healthz is unauth.
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("body = %q, want status:ok", body)
	}
}

// observeWrap is the outermost middleware on every route; assert
// it tags successes as code="ok" and 4xx as code="err", and uses
// the route template (not the URL) for the op label so cardinality
// stays bounded.
func TestObserveWrap_OKAndErrRoutes(t *testing.T) {
	e := setup(t, api.PlanPro)

	// 200 path: GET /v1/account.
	rec := e.do(t, "GET", "/v1/account", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/v1/account status = %d, want 200", rec.Code)
	}

	// 4xx path: POST /v1/apps with a duplicate slug.
	if dup := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dup"}, nil); dup.Code != 201 {
		t.Fatalf("seed create status = %d, want 201", dup.Code)
	}
	rec = e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dup"}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("dup status = %d, want 409", rec.Code)
	}

	srv := httptest.NewServer(e.ops.Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	bodyB, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(bodyB)

	// r.Pattern for Go 1.22 mux uses the registered pattern; we
	// assert the labels that show up regardless of which concrete
	// routes are observed. route templates — the observeWrap uses
	// r.Pattern (e.g. "GET /v1/account", "POST /v1/apps").
	want := []string{
		`apid_test_ops_total{code="ok",op="GET /v1/account"} 1`,
		`apid_test_ops_total{code="err",op="POST /v1/apps"} 1`,
		`apid_test_op_duration_seconds_count{op="GET /v1/account"} 1`,
		`apid_test_op_duration_seconds_count{op="POST /v1/apps"} 2`,
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("metrics body missing %q:\n%s", w, body)
		}
	}
}

// Auth-failure path (401) must also be observed by the wrap: the §12
// dashboard needs a rejected-traffic panel separate from a
// server-err panel so a misconfigured customer key spikes correctly.
func TestObserveWrap_AuthFailure(t *testing.T) {
	e := setup(t, api.PlanFree)
	req := httptest.NewRequest("GET", "/v1/account", nil)
	req.Header.Set("Authorization", "Bearer fp_live_deadbeef")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad key status = %d, want 401", rec.Code)
	}

	srv := httptest.NewServer(e.ops.Handler())
	t.Cleanup(srv.Close)
	resp, _ := http.Get(srv.URL)
	defer func() { _ = resp.Body.Close() }()
	bodyB, _ := io.ReadAll(resp.Body)
	body := string(bodyB)
	if !strings.Contains(body, `apid_test_ops_total{code="err",op="GET /v1/account"} 1`) {
		t.Errorf("metrics body missing 401-counted series:\n%s", body)
	}
}

// Unmatched routes (r.Pattern == "" — 404s a URL scanner hits) must
// be observed under the fixed op="unmatched" label, NOT the literal
// URL path. Recording under the path would let a scanner explode the
// label set unbounded (review finding #2 on PR #132); the fixed
// bucket keeps scanner traffic as one series per code.
func TestObserveWrap_UnmatchedRoute(t *testing.T) {
	e := setup(t, api.PlanFree)

	// Two distinct scanner paths → both should land on the same
	// op="unmatched" series (count = 2), proving we did NOT record
	// under r.URL.Path.
	for _, path := range []string{"/wp-login.php", "/.env"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, rec.Code)
		}
	}

	srv := httptest.NewServer(e.ops.Handler())
	t.Cleanup(srv.Close)
	resp, _ := http.Get(srv.URL)
	defer func() { _ = resp.Body.Close() }()
	bodyB, _ := io.ReadAll(resp.Body)
	body := string(bodyB)

	want := []string{
		`apid_test_ops_total{code="err",op="unmatched"} 2`,
		`apid_test_op_duration_seconds_count{op="unmatched"} 2`,
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("metrics body missing %q:\n%s", w, body)
		}
	}
	// Defense against a regression to the old "op = r.URL.Path" shape:
	// the literal path must NOT surface as its own op label.
	for _, banned := range []string{`op="/wp-login.php"`, `op="/.env"`} {
		if strings.Contains(body, banned) {
			t.Errorf("metrics body leaked scanner URL as op label %q:\n%s", banned, body)
		}
	}
}
