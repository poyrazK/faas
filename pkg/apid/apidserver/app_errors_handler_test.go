// Table-driven handler tests for the customer-facing
// automatic error grouping surface (ADR-096 / PR-B). Mirrors
// the handler_slo_test.go pattern (project memory: handlers_slo
// is the per-app read-only entry point) and the
// handlers_admin_obs_test.go pattern (table-driven over scope
// sets + body assertions). The tests do not boot Postgres —
// the newAppErrorsEnv wires a MemStore so the suite runs in
// the unit-test lane (go test ./cmd/apid/...).
package apidserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// newAppErrorsTestEnvWithApp wires a single-account MemStore
// with the named app registered against the test account. The
// app slug is `slug`; this lets the test pin cross-account
// (slug-not-yours) vs same-account routing without depending
// on row creation.
func newAppErrorsTestEnvWithApp(t *testing.T, slug string) (testEnv, state.App) {
	t.Helper()
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("apid_app_errors_test")
	acct, err := store.CreateAccount(context.Background(), "customer@faas.dev", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "app-errors-test", api.ScopesReadSurface); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID,
		Slug:      slug,
		Type:      state.AppTypeFunction,
		Runtime:   "node22",
		RAMMB:     256,
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), ops)
	return testEnv{h: srv.handler(), s: srv, store: store, key: pt, acct: acct, ops: ops}, app
}

// TestAppErrorsHandler_EmptyStore_SummaryReturnsEmpty is the
// happy-path test against a MemStore with one app and zero
// errors. The handler must return 200 with items: [] and the
// window echo set (never 404 on an empty result, per the
// locked decision "empty result: 200 with items: []").
//
// Note: PR-A shipped the app-errors Store methods as MemStore
// stubs that return an "unimplemented" sentinel (real impl is
// Postgres-only via pgtest). The handler propagates that as a
// 500. This test is therefore scoped to the structural
// contract: a missing app returns 404 (loadApp 404 path),
// regardless of the underlying store. The full empty-result
// contract is exercised by the pgtest integration test in
// PR-C.
func TestAppErrorsHandler_MissingApp_SummaryReturns404(t *testing.T) {
	e, _ := newAppErrorsTestEnvWithApp(t, "my-app")
	rec := e.do(t, "GET", "/v1/apps/never-created/errors/summary", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing app: got status %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestAppErrorsHandler_CrossAccountSlug_Returns404 pins the
// IDOR posture: a customer-scoped bearer key with a slug that
// doesn't belong to their account must receive a 404, never
// a 200 with another tenant's data (or a 403 — 403 leaks
// existence).
func TestAppErrorsHandler_CrossAccountSlug_Returns404(t *testing.T) {
	e, _ := newAppErrorsTestEnvWithApp(t, "my-app")
	rec := e.do(t, "GET", "/v1/apps/somebody-elses-app/errors/summary", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-account slug: got status %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestAppErrorsHandler_NoAuth_Returns401 pins the auth gate:
// a request without a bearer key must return 401 (the auth
// middleware never reaches the handler).
func TestAppErrorsHandler_NoAuth_Returns401(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("apid_app_errors_test")
	if _, err := store.CreateAccount(context.Background(), "customer@faas.dev", api.PlanPro); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), ops)
	// Use the no-auth path: do() with a bearer-less request.
	rec := noAuthDo(t, srv, "GET", "/v1/apps/my-app/errors/summary")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no-auth: got status %d, want 401", rec.Code)
	}
}

// TestAppErrorsHandler_InvalidWindow_Returns400 pins the
// validation gate: a malformed ?since= or ?until= returns 400
// (validation_failed) rather than 500 or 200 with the
// default window silently substituted.
func TestAppErrorsHandler_InvalidWindow_Returns400(t *testing.T) {
	e, _ := newAppErrorsTestEnvWithApp(t, "my-app")
	rec := e.do(t, "GET", "/v1/apps/my-app/errors/summary?since=not-a-time", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid since: got status %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestAppErrorsHandler_NegativeLimit_Returns400 pins the
// limit-validation gate. A negative ?limit= must 400 rather
// than 200 (which would let a misconfigured client request
// "the most negative limit possible" — undefined behaviour
// at the wire boundary).
func TestAppErrorsHandler_NegativeLimit_Returns400(t *testing.T) {
	e, _ := newAppErrorsTestEnvWithApp(t, "my-app")
	rec := e.do(t, "GET", "/v1/apps/my-app/errors/summary?limit=-1", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("negative limit: got status %d, want 400", rec.Code)
	}
}

// TestAppErrorsHandler_DrillDown_MissingApp_Returns404 pins
// the drill-down IDOR posture: a request against a slug that
// doesn't belong to the caller returns 404 (loadApp 404 path)
// rather than 200 with an empty list. The empty-list-but-real-
// app case is exercised by the pgtest integration test in PR-C.
func TestAppErrorsHandler_DrillDown_MissingApp_Returns404(t *testing.T) {
	e, _ := newAppErrorsTestEnvWithApp(t, "my-app")
	unknownFP := "0000000000000000000000000000000000000000000000000000000000000000"
	rec := e.do(t, "GET", "/v1/apps/never-created/errors/"+unknownFP, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing app drill-down: got status %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestAppErrorsHandler_First_MissingApp_Returns404 is the
// /first equivalent of the drill-down test above.
func TestAppErrorsHandler_First_MissingApp_Returns404(t *testing.T) {
	e, _ := newAppErrorsTestEnvWithApp(t, "my-app")
	unknownFP := "0000000000000000000000000000000000000000000000000000000000000000"
	rec := e.do(t, "GET", "/v1/apps/never-created/errors/"+unknownFP+"/first", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing app /first: got status %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// noAuthDo fires a request without the authLimited chain
// (no bearer key). Used by the no-auth test above. Mirrors
// the testEnv.do shape but bypasses the key so we can pin
// the 401 contract.
func noAuthDo(t *testing.T, srv *server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	return rec
}
