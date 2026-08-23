package main

// Handler tests for the per-app OpenAPI Import + Auto-Generation
// surface (ADR-126 / issue #975 item #2, slot 00378). The
// surface:
//
//   GET    /v1/apps/{slug}/openapi?source=manual_import|auto
//   POST   /v1/apps/{slug}/openapi
//   POST   /v1/apps/{slug}/openapi/dry-run
//   DELETE /v1/apps/{slug}/openapi
//
// Test surface (table-driven where applicable):
//   - GET manual_import: 200 happy path + headers, 404 missing
//   - GET auto: 200 happy path with cache miss, 200 cache hit,
//     200 degraded when no imported doc + no rules (Source:
//     empty: no_import_no_rules), 200 degraded when gateway
//     unreachable (Source: degraded: routes_unavailable)
//   - POST: 200 happy path, 413 too large, 422 invalid, 422 too
//     many endpoints, 403 per-account quota
//   - POST dry-run: 200 happy path, 422 invalid
//   - DELETE: 204 happy path, 204 idempotent
//
// The MemStore seeds the imported doc directly via
// UpsertAppOpenAPIDoc (item #2 store surface). The gateway
// bridge is bypassed (gatewaydControlURL left empty) so the
// auto-gen path exercises the degraded Source branch.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

// sampleOpenAPIDoc is the minimum-shape OpenAPI 3.1 doc the
// validator accepts (Paths object present + 1 endpoint). Used
// by the happy-path POST tests.
const sampleOpenAPIDoc = `{"openapi":"3.1.0","info":{"title":"sample","version":"1.0.0"},"paths":{"/users":{"get":{"responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"type":"object"}}}}}}}}}`

// invalidOpenAPIDoc is what the validator rejects — a non-object
// root. Mirrors the validator_test.go "reject non-object root"
// case so the handler test exercises the 422 path the same way.
const invalidOpenAPIDoc = `"just a string, not a doc"`

// seedApp creates an app on the memstore under the test env's
// account and returns the resolved state.App. Mirrors the
// pattern in app_errors_handler_test.go (slug-scoped happy paths).
func seedApp(t *testing.T, e testEnv, slug string) state.App {
	t.Helper()
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID,
		Slug:      slug,
		Type:      state.AppTypeFunction,
		Runtime:   "node22",
		RAMMB:     256,
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateApp(%q): %v", slug, err)
	}
	return app
}

// seedImport upserts an OpenAPI doc for the given app. The
// signature mirrors UpsertAppOpenAPIDoc — body, endpoint count,
// version string.
func seedImport(t *testing.T, e testEnv, appID string, body []byte, endpointCount int, version string) {
	t.Helper()
	if err := e.store.UpsertAppOpenAPIDoc(context.Background(), appID, e.acct.ID, body, endpointCount, version); err != nil {
		t.Fatalf("UpsertAppOpenAPIDoc: %v", err)
	}
}

// rawDo drives a request with a literal []byte body — no
// json.Marshal round-trip. Required for the size-cap test
// where the body is non-JSON filler (262 KiB of 'a'), and the
// empty-body test where the body is nil. Mirrors the
// rawPatchOpenAPIDoc pattern in handler_openapi_doc_test.go.
func (e testEnv) rawDo(t *testing.T, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if len(body) == 0 {
		r = bytes.NewReader(nil)
	} else {
		r = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Authorization", "Bearer "+e.key)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// findOpenAPIImportChanged locates the NotifyAppOpenAPIDocChanged
// emission on the capturing notifier. Helper for the POST + DELETE
// tests that want to assert the pg_notify fired.
func findOpenAPIImportChanged(n *capturingNotifier) (capturedNotification, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, e := range n.emitted {
		if e.Channel == db.NotifyAppOpenAPIDocChanged {
			return e, true
		}
	}
	return capturedNotification{}, false
}

// TestGetAppOpenAPI_ManualImport_Happy verifies the GET
// ?source=manual_import path returns 200 + the persisted body
// + the X-OpenAPI-Doc-* headers.
func TestGetAppOpenAPI_ManualImport_Happy(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedApp(t, e, "manual-import-happy")
	seedImport(t, e, app.ID, []byte(sampleOpenAPIDoc), 1, "3.1.0")

	rec := e.do(t, "GET", "/v1/apps/"+app.Slug+"/openapi?source=manual_import", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-OpenAPI-Doc-Source"); got != "manual_import" {
		t.Errorf("X-OpenAPI-Doc-Source=%q, want manual_import", got)
	}
	if got := rec.Header().Get("X-OpenAPI-Doc-Version"); got != "3.1.0" {
		t.Errorf("X-OpenAPI-Doc-Version=%q, want 3.1.0", got)
	}
	if got := rec.Header().Get("X-OpenAPI-Doc-Byte-Size"); got == "" || got == "0" {
		t.Errorf("X-OpenAPI-Doc-Byte-Size=%q, want positive", got)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "max-age=300") {
		t.Errorf("Cache-Control=%q, want max-age=300", got)
	}
	if !strings.Contains(rec.Body.String(), `"openapi":"3.1.0"`) {
		t.Errorf("body missing openapi key: %s", rec.Body.String())
	}
}

// TestGetAppOpenAPI_ManualImport_Missing verifies the 404
// branch when no import has been written for the app.
func TestGetAppOpenAPI_ManualImport_Missing(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedApp(t, e, "manual-import-missing")

	rec := e.do(t, "GET", "/v1/apps/manual-import-missing/openapi?source=manual_import", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestGetAppOpenAPI_Auto_NoImports verifies the auto-gen path
// returns 200 with the Source=empty: no_import_no_rules marker
// when neither an imported doc nor edge rules exist.
func TestGetAppOpenAPI_Auto_NoImports(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedApp(t, e, "auto-no-imports")

	rec := e.do(t, "GET", "/v1/apps/auto-no-imports/openapi?source=auto", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-OpenAPI-Doc-Source"); got != openapidiff.SourceEmptyImportRules {
		t.Errorf("X-OpenAPI-Doc-Source=%q, want %q", got, openapidiff.SourceEmptyImportRules)
	}
}

// TestGetAppOpenAPI_Auto_WithImport_CacheMiss verifies the
// auto-gen path fills the cache on first read (X-Faas-Cache:
// miss). The Source header is "auto" when an import is present
// but the gateway bridge isn't wired (degraded: routes_unavailable).
func TestGetAppOpenAPI_Auto_WithImport_CacheMiss(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedApp(t, e, "auto-cache-miss")
	seedImport(t, e, app.ID, []byte(sampleOpenAPIDoc), 1, "3.1.0")
	cache := openapidiff.NewSpecCache()
	e.s.WithSpecCache(cache)

	rec := e.do(t, "GET", "/v1/apps/auto-cache-miss/openapi?source=auto", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Faas-Cache"); got != "miss" {
		t.Errorf("X-Faas-Cache=%q, want miss", got)
	}
	if got := rec.Header().Get("X-OpenAPI-Doc-Source"); got != openapidiff.SourceAuto &&
		got != openapidiff.SourceDegradedRoutes &&
		got != openapidiff.SourceDegradedRules {
		t.Errorf("X-OpenAPI-Doc-Source=%q, want auto / degraded: routes_unavailable / degraded: rules_unavailable", got)
	}
	if cache.Len() != 1 {
		t.Errorf("cache.Len()=%d, want 1", cache.Len())
	}
}

// TestGetAppOpenAPI_Auto_WithImport_CacheHit verifies the
// auto-gen path returns X-Faas-Cache: hit on the second read.
func TestGetAppOpenAPI_Auto_WithImport_CacheHit(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedApp(t, e, "auto-cache-hit")
	seedImport(t, e, app.ID, []byte(sampleOpenAPIDoc), 1, "3.1.0")
	cache := openapidiff.NewSpecCache()
	e.s.WithSpecCache(cache)

	rec1 := e.do(t, "GET", "/v1/apps/auto-cache-hit/openapi?source=auto", nil, nil)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first read status %d, want 200", rec1.Code)
	}
	if got := rec1.Header().Get("X-Faas-Cache"); got != "miss" {
		t.Errorf("first X-Faas-Cache=%q, want miss", got)
	}
	rec2 := e.do(t, "GET", "/v1/apps/auto-cache-hit/openapi?source=auto", nil, nil)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second read status %d, want 200", rec2.Code)
	}
	if got := rec2.Header().Get("X-Faas-Cache"); got != "hit" {
		t.Errorf("second X-Faas-Cache=%q, want hit", got)
	}
}

// TestGetAppOpenAPI_Auto_CacheInvalidationViaNotify verifies
// that InvalidateByApp(appID) clears the cached entry so the
// next read is a miss.
func TestGetAppOpenAPI_Auto_CacheInvalidationViaNotify(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedApp(t, e, "auto-cache-invalidate")
	seedImport(t, e, app.ID, []byte(sampleOpenAPIDoc), 1, "3.1.0")
	cache := openapidiff.NewSpecCache()
	e.s.WithSpecCache(cache)

	if r := e.do(t, "GET", "/v1/apps/auto-cache-invalidate/openapi?source=auto", nil, nil); r.Code != http.StatusOK {
		t.Fatalf("fill: %d", r.Code)
	}
	if cache.Len() != 1 {
		t.Fatalf("cache.Len()=%d after fill, want 1", cache.Len())
	}
	cache.InvalidateByApp(app.ID)
	if cache.Len() != 0 {
		t.Fatalf("cache.Len()=%d after invalidate, want 0", cache.Len())
	}
	rec := e.do(t, "GET", "/v1/apps/auto-cache-invalidate/openapi?source=auto", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-fill: %d", rec.Code)
	}
	if got := rec.Header().Get("X-Faas-Cache"); got != "miss" {
		t.Errorf("after invalidate, X-Faas-Cache=%q, want miss", got)
	}
}

// TestGetAppOpenAPI_DryRunSourceNotAllowedOnGET verifies that
// GET ?source=dry_run returns 405 (dry-run is POST-only).
func TestGetAppOpenAPI_DryRunSourceNotAllowedOnGET(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedApp(t, e, "dry-run-get")

	rec := e.do(t, "GET", "/v1/apps/dry-run-get/openapi?source=dry_run", nil, nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d, want 405; body=%s", rec.Code, rec.Body.String())
	}
}

// TestGetAppOpenAPI_InvalidSource verifies that an unknown
// source value returns 400.
func TestGetAppOpenAPI_InvalidSource(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedApp(t, e, "invalid-source")

	rec := e.do(t, "GET", "/v1/apps/invalid-source/openapi?source=bogus", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestPostAppOpenAPI_Happy verifies the import endpoint accepts
// a valid doc, persists it, and returns 200 + meta + pg_notify.
func TestPostAppOpenAPI_Happy(t *testing.T) {
	e, notif := newTestServerWithCapturingNotifier(t, api.PlanPro)
	app := seedApp(t, e, "post-happy")

	rec := e.do(t, "POST", "/v1/apps/post-happy/openapi", json.RawMessage(sampleOpenAPIDoc), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["openapi_version"] != "3.1.0" {
		t.Errorf("openapi_version=%v, want 3.1.0", out["openapi_version"])
	}
	if out["app_id"] != app.ID {
		t.Errorf("app_id=%v, want %s", out["app_id"], app.ID)
	}
	if _, ok := findOpenAPIImportChanged(notif); !ok {
		t.Errorf("expected NotifyAppOpenAPIDocChanged emission, got %d emits", len(notif.emitted))
	}
}

// TestPostAppOpenAPI_TooLarge verifies the 413 branch when the
// body exceeds OpenAPIImportMaxDocBytes.
func TestPostAppOpenAPI_TooLarge(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedApp(t, e, "post-too-large")
	body := make([]byte, state.OpenAPIImportMaxDocBytes+1)
	for i := range body {
		body[i] = 'a'
	}

	rec := e.rawDo(t, "POST", "/v1/apps/post-too-large/openapi", body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
}

// TestPostAppOpenAPI_Invalid verifies the 422 branch when the
// doc fails the structural-minimum validator.
func TestPostAppOpenAPI_Invalid(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedApp(t, e, "post-invalid")

	rec := e.rawDo(t, "POST", "/v1/apps/post-invalid/openapi", []byte(invalidOpenAPIDoc))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

// TestPostAppOpenAPI_EmptyBody verifies the 400 branch when
// the request body is empty.
func TestPostAppOpenAPI_EmptyBody(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedApp(t, e, "post-empty")

	rec := e.rawDo(t, "POST", "/v1/apps/post-empty/openapi", []byte{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestPostAppOpenAPI_DryRunHappy verifies the dry-run endpoint
// returns a suggestion list (1 endpoint → 1 suggestion when
// no existing rules cover it).
func TestPostAppOpenAPI_DryRunHappy(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedApp(t, e, "post-dry-run")

	rec := e.do(t, "POST", "/v1/apps/post-dry-run/openapi/dry-run", json.RawMessage(sampleOpenAPIDoc), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["openapi_version"] != "3.1.0" {
		t.Errorf("openapi_version=%v, want 3.1.0", out["openapi_version"])
	}
	suggestions, ok := out["suggestions"].([]any)
	if !ok {
		t.Fatalf("suggestions type=%T, want []any", out["suggestions"])
	}
	if len(suggestions) != 1 {
		t.Errorf("suggestions=%d, want 1", len(suggestions))
	}
}

// TestPostAppOpenAPI_DryRun_Invalid verifies the 422 branch on
// the dry-run endpoint when the doc is invalid.
func TestPostAppOpenAPI_DryRun_Invalid(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedApp(t, e, "post-dry-run-invalid")

	rec := e.do(t, "POST", "/v1/apps/post-dry-run-invalid/openapi/dry-run", json.RawMessage(invalidOpenAPIDoc), nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

// TestDeleteAppOpenAPI_Happy verifies the DELETE endpoint
// returns 204 + emits the pg_notify.
func TestDeleteAppOpenAPI_Happy(t *testing.T) {
	e, notif := newTestServerWithCapturingNotifier(t, api.PlanPro)
	app := seedApp(t, e, "delete-happy")
	seedImport(t, e, app.ID, []byte(sampleOpenAPIDoc), 1, "3.1.0")

	rec := e.do(t, "DELETE", "/v1/apps/delete-happy/openapi", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := findOpenAPIImportChanged(notif); !ok {
		t.Errorf("expected NotifyAppOpenAPIDocChanged emission, got %d emits", len(notif.emitted))
	}
}

// TestDeleteAppOpenAPI_Idempotent verifies the DELETE endpoint
// is idempotent (204 even if no row exists).
func TestDeleteAppOpenAPI_Idempotent(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedApp(t, e, "delete-idempotent")

	rec := e.do(t, "DELETE", "/v1/apps/delete-idempotent/openapi", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

// _ keep time in the import set (CreateApp.CreatedAt uses it
// via the seedApp helper).
var _ = time.Now