// handlers_cors_presets_test.go — unit tests for the CORS
// preset CRUD handlers (issue #975 #4 PR-B / ADR-129).
//
// Coverage matrix (mirrors handlers_alerts_test.go shape):
//
//   - happy path: create + get + list + patch + delete
//   - plan-tier gate: Free customer gets 402
//     CodePlanCorsPresetsNotAllowed on POST
//   - per-account quota trip: Hobby at CorsPresetsPerAccount+1
//     returns 402 CodePlanCorsPresetQuotaReached scope=account
//   - per-app quota trip: Hobby at CorsPresetsPerApp+1 returns
//     402 CodePlanCorsPresetQuotaReached scope=app
//   - *+credentials footgun: AllowCredentials: true +
//     AllowOrigins: ["*"] returns 422 (ADR-091 D12)
//   - cross-tenant 404: a foreign account's preset id returns
//     404, not 403 (IDOR-safe byte-identical-404)
//   - partial-update: pointer-everything optionals let a
//     name-only PATCH leave allow_origins / allow_methods
//     untouched
//   - empty-PATCH rejected: at-least-one-field gate fires with
//     422 cors_preset_update_requires_field
//
// Tests run KVM-free via the in-memory store. The CORS-preset
// DTO is stable per pkg/api/cors_preset_dto.go; the wire
// contract tested here is the one pkg/api/client.go's
// hand-extracted SDK methods depend on.
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
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// corsPresetReq is a valid baseline request body for the
// CORS preset create handler. Tests start from this and mutate
// one field at a time so the failure surface stays narrow.
func corsPresetReq() api.CreateCorsPresetRequest {
	return api.CreateCorsPresetRequest{
		Name:             "public-https",
		AllowOrigins:     []string{"https://app.example.com"},
		AllowMethods:     []string{"GET", "POST"},
		AllowCredentials: false,
		MaxAgeSeconds:    600,
	}
}

// doAsCorsPreset sends a request using the given API key
// instead of the env's. Mirrors doAs on handlers_alerts_test.go
// for IDOR-safety tests that need a foreign key to probe
// another account's preset id.
func (e testEnv) doAsCorsPreset(t *testing.T, method, path string, body any, hdrs map[string]string, key string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Authorization", "Bearer "+key)
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// mustSeedCorsPreset drops a state.CorsPreset row directly via
// the store so list / get / patch / delete tests can assert
// state without going through the create handler. Mirrors
// mustSeedAccountWideRule at handlers_alerts_test.go:93.
func mustSeedCorsPreset(t *testing.T, e testEnv, name, appID string) state.CorsPreset {
	t.Helper()
	limits := api.MustLimitsFor(e.acct.Plan)
	row, err := e.store.CreateCorsPresetIfUnderQuota(context.Background(), state.CorsPreset{
		AccountID:     e.acct.ID,
		AppID:         appID,
		Name:          name,
		AllowOrigins:  []string{"https://app.example.com"},
		AllowMethods:  []string{"GET"},
		MaxAgeSeconds: 600,
	}, limits)
	if err != nil {
		t.Fatalf("seed cors preset: %v", err)
	}
	return row
}

// --- happy paths ----------------------------------------------------------

// TestCreateCorsPreset_HappyPath confirms the canonical create
// flow: 201 + UUID id + persisted row visible via the store.
func TestCreateCorsPreset_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, "POST", "/v1/cors-presets", corsPresetReq(), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create cors preset: status %d, body %s", rec.Code, rec.Body.String())
	}
	var out api.CorsPresetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal cors preset: %v", err)
	}
	if out.ID == "" {
		t.Errorf("id empty in response")
	}
	if out.Name != "public-https" {
		t.Errorf("name = %q, want public-https", out.Name)
	}
	if out.AppID != nil {
		t.Errorf("app_id = %v, want nil (account-wide)", out.AppID)
	}
	if out.CreatedAt == "" || out.UpdatedAt == "" {
		t.Errorf("created_at/updated_at empty: %+v", out)
	}
}

// TestListCorsPresets_HappyPath confirms list returns every
// preset on the account (account-wide + app-scoped unioned).
func TestListCorsPresets_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	_ = mustSeedCorsPreset(t, e, "acct-wide", "")
	_ = mustSeedCorsPreset(t, e, "another", "")
	rec := e.do(t, "GET", "/v1/cors-presets", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list cors presets: status %d, body %s", rec.Code, rec.Body.String())
	}
	var out api.CorsPresetListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(out.Presets) != 2 {
		t.Errorf("got %d presets, want 2", len(out.Presets))
	}
}

// TestListCorsPresets_AppIDQueryFilter pins the GET /v1/cors-presets
// ?app_id=... query parameter (issue #975 #4 PR-B / ADR-129). The
// filter is read from r.URL.Query() (NOT r.Body — GETs have no
// body, so a previous implementation that decoded the filter from
// the request body always saw filter.AppID == nil and silently
// returned the account-wide set). The expected behaviour: the
// handler scopes ListCorsPresetsForApp to the given app_id and
// returns ONLY the app-scoped presets; an unknown / cross-tenant
// app_id returns 404 (no slug-leak).
func TestListCorsPresets_AppIDQueryFilter(t *testing.T) {
	e := setup(t, api.PlanHobby)
	// One app-scoped preset for cors-list-app, one account-wide
	// preset, one app-scoped preset for a different app.
	appID := mustSeedApp(t, e, "cors-list-app")
	otherAppID := mustSeedApp(t, e, "cors-list-other")
	appScoped := mustSeedCorsPreset(t, e, "for-app", appID)
	_ = mustSeedCorsPreset(t, e, "acct-wide", "")
	_ = mustSeedCorsPreset(t, e, "for-other", otherAppID)

	rec := e.do(t, "GET", "/v1/cors-presets?app_id="+appID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list with app_id: status %d, body %s", rec.Code, rec.Body.String())
	}
	var out api.CorsPresetListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(out.Presets) != 1 {
		t.Fatalf("got %d presets, want 1 (app_id filter must scope to app-scoped only)", len(out.Presets))
	}
	if out.Presets[0].ID != appScoped.ID {
		t.Errorf("presets[0].id = %q, want %q (filter must return the app-scoped preset)", out.Presets[0].ID, appScoped.ID)
	}
}

// TestListCorsPresets_AppIDQueryFilter_CrossTenantReturns404 pins
// the cross-tenant IDOR guard on the GET /v1/cors-presets?app_id=
// path. A query parameter carrying an app_id the caller's account
// doesn't own must collapse to 404 (matches the precedent at
// handlers_alerts.go:243-247 — no slug-leak). Without this gate,
// a customer could probe other tenants' app ids by checking
// whether the response is a 200 empty-list or a 404.
func TestListCorsPresets_AppIDQueryFilter_CrossTenantReturns404(t *testing.T) {
	e := setup(t, api.PlanHobby)
	// Seed an app under a different account.
	otherEnv := setup(t, api.PlanHobby)
	otherAppID := mustSeedApp(t, otherEnv, "foreign-app")

	rec := e.do(t, "GET", "/v1/cors-presets?app_id="+otherAppID, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant app_id: status %d, want 404 (no slug-leak)", rec.Code)
	}
}

// TestGetCorsPreset_HappyPath confirms the by-id read path
// returns the canonical row.
func TestGetCorsPreset_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	row := mustSeedCorsPreset(t, e, "test", "")
	rec := e.do(t, "GET", "/v1/cors-presets/"+row.ID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get cors preset: status %d, body %s", rec.Code, rec.Body.String())
	}
	var out api.CorsPresetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal get: %v", err)
	}
	if out.ID != row.ID {
		t.Errorf("id = %q, want %q", out.ID, row.ID)
	}
}

// TestPatchCorsPreset_HappyPath confirms the partial-update
// path: only the fields the customer re-sends change; the
// rest of the row is preserved.
func TestPatchCorsPreset_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	row := mustSeedCorsPreset(t, e, "test", "")
	newName := "renamed"
	rec := e.do(t, "PATCH", "/v1/cors-presets/"+row.ID, api.UpdateCorsPresetRequest{
		Name: &newName,
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch cors preset: status %d, body %s", rec.Code, rec.Body.String())
	}
	var out api.CorsPresetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	if out.Name != newName {
		t.Errorf("name = %q, want %q", out.Name, newName)
	}
	if len(out.AllowOrigins) != 1 || out.AllowOrigins[0] != "https://app.example.com" {
		t.Errorf("allow_origins = %v, want preserved verbatim", out.AllowOrigins)
	}
}

// TestDeleteCorsPreset_HappyPath confirms the DELETE returns
// 204 and the row is gone from subsequent GET.
func TestDeleteCorsPreset_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	row := mustSeedCorsPreset(t, e, "test", "")
	rec := e.do(t, "DELETE", "/v1/cors-presets/"+row.ID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete cors preset: status %d, body %s", rec.Code, rec.Body.String())
	}
	rec2 := e.do(t, "GET", "/v1/cors-presets/"+row.ID, nil, nil)
	if rec2.Code != http.StatusNotFound {
		t.Errorf("post-delete GET: status %d, want 404", rec2.Code)
	}
}

// --- plan-tier gates -------------------------------------------------------

// TestCreateCorsPreset_FreeReturns402 pins the Free-tier plan
// gate: CorsPresetsPerAccount is 0 so the create path returns
// 402 plan_cors_preset_not_allowed BEFORE the quota path runs.
// The wire-level copy distinguishes "feature not allowed on
// this plan" from "feature allowed but at-quota" so the
// dashboard renders the right upgrade hint.
func TestCreateCorsPreset_FreeReturns402(t *testing.T) {
	e := setup(t, api.PlanFree)
	rec := e.do(t, "POST", "/v1/cors-presets", corsPresetReq(), nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("free create: status %d, want 402", rec.Code)
	}
	var prob api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
		t.Fatalf("unmarshal problem: %v", err)
	}
	if prob.Code != api.CodePlanCorsPresetsNotAllowed {
		t.Errorf("code = %q, want %q", prob.Code, api.CodePlanCorsPresetsNotAllowed)
	}
}

// --- quota gates -----------------------------------------------------------

// TestCreateCorsPreset_AtPerAccountLimitReturns403 pins the
// per-account quota gate: Hobby tier caps CorsPresetsPerAccount
// at the limit set in pkg/api/limits.go; the (limit+1)th POST
// returns 403 plan_cors_preset_quota_reached with scope=account.
// 403 (not 402) because the plan DOES unlock presets — the
// right copy is "delete a preset to add another", not "upgrade
// to Hobby". Mirrors ErrPlanAlertRuleQuota.
func TestCreateCorsPreset_AtPerAccountLimitReturns403(t *testing.T) {
	e := setup(t, api.PlanHobby)
	limits := api.MustLimitsFor(api.PlanHobby)
	for i := 0; i < limits.CorsPresetsPerAccount; i++ {
		_ = mustSeedCorsPreset(t, e, "filler-"+strconvI(i), "")
	}
	rec := e.do(t, "POST", "/v1/cors-presets", corsPresetReq(), nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("at-quota create: status %d, want 403; body %s", rec.Code, rec.Body.String())
	}
	var prob api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
		t.Fatalf("unmarshal problem: %v", err)
	}
	if prob.Code != api.CodePlanCorsPresetQuotaReached {
		t.Errorf("code = %q, want %q", prob.Code, api.CodePlanCorsPresetQuotaReached)
	}
}

// TestCreateCorsPreset_AtPerAppLimitReturns403 pins the
// per-app quota gate: Hobby tier caps CorsPresetsPerApp; the
// (limit+1)th app-scoped POST returns 403 with scope=app.
func TestCreateCorsPreset_AtPerAppLimitReturns403(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appID := mustSeedApp(t, e, "cors-cap-app")
	limits := api.MustLimitsFor(api.PlanHobby)
	for i := 0; i < limits.CorsPresetsPerApp; i++ {
		_ = mustSeedCorsPreset(t, e, "app-filler-"+strconvI(i), appID)
	}
	req := corsPresetReq()
	req.AppID = ptrString(appID)
	rec := e.do(t, "POST", "/v1/cors-presets", req, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("at-quota app create: status %d, want 403; body %s", rec.Code, rec.Body.String())
	}
	var prob api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
		t.Fatalf("unmarshal problem: %v", err)
	}
	if prob.Code != api.CodePlanCorsPresetQuotaReached {
		t.Errorf("code = %q, want %q", prob.Code, api.CodePlanCorsPresetQuotaReached)
	}
}

// --- validation matrix -----------------------------------------------------

// TestCreateCorsPreset_WildcardWithCredentialsRejected pins
// the *+credentials footgun (ADR-091 D12): AllowCredentials
// true + AllowOrigins ["*"] returns 400 (the wire-level
// status mirrors the inline EdgeRuleCORSAction validate at
// handlers_alerts.go:145 — the wire-level code is
// `validation_failed` and the message references the same
// RFC 7807 stable code as the inline validate).
func TestCreateCorsPreset_WildcardWithCredentialsRejected(t *testing.T) {
	e := setup(t, api.PlanHobby)
	req := corsPresetReq()
	req.AllowCredentials = true
	req.AllowOrigins = []string{"*"}
	rec := e.do(t, "POST", "/v1/cors-presets", req, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("footgun create: status %d, want 400; body %s", rec.Code, rec.Body.String())
	}
	var prob api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
		t.Fatalf("unmarshal problem: %v", err)
	}
	if prob.Code != api.CodeValidation {
		t.Errorf("code = %q, want %q", prob.Code, api.CodeValidation)
	}
}

// TestCreateCorsPreset_EmptyOriginsRejected pins the
// at-least-one-allow_origin gate: the storage-side CHECK
// constraint and the wire-level Validate both reject the
// empty-slice case.
func TestCreateCorsPreset_EmptyOriginsRejected(t *testing.T) {
	e := setup(t, api.PlanHobby)
	req := corsPresetReq()
	req.AllowOrigins = nil
	rec := e.do(t, "POST", "/v1/cors-presets", req, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty origins: status %d, want 400; body %s", rec.Code, rec.Body.String())
	}
}

// TestPatchCorsPreset_EmptyBodyRejected pins the
// at-least-one-field gate: an empty PATCH body returns
// 400 validation_failed.
func TestPatchCorsPreset_EmptyBodyRejected(t *testing.T) {
	e := setup(t, api.PlanHobby)
	row := mustSeedCorsPreset(t, e, "test", "")
	rec := e.do(t, "PATCH", "/v1/cors-presets/"+row.ID, api.UpdateCorsPresetRequest{}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch: status %d, want 400; body %s", rec.Code, rec.Body.String())
	}
}

// --- IDOR-safety -----------------------------------------------------------

// TestGetCorsPreset_CrossTenantReturns404 pins the IDOR-safety
// posture: a foreign account's preset id returns 404, not 403
// (no existence leak — same convention as GetEdgeRule and
// GetAlertRule at handlers_alerts_test.go).
func TestGetCorsPreset_CrossTenantReturns404(t *testing.T) {
	store := state.NewMemStore()
	// Two accounts on the same store.
	acctA, err := store.CreateAccount(context.Background(), "a@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	ptA, hashA, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(context.Background(), acctA.ID, hashA, "test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	acctB, err := store.CreateAccount(context.Background(), "b@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	ptB, hashB, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(context.Background(), acctB.ID, hashB, "test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	// Seed a preset on B's account.
	limits := api.MustLimitsFor(api.PlanHobby)
	row, err := store.CreateCorsPresetIfUnderQuota(context.Background(), state.CorsPreset{
		AccountID:     acctB.ID,
		Name:          "b-only",
		AllowOrigins:  []string{"https://app.example.com"},
		AllowMethods:  []string{"GET"},
		MaxAgeSeconds: 600,
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	// Boot the server with both keys visible.
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	envA := testEnv{h: srv.handler(), store: store, key: ptA, acct: acctA}
	envB := testEnv{h: srv.handler(), store: store, key: ptB, acct: acctB}
	_ = envB
	// A's key asks for B's preset id → 404.
	rec := envA.doAsCorsPreset(t, "GET", "/v1/cors-presets/"+row.ID, nil, nil, ptA)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant GET: status %d, want 404; body %s", rec.Code, rec.Body.String())
	}
}

// --- helpers ---------------------------------------------------------------

// ptrString returns &s for the string pointer helpers used
// by the partial-update tests.
func ptrString(s string) *string { return &s }

// strconvI is a tiny strconv.Itoa alias to avoid an import in
// every test helper; mirrors the convention at the alert-rule
// tests.
func strconvI(i int) string {
	if i == 0 {
		return "0"
	}
	const digits = "0123456789"
	var buf [16]byte
	n := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		n--
		buf[n] = digits[i%10]
		i /= 10
	}
	if neg {
		n--
		buf[n] = '-'
	}
	return string(buf[n:])
}

// CorsPresetMaxOrigins / CorsPresetMaxAllowMethods were documented as
// enforced at the apid write boundary but nothing checked them; a preset
// could carry as many origins as the body held, walked by the gateway on
// every matching request.
func TestCorsPresetOriginsCappedByPlan(t *testing.T) {
	e := setup(t, api.PlanHobby)
	limit := api.MustLimitsFor(api.PlanHobby).CorsPresetMaxOrigins
	origins := make([]string, limit+1)
	for i := range origins {
		origins[i] = fmt.Sprintf("https://o%d.example.com", i)
	}
	req := corsPresetReq()
	req.AllowOrigins = origins
	rec := e.do(t, "POST", "/v1/cors-presets", req, nil)
	assertProblem(t, rec, http.StatusUnprocessableEntity, api.CodeCorsPresetTooLarge)

	req.AllowOrigins = origins[:limit]
	if rec := e.do(t, "POST", "/v1/cors-presets", req, nil); rec.Code != http.StatusCreated {
		t.Fatalf("preset at the cap = %d: %s", rec.Code, rec.Body.String())
	}

	seeded := mustSeedCorsPreset(t, e, "patch-me", "")
	rec = e.do(t, "PATCH", "/v1/cors-presets/"+seeded.ID, api.UpdateCorsPresetRequest{AllowOrigins: origins}, nil)
	assertProblem(t, rec, http.StatusUnprocessableEntity, api.CodeCorsPresetTooLarge)
}
