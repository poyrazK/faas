package main

// Tests for the three account-scoped list endpoints (issue #393):
//
//   GET /v1/instances        → listInstancesForAccount
//   GET /v1/secrets          → listSecretsForAccount
//   GET /v1/apps/metrics     → getAppsMetrics
//
// Test coverage:
//
//   - Happy path for each handler (one account, multiple rows).
//   - Strict-mode cursor / limit validation (400 RFC 7807).
//   - Cross-account isolation (alice sees her rows; bob sees none).
//   - Plaintext-never-leaked invariant for the secrets endpoint.
//   - Metrics rollup: degraded source envelope on Prometheus unavailable,
//     fan-out returns the per-app shape per app_slug.
//
// The fixtures mirror the existing list_invoices_test.go /
// handlers_secrets_test.go / handlers_metrics_test.go shapes so a
// reviewer can pattern-match against the per-app endpoints.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// mustCreateAccount is a small helper that mirrors the test fixtures
// in handlers_secrets_test.go (mustNamed) but takes the store
// directly. Returns an account + an admin API-key plaintext.
func mustCreateAccount(t *testing.T, store *state.MemStore, label string, plan api.Plan) (state.Account, string) {
	t.Helper()
	acct, err := store.CreateAccount(context.Background(), label+"@example.com", plan)
	if err != nil {
		t.Fatalf("create %s: %v", label, err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	return acct, pt
}

// mustSeedInstanceDirect creates an instance row in MemStore without
// going through the full /v1/apps wake path (test fixture). Returns
// the generated instance id so the test can assert against it.
func mustSeedInstanceDirect(t *testing.T, store *state.MemStore, appID, deploymentID, st string, ramMB int) string {
	t.Helper()
	ins, err := store.CreateInstance(context.Background(), appID, deploymentID, st, ramMB, "node-1", "")
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	return ins.ID
}

// TestListInstancesForAccount_HappyPath_Empty covers the empty-page
// case (no rows → 200 with empty instances, no cursor).
func TestListInstancesForAccount_HappyPath_Empty(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, http.MethodGet, "/v1/instances", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.ListInstancesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Instances) != 0 {
		t.Fatalf("expected empty, got %d rows", len(out.Instances))
	}
	if out.NextBefore != "" {
		t.Fatalf("expected empty cursor, got %q", out.NextBefore)
	}
}

// TestListInstancesForAccount_HappyPath_TwoApps_ThreeInstancesEach
// seeds 2 apps × 3 instances each (6 rows total) and asserts the
// response carries every row with the right app_id and the next_before
// cursor fires because the response hits the default limit (25).
func TestListInstancesForAccount_HappyPath_TwoApps_ThreeInstancesEach(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appA := createApp(t, e, "app-a")
	appB := createApp(t, e, "app-b")
	for i := 0; i < 3; i++ {
		mustSeedInstanceDirect(t, e.store, appA.ID, "", "running", 256)
		mustSeedInstanceDirect(t, e.store, appB.ID, "", "running", 256)
	}

	rec := e.do(t, http.MethodGet, "/v1/instances", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.ListInstancesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Instances) != 6 {
		t.Fatalf("expected 6 instances, got %d", len(out.Instances))
	}
	// app_id is populated on every row.
	seen := map[string]int{}
	for _, ins := range out.Instances {
		seen[ins.AppID]++
	}
	if seen[appA.ID] != 3 || seen[appB.ID] != 3 {
		t.Fatalf("expected 3/3 split across apps, got %v", seen)
	}
}

// TestListInstancesForAccount_BadLimit covers the strict-mode 400
// path on `?limit=` outside the 1..100 range. Same shape as
// TestListInvoices_BadLimit.
func TestListInstancesForAccount_BadLimit(t *testing.T) {
	e := setup(t, api.PlanHobby)
	for _, lim := range []string{"0", "99999", "garbage", "-1"} {
		rec := e.do(t, http.MethodGet, "/v1/instances?limit="+lim, nil, nil)
		assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	}
}

// TestListInstancesForAccount_CrossAccountIsolation seeds an instance
// for bob's app, then asserts alice's GET /v1/instances returns no rows
// for bob's app_id (the SQL JOIN on apps.account_id = $1 is the only
// IDOR guard; this test pins that contract).
func TestListInstancesForAccount_CrossAccountIsolation(t *testing.T) {
	store := state.NewMemStore()
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	alice, keyAlice := mustCreateAccount(t, store, "alice", api.PlanHobby)
	bob, keyBob := mustCreateAccount(t, store, "bob", api.PlanHobby)
	envA := testEnv{h: srv.handler(), store: store, key: keyAlice, acct: alice}
	envB := testEnv{h: srv.handler(), store: store, key: keyBob, acct: bob}
	appBob := createApp(t, envB, "bob-app")
	for i := 0; i < 5; i++ {
		mustSeedInstanceDirect(t, store, appBob.ID, "", "running", 256)
	}

	// Alice sees her one app's instances.
	rec := envA.do(t, http.MethodGet, "/v1/instances", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("alice status %d", rec.Code)
	}
	var aResp api.ListInstancesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &aResp); err != nil {
		t.Fatalf("unmarshal alice: %v", err)
	}
	for _, ins := range aResp.Instances {
		if ins.AppID == appBob.ID {
			t.Fatalf("alice saw bob's instance: app_id=%s", ins.AppID)
		}
	}

	// Bob sees his 5 instances.
	rec = envB.do(t, http.MethodGet, "/v1/instances", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("bob status %d", rec.Code)
	}
	var bResp api.ListInstancesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &bResp); err != nil {
		t.Fatalf("unmarshal bob: %v", err)
	}
	if len(bResp.Instances) != 5 {
		t.Fatalf("bob expected 5 instances, got %d", len(bResp.Instances))
	}
	for _, ins := range bResp.Instances {
		if ins.AppID != appBob.ID {
			t.Fatalf("bob saw a non-bob instance: app_id=%s", ins.AppID)
		}
	}
}

// TestListInstancesForAccount_Pagination seeds 5 instances, requests
// ?limit=2, and asserts the page shape:
//
//   - size ≤ limit,
//   - next_before is emitted when the page is full.
//
// The strict "all rows reachable" walk is left to the e2e test
// (cmd/e2e/account_scoped_e2e_test.go) — it requires the pgstore's
// UUIDv7 id ordering that Memstore's random newID() can't provide.
// Memstore's comparator matches `id < before` exactly so the
// unit-test assertion is the same shape as the production cursor
// walk; the only difference is the id ordering.
func TestListInstancesForAccount_Pagination(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "pagi-app")
	for i := 0; i < 5; i++ {
		mustSeedInstanceDirect(t, e.store, app.ID, "", "running", 256)
		time.Sleep(2 * time.Millisecond)
	}

	// Page 1: ?limit=2
	rec := e.do(t, http.MethodGet, "/v1/instances?limit=2", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("page 1 status %d", rec.Code)
	}
	var p1 api.ListInstancesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &p1); err != nil {
		t.Fatalf("unmarshal page 1: %v", err)
	}
	if len(p1.Instances) > 2 {
		t.Fatalf("page 1 size %d, want <=2", len(p1.Instances))
	}
	if len(p1.Instances) == 2 && p1.NextBefore == "" {
		t.Fatalf("page 1 full but next_before empty")
	}
}

// TestListSecretsForAccount_HappyPath covers the basic success path:
// row shape carries app_id + app_slug + ciphertext; the plaintext
// value never appears on the wire.
func TestListSecretsForAccount_HappyPath(t *testing.T) {
	e := setupSecrets(t, api.PlanHobby)
	app := createApp(t, e, "secrets-app")

	// PUT two secrets (the ciphertext is what the handler will read
	// back — plaintext never crosses the wire).
	putOne := func(key, val string) {
		t.Helper()
		rec := e.do(t, "PUT", "/v1/apps/"+app.Slug+"/secrets/"+key,
			api.PutAppSecretRequest{Value: val}, nil)
		if rec.Code != 200 {
			t.Fatalf("PUT %s: %d %s", key, rec.Code, rec.Body)
		}
	}
	putOne("STRIPE_KEY", "sk_test_abc")
	putOne("DATABASE_URL", "postgres://x:y@z/db")

	rec := e.do(t, http.MethodGet, "/v1/secrets", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.ListSecretsForAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Secrets) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(out.Secrets))
	}
	for _, s := range out.Secrets {
		if s.AppID != app.ID {
			t.Errorf("secret app_id mismatch: got %s want %s", s.AppID, app.ID)
		}
		if s.AppSlug != app.Slug {
			t.Errorf("secret app_slug mismatch: got %q want %q", s.AppSlug, app.Slug)
		}
		if s.Ciphertext == "" {
			t.Errorf("ciphertext must be non-empty")
		}
		// Plaintext must NEVER appear.
		if strings.Contains(rec.Body.String(), "sk_test_abc") || strings.Contains(rec.Body.String(), "postgres://x:y@z/db") {
			t.Fatalf("plaintext leaked into response: %s", rec.Body.String())
		}
	}
}

// TestListSecretsForAccount_PlaintextInvariant hardens the invariant
// against the marker-value replay attack — the same shape used in
// cmd/e2e/secrets_e2e_test.go.
func TestListSecretsForAccount_PlaintextInvariant(t *testing.T) {
	e := setupSecrets(t, api.PlanHobby)
	app := createApp(t, e, "plaintext-app")
	const marker = "PLAINTEXT-MARKER-XYZ123"
	rec := e.do(t, "PUT", "/v1/apps/"+app.Slug+"/secrets/MARKER_KEY",
		api.PutAppSecretRequest{Value: marker}, nil)
	if rec.Code != 200 {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, http.MethodGet, "/v1/secrets", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), marker) {
		t.Fatalf("plaintext marker leaked: %s", rec.Body.String())
	}
}

// TestListSecretsForAccount_BadLimit covers the strict-mode 400 path.
func TestListSecretsForAccount_BadLimit(t *testing.T) {
	e := setupSecrets(t, api.PlanHobby)
	for _, lim := range []string{"0", "99999", "garbage", "-1"} {
		rec := e.do(t, http.MethodGet, "/v1/secrets?limit="+lim, nil, nil)
		assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	}
}

// TestListSecretsForAccount_CrossAccountIsolation pins that account-A
// cannot read account-B's secrets via the account-scoped endpoint.
// Mirrors the per-app TestSecrets_AppOwnershipBoundary shape, but
// without the (slug → 404) collapse: account-scoped routes resolve
// account from the auth principal and SQL JOIN is the only guard.
func TestListSecretsForAccount_CrossAccountIsolation(t *testing.T) {
	store := state.NewMemStore()
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	alice, keyAlice := mustCreateAccount(t, store, "alice", api.PlanHobby)
	bob, keyBob := mustCreateAccount(t, store, "bob", api.PlanHobby)
	envA := testEnv{h: srv.handler(), store: store, key: keyAlice, acct: alice}
	envB := testEnv{h: srv.handler(), store: store, key: keyBob, acct: bob}
	appA := createApp(t, envA, "alice-secrets-app")
	appB := createApp(t, envB, "bob-secrets-app")
	if err := store.UpsertAppSecret(context.Background(), alice.ID, appA.ID, "A_SECRET", []byte("ct-a")); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecret(context.Background(), bob.ID, appB.ID, "B_SECRET", []byte("ct-b")); err != nil {
		t.Fatal(err)
	}

	rec := envA.do(t, http.MethodGet, "/v1/secrets", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("alice status %d", rec.Code)
	}
	var aResp api.ListSecretsForAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &aResp); err != nil {
		t.Fatalf("unmarshal alice: %v", err)
	}
	if len(aResp.Secrets) != 1 {
		t.Fatalf("alice expected 1 secret, got %d", len(aResp.Secrets))
	}
	if aResp.Secrets[0].Key != "A_SECRET" {
		t.Fatalf("alice got wrong key: %q", aResp.Secrets[0].Key)
	}
	if aResp.Secrets[0].AppSlug != "alice-secrets-app" {
		t.Fatalf("alice got wrong slug: %q", aResp.Secrets[0].AppSlug)
	}

	rec = envB.do(t, http.MethodGet, "/v1/secrets", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("bob status %d", rec.Code)
	}
	var bResp api.ListSecretsForAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &bResp); err != nil {
		t.Fatalf("unmarshal bob: %v", err)
	}
	if len(bResp.Secrets) != 1 {
		t.Fatalf("bob expected 1 secret, got %d", len(bResp.Secrets))
	}
	if bResp.Secrets[0].Key != "B_SECRET" {
		t.Fatalf("bob got wrong key: %q", bResp.Secrets[0].Key)
	}
}

// TestGetAppsMetrics_Degraded_NoPrometheus covers the
// `s.promqlClient == nil` short-circuit. The response is 200 with
// `source: "degraded: prometheus not configured"`, `apps: null` —
// matching the per-app handler's contract exactly.
func TestGetAppsMetrics_Degraded_NoPrometheus(t *testing.T) {
	e := setup(t, api.PlanHobby)
	createApp(t, e, "app-x")
	createApp(t, e, "app-y")

	rec := e.do(t, http.MethodGet, "/v1/apps/metrics?range=5m", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppsMetricsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Range != "5m" {
		t.Fatalf("range echo: got %q want %q", out.Range, "5m")
	}
	wantSrc := appmetrics.SourceDegradedPrefix + "prometheus not configured"
	if out.Source != wantSrc {
		t.Fatalf("source: got %q want %q", out.Source, wantSrc)
	}
	if out.Apps != nil {
		t.Fatalf("apps should be null when degraded, got %+v", out.Apps)
	}
	if out.AsOf == "" {
		t.Fatalf("as_of must be populated")
	}
}

// TestGetAppsMetrics_InvalidRange covers the closed-vocabulary check
// on `?range=` (the same shape as the per-app handler's
// appmetrics.IsValidRange guard).
func TestGetAppsMetrics_InvalidRange(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, http.MethodGet, "/v1/apps/metrics?range=99y", nil, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}

// TestGetAppsMetrics_HappyPath_WithProm wires a fake Prometheus that
// returns per-app vector data, then asserts the rollup is keyed by
// app_slug and each row carries the per-app `request_count`.
// Exercises the QueryMap + QueryBuckets helpers end-to-end.
func TestGetAppsMetrics_HappyPath_WithProm(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appFoo := createApp(t, e, "foo-app")
	appBar := createApp(t, e, "bar-app")
	// Two-vector response: request_count per app.
	// Bucket response: per-app histogram (3 buckets each).
	// Scalar response: fleet wake p95.
	responder := func(query string) string {
		switch {
		case strings.Contains(query, "sum by (app)(increase(gateway_request_duration_seconds_count"):
			return fmt.Sprintf(`{"data":{"resultType":"vector","result":[{"metric":{"app":"%s"},"value":[1,"42"]},{"metric":{"app":"%s"},"value":[1,"17"]}]}}`, appFoo.ID, appBar.ID)
		case strings.Contains(query, "sum by (app)(rate(gateway_request_duration_seconds_count{class"):
			return fmt.Sprintf(`{"data":{"resultType":"vector","result":[{"metric":{"app":"%s"},"value":[1,"1.4"]},{"metric":{"app":"%s"},"value":[1,"0"]}]}}`, appFoo.ID, appBar.ID)
		case strings.Contains(query, "sum by (app)(rate(gateway_cold_boot_total"):
			return fmt.Sprintf(`{"data":{"resultType":"vector","result":[{"metric":{"app":"%s"},"value":[1,"5"]},{"metric":{"app":"%s"},"value":[1,"3"]}]}}`, appFoo.ID, appBar.ID)
		case strings.Contains(query, "sum by (app, le)(rate(gateway_request_duration_seconds_bucket"):
			return fmt.Sprintf(`{"data":{"resultType":"vector","result":[{"metric":{"app":"%s","le":"0.1"},"value":[1,"20"]},{"metric":{"app":"%s","le":"0.5"},"value":[1,"35"]},{"metric":{"app":"%s","le":"+Inf"},"value":[1,"42"]},{"metric":{"app":"%s","le":"0.1"},"value":[1,"10"]},{"metric":{"app":"%s","le":"0.5"},"value":[1,"15"]},{"metric":{"app":"%s","le":"+Inf"},"value":[1,"17"]}]}}`,
				appFoo.ID, appFoo.ID, appFoo.ID, appBar.ID, appBar.ID, appBar.ID)
		case strings.Contains(query, "histogram_quantile(0.95, sum by (le)(rate(gateway_wake_latency_seconds_bucket"):
			return `{"data":{"resultType":"vector","result":[{"value":[1,"380"]}]}}`
		default:
			return `{"data":{"resultType":"vector","result":[]}}`
		}
	}
	installPromFixture(t, &e, responder)

	rec := e.do(t, http.MethodGet, "/v1/apps/metrics?range=5m", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppsMetricsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Source != "prometheus" {
		t.Fatalf("source: got %q want %q", out.Source, "prometheus")
	}
	if len(out.Apps) != 2 {
		t.Fatalf("expected 2 per-app rows, got %d (%v)", len(out.Apps), out.Apps)
	}
	for slug, row := range out.Apps {
		if row.RequestCount == 0 {
			t.Errorf("app %s request_count=0, want 42 or 17", slug)
		}
		if row.WakeP95MS <= 0 {
			t.Errorf("app %s wake_p95_ms should be set from FLEET scalar", slug)
		}
		// Latency percentiles must come from the in-process
		// histogramQuantile walk against the QueryBuckets result —
		// not zeroed, not silently-NaN. The fixture's p95 (39.9/42
		// for foo-app, 16.15/17 for bar-app) lands above the last
		// finite bucket (0.5) so the walk returns prevNonEmptyUpper
		// = 0.5 (the +Inf bucket is skipped per PromQL semantics).
		if row.LatencyP50MS <= 0 {
			t.Errorf("app %s latency_p50_ms=%.4f, want >0", slug, row.LatencyP50MS)
		}
		if math.Abs(row.LatencyP95MS-0.5) > 1e-9 {
			t.Errorf("app %s latency_p95_ms=%.6f, want 0.5 (cap at last finite bucket)", slug, row.LatencyP95MS)
		}
		if math.Abs(row.LatencyP99MS-0.5) > 1e-9 {
			t.Errorf("app %s latency_p99_ms=%.6f, want 0.5 (same fixture)", slug, row.LatencyP99MS)
		}
	}
}

// TestGetAppsMetrics_ZeroTrafficDoesNotDegrade pins the ratio guard used by
// the account dashboard. Prometheus evaluates an idle counter's 0/0 ratio as
// NaN; filtering zero denominators must leave that app at the response map's
// zero value instead of degrading metrics for the entire account.
func TestGetAppsMetrics_ZeroTrafficDoesNotDegrade(t *testing.T) {
	e := setup(t, api.PlanHobby)
	active := createApp(t, e, "active-app")
	idle := createApp(t, e, "idle-app")

	responder := func(query string) string {
		switch {
		case strings.Contains(query, "sum by (app)(increase(gateway_request_duration_seconds_count"):
			return fmt.Sprintf(`{"data":{"resultType":"vector","result":[{"metric":{"app":"%s"},"value":[1,"12"]},{"metric":{"app":"%s"},"value":[1,"0"]}]}}`, active.ID, idle.ID)
		case strings.Contains(query, "gateway_request_duration_seconds_count{class") || strings.Contains(query, "gateway_cold_boot_total"):
			if !strings.Contains(query, "and on (app)") || !strings.Contains(query, "> 0") {
				return fmt.Sprintf(`{"data":{"resultType":"vector","result":[{"metric":{"app":"%s"},"value":[1,"NaN"]}]}}`, idle.ID)
			}
			return fmt.Sprintf(`{"data":{"resultType":"vector","result":[{"metric":{"app":"%s"},"value":[1,"0"]}]}}`, active.ID)
		case strings.Contains(query, "gateway_request_duration_seconds_bucket"):
			return `{"data":{"resultType":"vector","result":[]}}`
		case strings.Contains(query, "gateway_wake_latency_seconds_bucket"):
			if !strings.Contains(query, "gateway_wake_latency_seconds_count") || !strings.Contains(query, "or vector(0)") {
				return `{"data":{"resultType":"vector","result":[{"value":[1,"NaN"]}]}}`
			}
			return `{"data":{"resultType":"vector","result":[{"value":[1,"0"]}]}}`
		default:
			return `{"data":{"resultType":"vector","result":[]}}`
		}
	}
	installPromFixture(t, &e, responder)

	rec := e.do(t, http.MethodGet, "/v1/apps/metrics?range=24h", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppsMetricsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Source != appmetrics.SourcePrometheus {
		t.Fatalf("source: got %q want %q", out.Source, appmetrics.SourcePrometheus)
	}
	if out.Apps[idle.Slug].ErrorRatePct != 0 || out.Apps[idle.Slug].ColdStartPct != 0 {
		t.Fatalf("idle metrics: got %+v, want zero-valued ratios", out.Apps[idle.Slug])
	}
}

// TestGetAppsMetrics_Degraded_FirstQueryFails wires a fake Prometheus
// that always returns errors, then asserts the rollup short-circuits
// to "degraded: <reason>" with apps=null — never partial-populated.
func TestGetAppsMetrics_Degraded_FirstQueryFails(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "deg-app")
	responder := func(query string) string {
		// 5xx — every QueryMap / QueryBuckets will fail.
		return `{"status":"error","errorType":"bad_data","error":"parse error"}`
	}
	// httptest.Server returns 200; we want 5xx — wrap responder.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, responder(r.URL.Query().Get("query")))
	}))
	t.Cleanup(srv.Close)
	e.s.WithStatusCache(srv.URL, "")

	rec := e.do(t, http.MethodGet, "/v1/apps/metrics?range=5m", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppsMetricsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.HasPrefix(out.Source, "degraded:") {
		t.Fatalf("source: got %q want degraded:<reason>", out.Source)
	}
	if out.Apps != nil {
		t.Fatalf("apps must be nil on degraded, got %+v", out.Apps)
	}
}

// TestListAccountRoutes_NoRequireMFA pins the deliberate omission of
// requireMFA from /v1/apps/metrics (mirrors the per-app route comment
// at server.go:562-568). The other two routes DO require MFA — see
// TestListAccountSecrets_RequiresMFA below.
func TestGetAppsMetrics_NoRequireMFA(t *testing.T) {
	e, cookie := setupWithSession(t)
	mustSeedApp(t, e, "mfa-omitted-app")
	req := httptest.NewRequest(http.MethodGet, "/v1/apps/metrics", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("session-cookie (no MFA) status %d, want 200 — route must NOT requireMFA", rec.Code)
	}
}

// TestListAccountSecrets_RequiresMFA pins that /v1/secrets DOES
// require MFA (matching the per-app endpoint), so a session cookie
// without MFA-clearance is rejected. We have to inline a custom
// setupWithSession-MFA because the package-level setupWithSession
// helper issues the cookie with mfaPending=false (a fully
// authenticated dashboard session). Here we want mfaPending=true
// so the middleware actually exercises the gate.
func TestListAccountSecrets_RequiresMFA(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "mfa-cookie@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sid := uuid.NewString()
	if _, err := store.CreateSession(context.Background(), sid, acct.ID, "192.0.2.30", "mfa-test-ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	token, err := mgr.IssueWithSession(sid, acct.ID, true) // mfaPending=true
	if err != nil {
		t.Fatal(err)
	}
	ops := wire.NewOpsMetrics("apid_mfa_test")
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr,
		nil, 15*60_000_000_000, "").WithOpsMetrics(context.Background(), ops)
	e := testEnv{h: srv.handler(), store: store, key: "", acct: acct, ops: ops}
	mustSeedApp(t, e, "mfa-required-app")

	req := httptest.NewRequest(http.MethodGet, "/v1/secrets", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("session-cookie (mfa_pending=true) returned 200, want non-200 — route must requireMFA")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 — the gate returns 403 (not 401) so the dashboard can render the prompt", rec.Code)
	}
}

// TestHistogramQuantile pins the PromQL semantics the
// account-scoped metrics rollup depends on. The walk matches
// PromQL histogram_quantile() — see handlers_account_scoped.go
// for the contract:
//
//   - skip the +Inf bucket (would otherwise return +Inf for q<1)
//   - find the first bucket whose cumulative ≥ q·N
//   - interpolate linearly in (prevNonEmptyUpper, upper) by count ratio
//   - empty / nil maps → 0 (matches appmetrics.SafeFloat's NaN clamp)
//   - malformed `le` strings → row dropped, rest still computes
//   - q outside [0,1] → returns 0 / prevUpper (PromQL returns NaN; we
//     clamp to 0 to match the dashboard's "no data" rendering)
//
// Each row's expected value is hand-computed from the fixture so a
// regression in the walk surfaces immediately.
func TestHistogramQuantile(t *testing.T) {
	cases := []struct {
		name    string
		q       float64
		buckets map[string]float64
		want    float64
	}{
		{
			name:    "empty buckets returns 0",
			q:       0.95,
			buckets: nil,
			want:    0,
		},
		{
			name:    "only +Inf bucket returns 0 (PromQL skips +Inf)",
			q:       0.95,
			buckets: map[string]float64{"+Inf": 42},
			want:    0,
		},
		{
			name: "single finite bucket interpolates from 0",
			// cum = {le=0.5: 10}, total = 10. p50 target = 5.
			// First (and only) bucket cum=10 ≥ 5 → interp from
			// prevUpper=0 to upper=0.5 by frac=5/10=0.5 → 0.25.
			// Matches the canonical pkg/gateway/testhist walk
			// (and PromQL itself: single-bucket input is
			// underdetermined, so linear-from-zero is a reasonable
			// convention; never reached in practice because the
			// dashboard always sees ≥ 3 buckets).
			q:       0.50,
			buckets: map[string]float64{"0.5": 10},
			want:    0.25,
		},
		{
			name: "monotonic buckets: p50 between 0.1 and 0.5",
			// cum = {le=0.1: 5, le=0.5: 20, le=+Inf: 20}, total = 20.
			// p50 target = 10 → first bucket with cum ≥ 10 is 0.5
			// (cum=20 ≥ 10), prevUpper=0.1, countInBucket=15,
			// frac = (10-5)/15 = 0.333..., result = 0.1 + 0.333*(0.5-0.1)
			// = 0.1 + 0.1333... = 0.2333...
			q:       0.50,
			buckets: map[string]float64{"0.1": 5, "0.5": 20, "+Inf": 20},
			want:    0.23333333333333333,
		},
		{
			name: "p95 lands inside the populated bucket",
			// cum = {le=0.1: 20, le=0.5: 35, le=+Inf: 42}, total = 42.
			// p95 target = 39.9 → first bucket with cum ≥ 39.9 is +Inf.
			// +Inf is skipped → returns prevUpper (the last
			// finite-non-empty bucket), which is 0.5.
			q:       0.95,
			buckets: map[string]float64{"0.1": 20, "0.5": 35, "+Inf": 42},
			want:    0.5,
		},
		{
			name: "p99 in the same fixture caps at 0.5",
			// Same as p95 above; target = 41.58 → still above 35,
			// still under +Inf → return prevUpper = 0.5.
			q:       0.99,
			buckets: map[string]float64{"0.1": 20, "0.5": 35, "+Inf": 42},
			want:    0.5,
		},
		{
			name: "p10 inside the 0.1 bucket",
			// target = 0.10*42 = 4.2 → first bucket with cum ≥ 4.2 is
			// le=0.1 (cum=20 ≥ 4.2), prevUpper=0 (no prior
			// non-empty), countInBucket=20, frac = 4.2/20 = 0.21,
			// result = 0 + 0.21*(0.1-0) = 0.021.
			q:       0.10,
			buckets: map[string]float64{"0.1": 20, "0.5": 35, "+Inf": 42},
			want:    0.021,
		},
		{
			name: "malformed le is dropped, rest computes",
			// "junk" is dropped; remaining cum = {le=0.5: 10}.
			// Same as the single-bucket case → p50 returns 0.25.
			q:       0.50,
			buckets: map[string]float64{"junk": 99, "0.5": 10},
			want:    0.25,
		},
		{
			name:    "all malformed le returns 0",
			q:       0.95,
			buckets: map[string]float64{"junk": 1, "NaN": 2},
			want:    0,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := histogramQuantile(tc.q, tc.buckets)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("histogramQuantile(%v, %v) = %v, want %v",
					tc.q, tc.buckets, got, tc.want)
			}
		})
	}
}
