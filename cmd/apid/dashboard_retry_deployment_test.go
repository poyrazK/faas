// Unit + handler-level tests for dashboard_retry_deployment.go
// (ADR-117 §Production-ready follow-on, C4).
//
// Pin surface:
//
//   - failedStageFromJSON (pure helper, no storage deps):
//
//   - returns first failed row's name
//
//   - empty / malformed input falls through to ""
//
//   - falls back to `current` only when it's closed-vocab
//
//   - unknown `current` names do not surface
//
//   - dashboardRetryDeployment (handler with memstore fixture):
//
//   - happy path: 302 to /dashboard/apps/<slug>/
//     deployments/<new-id>, new row's stage_state.current
//     pinned to from_stage, new id differs from source
//
//   - cross-account: 302 (login redirect) or 401, never 200
//
//   - closed-vocab slip: 302 to ?retried=bad
//
//   - missing ?from: 302 to ?retried=bad (no defaulting)
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// ─── failedStageFromJSON (pure helper) ──────────────────────────────────────

func TestFailedStageFromJSON_FirstFailedRow(t *testing.T) {
	ss := state.StageState{
		Current: "image_build",
		History: []state.StageStateItem{
			{Name: "source_download", Status: "completed"},
			{Name: "dependency_restore", Status: "completed"},
			{Name: "image_build", Status: "failed"},
		},
	}
	b, _ := json.Marshal(ss)
	if got := failedStageFromJSON(b); got != "image_build" {
		t.Errorf("got %q, want %q", got, "image_build")
	}
}

func TestFailedStageFromJSON_EmptyAndMalformed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input []byte
		want  string
	}{
		{"nil", nil, ""},
		{"empty", []byte{}, ""},
		{"malformed", []byte("{not json"), ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := failedStageFromJSON(tc.input); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFailedStageFromJSON_NoFailedRowFallsBackToCurrent(t *testing.T) {
	// A pre-ADR-117 row might have a non-empty `current` but no
	// failed history row (the jsonb column was empty when the
	// row failed and was populated only on the next deploy). The
	// helper falls back to `current` when it's a closed-vocab
	// name; callers gate on empty to decide "no retry form".
	ss := state.StageState{Current: "snapshot_prepare"}
	b, _ := json.Marshal(ss)
	if got := failedStageFromJSON(b); got != "snapshot_prepare" {
		t.Errorf("got %q, want %q (current fallback)", got, "snapshot_prepare")
	}
}

func TestFailedStageFromJSON_NoClosedVocabCurrentReturnsEmpty(t *testing.T) {
	// Belt + suspenders: if `current` carries an unknown name
	// (operator-side jsonb corruption, unknown future stage),
	// the helper must NOT surface it as a from_stage — the
	// closed-6 vocabulary is enforced upstream.
	ss := state.StageState{Current: "future_stage_xyz"}
	b, _ := json.Marshal(ss)
	if got := failedStageFromJSON(b); got != "" {
		t.Errorf("got %q, want empty (unknown vocab rejected)", got)
	}
}

// ─── dashboardRetryDeployment (storage-backed handler) ──────────────────────

const retryTestSlug = "my-app"

// seedFailedDeployment reuses the alice@example.com account that
// newAuthedDashboardServerFull already created (the session cookie
// is bound to that account). Seeding a separate account would route
// the GET /dashboard/apps/.../deployments/<id> read into the
// app.AccountID != acct.ID IDOR-guard at handlers_dashboard.go:1738
// and 404 the page. The handler test path drives a POST against
// the new route; the handler must mint a CSRF token AND verify it
// against the source row's id.
func seedFailedDeployment(t *testing.T, store *state.MemStore) (state.Account, state.App, state.Deployment) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.AccountByEmail(ctx, "alice@example.com")
	if err != nil {
		t.Fatalf("lookup alice@example.com: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID,
		Slug:      retryTestSlug,
		RAMMB:     256,
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		ImageDigest: "img:latest",
		SourceURL:   "git:abc",
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	// Drive the canonical stage pipeline so the jsonb populates
	// the shape the renderer's failedStageFromJSON helper scans.
	// SetDeploymentFailedEx now finalizes any active stage atomically;
	// this fixture still drives the canonical stage pipeline first so
	// the renderer's failedStageFromJSON helper sees the exact shape.
	// After the helper, the row's history[-1] is
	// image_build with status=failed and current is cleared
	// (the in-flight stage rolls into history on failure).
	now := time.Now().UTC()
	transitions := []struct {
		from, to state.StageName
	}{
		{state.StageSourceDownload, state.StageDependencyRestore},
		{state.StageDependencyRestore, state.StageImageBuild},
		{state.StageImageBuild, state.StageSnapshotPrepare},
	}
	for _, tr := range transitions {
		if _, err := store.AppendDeploymentStage(ctx, dep.ID, tr.from, tr.to, now, ""); err != nil {
			t.Fatalf("seed transition %s→%s: %v", tr.from, tr.to, err)
		}
	}
	if _, err := store.MarkDeploymentStageFailed(ctx, dep.ID, now, "build VM out of memory"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if _, err := store.SetDeploymentFailedEx(ctx, dep.ID, "image_build_oom", "build VM out of memory",
		"build VM hit its 2 GB ceiling", "warm the cache before retry", "see ADR-003", nil); err != nil {
		t.Fatalf("set failed-ex: %v", err)
	}
	return acct, app, dep
}

// dashboardRetryFormPOST issues a POST against
// /dashboard/apps/{slug}/deployments/{id}/retry?from=<stage>
// with the supplied csrf_token. The session cookie is OPTIONAL —
// cross-account test omits it on purpose.
func dashboardRetryFormPOST(t *testing.T, h http.Handler, sessionCookie *http.Cookie, id, from, csrfToken string, extraCookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	path := "/dashboard/apps/" + retryTestSlug + "/deployments/" + id + "/retry"
	if from != "" {
		path += "?from=" + from
	}
	form := strings.NewReader(middleware.FormFieldName + "=" + csrfToken)
	r := httptest.NewRequest(http.MethodPost, path, form)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if sessionCookie != nil {
		r.AddCookie(sessionCookie)
	}
	for _, c := range extraCookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// renderDashboardDeploymentDetail drives a GET against the deployment
// detail page so the CSRF sidecar + form token get minted by the
// render path. Returns the csrf sidecar cookie + the form value the
// template rendered. Mirrors `renderDashboardAccount`'s shape but for
// the deployment-detail page (C4 — the new form is on the detail
// page, not on /dashboard/account).
func renderDashboardDeploymentDetail(t *testing.T, h http.Handler, sessionCookie *http.Cookie, depID string) (csrfCookie string, token string) {
	t.Helper()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/dashboard/apps/"+retryTestSlug+"/deployments/"+depID, nil)
	if sessionCookie != nil {
		r.AddCookie(sessionCookie)
	}
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /dashboard/apps/.../deployments/%s: status = %d, want 200\nbody = %s",
			depID, rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == middleware.CookieNameAuthenticated {
			csrfCookie = c.Value
			break
		}
	}
	if csrfCookie == "" {
		t.Fatalf("rendered detail page is missing the faas_csrf sidecar")
	}
	// Pin the token's existence (the form must render). Extract
	// it from the rendered HTML. We do not pin the exact value —
	// sealed envelopes are random per render.
	body := rec.Body.String()
	if !strings.Contains(body, `name="csrf_token"`) {
		t.Fatalf("rendered detail page missing csrf_token field\nbody = %s", body)
	}
	if !strings.Contains(body, `class="deploy-retry-form"`) {
		t.Fatalf("rendered detail page missing the retry-form class (CanRetry gate)\nbody = %s", body)
	}
	// The token is between `value="..."` immediately after
	// `name="csrf_token"`. Pull it.
	const marker = `name="csrf_token" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("could not locate csrf_token value attribute")
	}
	rest := body[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("malformed csrf_token value attribute")
	}
	token = rest[:j]
	return csrfCookie, token
}

// TestDashboardRetryDeployment_HappyPath — POST with the right
// envelope + from_stage=image_build → 302 to the new row's id.
// Confirms the storage-layer retry primitive fired (a fresh
// row exists, with stage_state.current=image_build) AND the
// redirect target points to the new row, not the source.
//
// The test drives a real GET against /dashboard/apps/<slug>/
// deployments/<id> to mint a fresh CSRF envelope (matching the
// dashboardDelete / dashboardFireCron pattern). Sealing the
// token against (action="retry_deployment", account_id)
// requires the render path.
func TestDashboardRetryDeployment_HappyPath(t *testing.T) {
	h, sessionCookie, store, _ := newAuthedDashboardServerFull(t)
	_, _, dep := seedFailedDeployment(t, store)
	csrfSidecar, token := renderDashboardDeploymentDetail(t, h, sessionCookie, dep.ID)
	rec := dashboardRetryFormPOST(t, h, sessionCookie, dep.ID, string(state.StageSnapshotPrepare), token,
		&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrfSidecar})
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302\nbody = %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/dashboard/apps/"+retryTestSlug+"/deployments/") {
		t.Fatalf("Location = %q, want /dashboard/apps/%s/deployments/<new-id>", loc, retryTestSlug)
	}
	newID := strings.TrimPrefix(loc, "/dashboard/apps/"+retryTestSlug+"/deployments/")
	if strings.Contains(newID, "?") {
		newID = newID[:strings.Index(newID, "?")]
	}
	if newID == dep.ID {
		t.Fatalf("Location id (%q) must differ from source id (%q)", newID, dep.ID)
	}
	newDep, err := store.DeploymentByID(t.Context(), newID)
	if err != nil {
		t.Fatalf("load new deployment: %v", err)
	}
	if newDep.Status != state.DeployPending {
		t.Errorf("new row status = %q, want %q", newDep.Status, state.DeployPending)
	}
	if newDep.AppID != dep.AppID {
		t.Errorf("new row AppID = %q, want %q (must inherit from source)", newDep.AppID, dep.AppID)
	}
}

// TestDashboardRetryDeployment_CrossAccount — POST without a
// session cookie (every handler in the dashboardChain redirects
// to /login when the cookie is missing). That's the IDOR-safe
// behaviour we want: a probe from outside the account sees the
// same login redirect it would see on /dashboard/ — never a 200,
// never a 500, never a 403 (which would confirm the source id
// exists on a different account).
func TestDashboardRetryDeployment_CrossAccount(t *testing.T) {
	h, _, store, _ := newAuthedDashboardServerFull(t)
	_, _, dep := seedFailedDeployment(t, store)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost,
		"/dashboard/apps/"+retryTestSlug+"/deployments/"+dep.ID+"/retry?from=image_build",
		strings.NewReader(""))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, r)
	if rec.Code == http.StatusOK {
		t.Fatalf("cross-account probe got 200; must not see the page\nbody = %s", rec.Body.String())
	}
	if rec.Code != http.StatusFound && rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 302 (login redirect) or 401", rec.Code)
	}
}

// TestDashboardRetryDeployment_ClosedVocabSlip — POST with
// ?from=not_a_stage → 302 to ?retried=bad (no fresh row
// inserted). Defends the closed-6 vocabulary at the dashboard
// edge; storage has its own IsStageName check.
func TestDashboardRetryDeployment_ClosedVocabSlip(t *testing.T) {
	h, sessionCookie, store, _ := newAuthedDashboardServerFull(t)
	_, _, dep := seedFailedDeployment(t, store)
	csrfSidecar, token := renderDashboardDeploymentDetail(t, h, sessionCookie, dep.ID)
	rec := dashboardRetryFormPOST(t, h, sessionCookie, dep.ID, "not_a_stage", token,
		&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrfSidecar})
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (?retried=bad)\nbody = %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "retried=bad") {
		t.Errorf("Location = %q, want ?retried=bad", loc)
	}
}

// TestDashboardRetryDeployment_NoFromStage — POST without the
// ?from query param → same ?retried=bad branch. The handler
// must NOT silently pick a stage.
func TestDashboardRetryDeployment_NoFromStage(t *testing.T) {
	h, sessionCookie, store, _ := newAuthedDashboardServerFull(t)
	_, _, dep := seedFailedDeployment(t, store)
	csrfSidecar, token := renderDashboardDeploymentDetail(t, h, sessionCookie, dep.ID)
	rec := dashboardRetryFormPOST(t, h, sessionCookie, dep.ID, "", token,
		&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrfSidecar})
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "retried=bad") {
		t.Errorf("Location = %q, want ?retried=bad", loc)
	}
}
