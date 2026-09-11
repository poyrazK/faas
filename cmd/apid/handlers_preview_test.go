package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedPreviewAppForTest mirrors preview_janitor_test.go's
// seedPreviewApp but inlines the bare minimum so this test file
// stays self-contained (no need to share a helper across the
// destroy-prep surface). Returns the freshly-created app row so
// the caller can assert on the post-destroy state.
func seedPreviewAppForTest(t *testing.T, e testEnv, slug, parentSlug string, prNum int) state.App {
	t.Helper()
	expires := time.Now().Add(7 * 24 * time.Hour)
	app := state.App{
		AccountID:        e.acct.ID,
		Slug:             slug,
		Type:             "stateless",
		RAMMB:            256,
		MaxConcurrency:   1,
		IdleTimeoutS:     30,
		Status:           state.AppActive,
		PreviewOfSlug:    parentSlug,
		PreviewPrNumber:  prNum,
		PreviewPrState:   state.PreviewPrStateOpen,
		PreviewExpiresAt: &expires,
	}
	created, err := e.store.CreateAppIfUnderQuota(context.Background(), app, api.Limits{DeployedApps: 10000})
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota(%q): %v", slug, err)
	}
	return created
}

// TestDestroyPreview_HappyPath confirms the destroy endpoint
// soft-deletes the preview row + returns 204. Pins:
//
//   - preview_pr_state advances to 'torn_down' so the janitor
//     doesn't re-process the row on a subsequent tick.
//   - apps.status flips to 'deleted' (SoftDeleteAppCascade).
//   - Subsequent GET returns 404 (the row is genuinely gone
//     from the customer-facing list, not just flagged).
func TestDestroyPreview_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	seeded := seedPreviewAppForTest(t, e, "pr-42-acme", "acme", 42)

	rec := e.do(t, "POST", "/v1/preview/pr-42-acme/destroy", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204: %s", rec.Code, rec.Body.String())
	}

	// Post-destroy assertions: the row exists in the store but
	// is in tombstone state (status='deleted', preview_pr_state='torn_down').
	got, err := e.store.AppByID(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("AppByID post-destroy: %v (row should still exist — soft-delete, not hard-delete)", err)
	}
	if got.Status != state.AppDeleted {
		t.Errorf("post-destroy status = %q, want %q", got.Status, state.AppDeleted)
	}
	if got.PreviewPrState != state.PreviewPrStateTornDown {
		t.Errorf("post-destroy preview_pr_state = %q, want %q", got.PreviewPrState, state.PreviewPrStateTornDown)
	}

	// The customer-facing list (PreviewAppsByParent) filters
	// out the deleted row — same shape as the per-parent pane.
	previews, err := e.store.PreviewAppsByParent(context.Background(), e.acct.ID, "acme")
	if err != nil {
		t.Fatalf("PreviewAppsByParent: %v", err)
	}
	if len(previews) != 0 {
		t.Errorf("PreviewAppsByParent returned %d rows post-destroy; want 0 (the soft-deleted preview must not appear in the customer-facing list)", len(previews))
	}
}

// spec: preview teardown is a terminal cancellation barrier for its pipeline.
func TestDestroyPreview_CancelsDeploymentAndRejectsLatePromotion(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedPreviewAppForTest(t, e, "pr-43-acme", "acme", 43)
	dep, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindTarball, ImageDigest: "sha256:preview-cancel",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateDeploymentStatus(context.Background(), dep.ID, state.DeployBuilding, ""); err != nil {
		t.Fatal(err)
	}
	build, err := e.store.CreateBuild(context.Background(), dep.ID, state.DeploymentKindTarball, 128, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.ClaimQueuedBuild(context.Background(), build.ID); err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, "POST", "/v1/preview/pr-43-acme/destroy", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204: %s", rec.Code, rec.Body.String())
	}
	gotDep, _ := e.store.DeploymentByID(context.Background(), dep.ID)
	if gotDep.Status != state.DeployCancelled {
		t.Fatalf("deployment status = %q, want cancelled", gotDep.Status)
	}
	gotBuild, _ := e.store.BuildByID(context.Background(), build.ID)
	if gotBuild.Status != state.BuildCancelled {
		t.Fatalf("build status = %q, want cancelled", gotBuild.Status)
	}
	if err := e.store.UpdateDeploymentStatus(context.Background(), dep.ID, state.DeploySnapshotting, ""); !errors.Is(err, state.ErrInvalidStateTransition) {
		t.Fatalf("late snapshot transition = %v, want ErrInvalidStateTransition", err)
	}
	if err := e.store.MarkDeploymentLive(context.Background(), dep.ID); !errors.Is(err, state.ErrInvalidStateTransition) {
		t.Fatalf("late live promotion = %v, want ErrInvalidStateTransition", err)
	}
	gotDep, _ = e.store.DeploymentByID(context.Background(), dep.ID)
	if gotDep.Status != state.DeployCancelled {
		t.Fatalf("late writers changed deployment status to %q", gotDep.Status)
	}
}

// TestDestroyPreview_ProductionAppReturns404 confirms the
// preview-only gate: a production app slug POSTed to the
// preview-destroy endpoint returns 404, NOT 204. Defends
// against the easy footgun where a customer reaches the URL
// from a stale link and accidentally destroys their prod app.
func TestDestroyPreview_ProductionAppReturns404(t *testing.T) {
	e := setup(t, api.PlanPro)
	// Seed a PRODUCTION app (PreviewOfSlug = "").
	mustSeedApp(t, e, "prod-app")

	rec := e.do(t, "POST", "/v1/preview/prod-app/destroy", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 (production app on preview-only route): %s", rec.Code, rec.Body.String())
	}
	// Confirm the production row is intact post-call.
	app, err := e.store.AppBySlug(context.Background(), "prod-app")
	if err != nil {
		t.Fatalf("AppBySlug post-call: %v (production app must not be touched by the preview-only route)", err)
	}
	if app.Status != state.AppActive {
		t.Errorf("production app status = %q after a /preview/.../destroy call; want %q (the preview-only gate must short-circuit before any write)", app.Status, state.AppActive)
	}
}

// TestDestroyPreview_UnknownSlugReturns404 confirms the
// not-found branch for a slug that doesn't exist at all.
// Returns 404 with the canonical CodeNotFound so the wire shape
// is indistinguishable from a row that exists but is a
// production app (the gate above).
func TestDestroyPreview_UnknownSlugReturns404(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/preview/nope-no-such-preview/destroy", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// TestDestroyPreview_CrossAccountReturns404 confirms the
// loadApp auth gate: a preview seeded under account A is
// invisible to account B. Returns 404 (not 403) so the wire
// shape is indistinguishable from "row not found" — no
// information leak about whether the slug exists in another
// account.
//
// Note: setup() mints a Pro account by default; this test
// creates a second account on the same store and asserts the
// cross-account POST is refused.
func TestDestroyPreview_CrossAccountReturns404(t *testing.T) {
	e := setup(t, api.PlanPro)
	// Seed a second account in the same store, then a preview
	// under it. The default setup() account cannot destroy
	// this preview because loadApp filters by account_id.
	otherAcct, err := e.store.CreateAccount(context.Background(), "other-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount(other): %v", err)
	}
	otherKey, otherHash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(context.Background(), otherAcct.ID, otherHash, "test", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey(other): %v", err)
	}
	expires := time.Now().Add(7 * 24 * time.Hour)
	_, err = e.store.CreateAppIfUnderQuota(context.Background(), state.App{
		AccountID:        otherAcct.ID,
		Slug:             "pr-9-other",
		Type:             "stateless",
		RAMMB:            256,
		MaxConcurrency:   1,
		IdleTimeoutS:     30,
		Status:           state.AppActive,
		PreviewOfSlug:    "their-app",
		PreviewPrNumber:  9,
		PreviewPrState:   state.PreviewPrStateOpen,
		PreviewExpiresAt: &expires,
	}, api.Limits{DeployedApps: 10000})
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota(other preview): %v", err)
	}

	// Drive the call from the DEFAULT account (e), NOT the
	// other account — that's the cross-account attempt.
	rec := e.do(t, "POST", "/v1/preview/pr-9-other/destroy", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 (cross-account preview destroy must refuse): %s", rec.Code, rec.Body.String())
	}
	// The other-account's preview is intact.
	otherApp, err := e.store.AppBySlug(context.Background(), "pr-9-other")
	if err != nil {
		t.Fatalf("AppBySlug(other preview): %v (other-account's preview must not be touched)", err)
	}
	if otherApp.Status != state.AppActive {
		t.Errorf("cross-account preview status = %q; want %q", otherApp.Status, state.AppActive)
	}

	// Sanity: the OTHER account can destroy its own preview
	// successfully. This proves the cross-account refusal
	// above was driven by the auth gate, not a hidden bug in
	// the destroy chain. We drive e.h directly because e.do
	// always uses e.key (the default account's key); the
	// other-account's destroy needs its own Authorization
	// header.
	req := httptest.NewRequest("POST", "/v1/preview/pr-9-other/destroy", nil)
	req.Header.Set("Authorization", "Bearer "+otherKey)
	rec2 := httptest.NewRecorder()
	e.h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("own-account destroy status %d, want 204: %s", rec2.Code, rec2.Body.String())
	}
}

// TestDestroyPreview_ResponseShape asserts the success path
// returns 204 with no content (the canonical wire shape for
// "no body, action succeeded"). The destroy handler must
// never echo the row back on success — the customer's UI
// already knows the slug from the URL.
func TestDestroyPreview_ResponseShape(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedPreviewAppForTest(t, e, "pr-1-foo", "foo", 1)

	rec := e.do(t, "POST", "/v1/preview/pr-1-foo/destroy", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty (204 No Content has no body by RFC 9110 §15.3.5.1)", rec.Body.String())
	}
}

// TestDestroyPreview_ProblemShape pins the RFC 7807 wire
// shape on the 404 not-found branch. The customer-facing
// SDKs decode `code` + `title` to render localised error
// messages, so a stable wire shape matters.
func TestDestroyPreview_ProblemShape(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/preview/no-such-preview/destroy", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
	var p map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("unmarshal problem: %v", err)
	}
	for _, want := range []string{"code", "title", "detail"} {
		if _, ok := p[want]; !ok {
			t.Errorf("problem missing %q field (RFC 7807 wire shape): %+v", want, p)
		}
	}
	if got := p["code"]; !strings.Contains(got.(string), "not_found") {
		t.Errorf("problem code = %q, want contains \"not_found\"", got)
	}
}
