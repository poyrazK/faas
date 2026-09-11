// handlers_diff_test.go — pins the loadApp / AppBySlug split
// in cmd/apid/handlers_diff.go.
//
// Why this file exists:
//
// Code-review finding #1 on PR #869: the diff handler originally
// used s.loadApp for the slug → app lookup, which writes a 404
// Problem and returns (false) on missing-app. The docstring at
// the top of handlers_diff.go advertises the "what if" semantics
// — fresh-app diffs must return 200 with a would-create-app
// Change so CI consumers can preview a deploy that doesn't yet
// exist on the platform. The two failure modes (missing app
// vs cross-account slug) must be distinguished: the first is a
// preview path, the second is IDOR protection and must stay 404.
//
// The handler now reads the store directly with AppBySlug;
// loadApp stays out of the diff path. These tests pin the
// three-bucket split so a future refactor that re-introduces
// loadApp or weakens the AccountID check fires a red gate
// instead of a 3am pager.
//
// Coverage matrix:
//
//	                          status  shape
//	slug does not exist      200     empty Diff with would-create-app,
//	                                 blocking usually false (no
//	                                 quota breach on a fresh app)
//	slug exists on a         404     Problem{CodeNotFound}
//	  different account
//	slug exists on this      200     Diff with current config
//	  account                       (smoke — the engine ran)

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
	"github.com/onebox-faas/faas/pkg/wire"
)

// newDiffTestEnv is the test seam for the POST /v1/apps/{slug}/diff
// handler. Mirrors newTestServerWithCapturingNotifier's shape
// (handlers_app_changed_test.go:105) but without the Notifier
// — the diff handler is read-only, so no DB writes happen and no
// ngAppChanged payload needs to be captured.
func newDiffTestEnv(t *testing.T, plan api.Plan) testEnv {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), fmt.Sprintf("%s@example.com", plan), plan)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "diff-test", api.ScopesReadSurface); err != nil {
		t.Fatal(err)
	}
	ops := wire.NewOpsMetrics("apid_diff_test")
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", nil).WithOpsMetrics(context.Background(), ops)
	return testEnv{h: srv.handler(), s: srv, store: store, key: pt, acct: acct, ops: ops}
}

func postDiffReq(t *testing.T, e testEnv, slug string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/apps/"+slug+"/diff", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestDiffPendingFromRequest_PreservesAppProtocol(t *testing.T) {
	protocol := "grpc"
	req := api.DiffRequest{AppConfig: &api.DiffAppConfigPatch{AppProtocol: &protocol}}
	pending := diffPendingFromRequest(&req)
	if pending.AppConfig.AppProtocol == nil || *pending.AppConfig.AppProtocol != protocol {
		t.Fatalf("app_protocol = %v, want %q", pending.AppConfig.AppProtocol, protocol)
	}
}

// TestDiffApp_MissingSlug_Returns200WithPreview is the regression
// test for code-review finding #1. Pre-fix: loadApp wrote a 404
// and the CI consumer lost the diff entirely. Post-fix: the
// handler reads AppBySlug and hits the state.ErrNotFound branch,
// which seeds a zero-value app and lets the engine emit a
// would-create-app Change. The customer sees a 200 + DiffResponse.
func TestDiffApp_MissingSlug_Returns200WithPreview(t *testing.T) {
	e := newDiffTestEnv(t, api.PlanHobby)

	rec := postDiffReq(t, e, "never-existed", []byte(`{"app_config":{"ram_mb":256}}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("missing slug status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.DiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not a DiffResponse: %v\nbody=%s", err, rec.Body.String())
	}
	// The Preview shape is documented as blocking=true if the would-be
	// fresh app breaches plan quotas, blocking=false otherwise. A
	// Hobby app with 256MB RAM is well under the cap, so blocking
	// must be false.
	if resp.Blocking {
		t.Errorf("blocking = true, want false for a valid Hobby preview; breaks=%+v", resp.Diff.Breaks)
	}
	// And the preview itself must be a valid Diff (slug echoed, plan
	// resolved, no nil deref).
	if resp.Diff.Slug != "never-existed" {
		t.Errorf("diff.slug = %q, want %q", resp.Diff.Slug, "never-existed")
	}
	if resp.Plan != "hobby" {
		t.Errorf("plan = %q, want %q", resp.Plan, "hobby")
	}
}

func TestDiffApp_FreshSourcePreviewIncludesResolvedIdentity(t *testing.T) {
	e := newDiffTestEnv(t, api.PlanHobby)
	rec := postDiffReq(t, e, "fresh-function", []byte(`{
		"build_plan": {
			"framework": "python",
			"runtime": "python312",
			"handler": "handler.handler",
			"class": "function",
			"source_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		}
	}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("fresh source preview status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.DiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not a DiffResponse: %v\nbody=%s", err, rec.Body.String())
	}
	if resp.Blocking {
		t.Fatalf("fresh source preview unexpectedly blocking: %+v", resp.Diff.Breaks)
	}
	fields := map[string]api.DiffChange{}
	for _, change := range resp.Diff.Changes {
		fields[change.Field] = change
	}
	if fields["app"].Kind != "add" || !bytes.Contains(fields["app"].After, []byte(`"class":"function"`)) {
		t.Fatalf("app change = %+v, want function add", fields["app"])
	}
	if fields["deployment"].Kind != "add" || !bytes.Contains(fields["deployment"].After, []byte(`source_sha256`)) {
		t.Fatalf("deployment change = %+v, want source identity add", fields["deployment"])
	}
}

func TestDiffApp_FreshImagePreviewIncludesDeploymentIdentity(t *testing.T) {
	e := newDiffTestEnv(t, api.PlanHobby)
	rec := postDiffReq(t, e, "fresh-image", []byte(`{
		"build_plan": {"framework":"unknown", "class":"app"},
		"image": "registry.example.com/hello:v1"
	}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("fresh image preview status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.DiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not a DiffResponse: %v\nbody=%s", err, rec.Body.String())
	}
	var deployment api.DiffChange
	for _, change := range resp.Diff.Changes {
		if change.Field == "deployment" {
			deployment = change
		}
	}
	if deployment.Kind != "add" || !bytes.Contains(deployment.After, []byte(`"image":"registry.example.com/hello:v1"`)) {
		t.Fatalf("deployment change = %+v, want image identity add", deployment)
	}
}

// TestDiffApp_CrossAccountSlug_Returns404 pins the IDOR boundary.
// The diff endpoint must not leak the "missing app" preview path
// to a slug that exists on a different account — that's the
// attack surface loadApp was guarding against. We pin the 404
// status so a future refactor that drops the AccountID check
// fails this test instead of leaking cross-account rows.
func TestDiffApp_CrossAccountSlug_Returns404(t *testing.T) {
	e := newDiffTestEnv(t, api.PlanHobby)

	// Insert an app under a different account. MemStore.CreateApp
	// takes a state.App directly (handlers_app_changed_test.go:436
	// uses the same pattern).
	otherAcct, err := e.store.CreateAccount(context.Background(), "other@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: otherAcct.ID,
		Slug:      "their-app",
		Type:      state.AppTypeApp,
		RAMMB:     256,
	}); err != nil {
		t.Fatal(err)
	}

	// Now ask for a diff against their-app using our key.
	rec := postDiffReq(t, e, "their-app", []byte(`{}`))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account slug status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	// The 404 must surface as RFC 7807 with the standard code, not
	// as a 200 + would-create-app Change (which would tell the
	// attacker the slug exists on someone else's account).
	body := bytes.TrimSpace(rec.Body.Bytes())
	if !bytes.Contains(body, []byte(`"code":"not_found"`)) {
		t.Errorf("404 body code = %q, want not_found\nbody=%s", body, body)
	}
}

// TestDiffApp_ExistingSlug_Returns200WithCurrentConfig is the
// happy-path smoke. Round-trip through the engine, assert the
// current config is echoed back as a no-change Diff.
func TestDiffApp_ExistingSlug_Returns200WithCurrentConfig(t *testing.T) {
	e := newDiffTestEnv(t, api.PlanHobby)

	// Create the app under our account.
	if _, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID,
		Slug:      "my-app",
		Type:      state.AppTypeApp,
		RAMMB:     256,
	}); err != nil {
		t.Fatal(err)
	}

	// Empty body — propose no changes. The handler should still
	// return 200 with a Diff echoing the current config (no
	// Changes, no Breaks).
	rec := postDiffReq(t, e, "my-app", []byte(`{}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.DiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not a DiffResponse: %v\nbody=%s", err, rec.Body.String())
	}
	if resp.Blocking {
		t.Errorf("blocking = true, want false on a no-op diff; breaks=%+v", resp.Diff.Breaks)
	}
	if resp.Diff.Slug != "my-app" {
		t.Errorf("diff.slug = %q, want %q", resp.Diff.Slug, "my-app")
	}
}
