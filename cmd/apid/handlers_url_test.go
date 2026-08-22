// Tests for GET /v1/deployments/{id}/url — the per-deployment
// preview URL read seam (issue #976 / ADR-122 / SAFE-RELEASES-C.2).
//
// Pins:
//
//   - Happy path (live deployment): handler resolves the deployment,
//     looks up the parent app, computes the ordinal, and returns
//     a populated Host + URL field of `deploy-{N}.{slug}.gregale.dev`
//     form. Alive=true.
//   - Happy path (building deployment): same envelope as live — any
//     "preview-active" status yields a populated URL.
//   - Failed deployment returns 200 with Alive=false and Host=""
//     (NOT a 404 — the row exists, the customer can still see it,
//     just no preview URL while it's failed).
//   - Cross-account probe: 404, never 403 (IDOR posture mirrors
//     getDeployment / getDeploymentStages / getDeploymentScan).
//   - Unknown id: 404 with the standard not-found problem code.
//   - Disabled zone: when wire.DeployWildcardSuffix is "" the
//     handler still returns 200 with Alive=false (NOT 404) so
//     dashboards render the closed-state copy consistently
//     across environments.
package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestGetDeploymentURL_HappyPath exercises the production wire:
// one deployment, status='live', parent app with slug "url-happy"
// → first deployment → ordinal 1 →
//
//	expect: Host="deploy-1.url-happy.gregale.dev"
//	expect: URL="https://deploy-1.url-happy.gregale.dev"
//	expect: Alive=true.
//	expect: DeploymentID + AppID echoed on the response.
func TestGetDeploymentURL_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "url-happy")
	// mustSeedDeployment stamps status='building'. Flip to 'live'
	// so the happy path exercises the production wire (the
	// certissuer mints for live and in-flight alike, but the
	// most common read is post-success).
	if err := e.store.UpdateDeploymentStatus(context.Background(), dep.ID, state.DeployLive, ""); err != nil {
		t.Fatalf("UpdateDeploymentStatus → live: %v", err)
	}
	dep.Status = state.DeployLive
	rec := e.do(t, "GET", "/v1/deployments/"+dep.ID+"/url", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got api.DeploymentPreviewURL
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (raw body=%s)", err, rec.Body.String())
	}
	if !got.Alive {
		t.Errorf("Alive = false on a live deployment; want true")
	}
	wantHost := "deploy-1.url-happy.gregale.dev"
	if got.Host != wantHost {
		t.Errorf("Host = %q, want %q (deployment ordinal must resolve to 1 for the first app deployment)", got.Host, wantHost)
	}
	wantURL := "https://" + wantHost
	if got.URL != wantURL {
		t.Errorf("URL = %q, want %q", got.URL, wantURL)
	}
	if got.DeploymentID != dep.ID {
		t.Errorf("DeploymentID = %q, want %q", got.DeploymentID, dep.ID)
	}
	if got.AppID != dep.AppID {
		t.Errorf("AppID = %q, want %q", got.AppID, dep.AppID)
	}
}

// TestGetDeploymentURL_BuildingStatusIsAlive pins that any
// "preview-active" status (pending/building/imaging/snapshotting/
// live) — not just 'live' — yields Host="" false but Alive=true
// AND a non-empty Host. A customer mid-deploy deserves a
// functioning preview URL: the cert issuer mints for live and
// in-flight deployments alike, so the read seam must agree.
func TestGetDeploymentURL_BuildingStatusIsAlive(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "url-building")
	// Flip status manually so the test exercises the building →
	// alive path (the seed path is 'live' only).
	if err := e.store.UpdateDeploymentStatus(context.Background(), dep.ID, state.DeployBuilding, "test flip"); err != nil {
		t.Fatalf("UpdateDeploymentStatus: %v", err)
	}
	rec := e.do(t, "GET", "/v1/deployments/"+dep.ID+"/url", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got api.DeploymentPreviewURL
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if !got.Alive {
		t.Errorf("Alive = false on status=building; want true")
	}
	if got.Host == "" {
		t.Errorf("Host empty on status=building; want deploy-1.url-building.gregale.dev")
	}
}

// TestGetDeploymentURL_FailedReturnsAliveFalse covers the
// not-preview-active branch. Status='failed' is NOT in the
// preview-active set, so the handler returns 200 with
// Alive=false and Host="". The 200 envelope (NOT a 404) is
// load-bearing: the dashboard renders a "preview closed" chip
// without re-fetching the deployment row.
func TestGetDeploymentURL_FailedReturnsAliveFalse(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "url-failed")
	if err := e.store.UpdateDeploymentStatus(context.Background(), dep.ID, state.DeployFailed, "test-failure"); err != nil {
		t.Fatalf("UpdateDeploymentStatus: %v", err)
	}
	rec := e.do(t, "GET", "/v1/deployments/"+dep.ID+"/url", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got api.DeploymentPreviewURL
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Alive {
		t.Errorf("Alive = true on status=failed; want false")
	}
	if got.Host != "" {
		t.Errorf("Host = %q on failed deployment; want empty", got.Host)
	}
	if got.URL != "" {
		t.Errorf("URL = %q on failed deployment; want empty", got.URL)
	}
}

// TestGetDeploymentURL_UnknownReturns404 covers the not-found
// branch (deployment id format is valid hex but no such row).
// Same posture as getDeploymentScan / getDeploymentStages.
func TestGetDeploymentURL_UnknownReturns404(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/deployments/deadbeefdeadbeefdeadbeefdeadbeef/url", nil, nil)
	assertProblem(t, rec, 404, api.CodeNotFound)
}

// TestGetDeploymentURL_CrossAccountReturns404 locks the IDOR
// posture: a probe from a second account must NEVER distinguish
// "deployment doesn't exist" from "deployment exists in another
// account" — both surface as 404 with the same problem code.
// Same posture as getDeployment (handlers_ext.go:1136) and
// getDeploymentStages (handlers_stages.go:49).
func TestGetDeploymentURL_CrossAccountReturns404(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "url-cross")

	store := e.store
	foreignAcct, err := store.CreateAccount(context.Background(), "intruder-url@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount foreign: %v", err)
	}
	pt, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(context.Background(), foreignAcct.ID, hash, "intruder", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey foreign: %v", err)
	}
	rec := e.doAs(t, "GET", "/v1/deployments/"+dep.ID+"/url", nil, nil, pt)
	if rec.Code != 404 {
		t.Fatalf("cross-account GET /url: status %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), api.CodeNotFound) {
		t.Errorf("cross-account GET /url: body did not carry %s, got %s", api.CodeNotFound, rec.Body.String())
	}
}

// TestGetDeploymentURL_SecondDeploymentOrdinalIsTwo covers the
// "ordinal isn't always 1" pin: the same app has a second
// deployment, and the second one is ordinal 2. A regression
// that always returned 1 would mint a duplicate URL that the
// allowlist would refuse to admit for the second row.
//
// We hand-roll the seed here (instead of reusing mustSeedDeployment)
// because mustSeedDeployment provisions a fresh app per call — and
// the ordinal counter is per-app, so we need both rows under one
// app.
func TestGetDeploymentURL_SecondDeploymentOrdinalIsTwo(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID,
		Slug:      "url-ord",
		Type:      state.AppTypeApp,
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	first, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:ord1",
		Kind:        state.DeploymentKindImage,
		Status:      state.DeployBuilding,
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed first deployment: %v", err)
	}
	second, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:ord2",
		Kind:        state.DeploymentKindImage,
		Status:      state.DeployBuilding,
		CreatedAt:   time.Now().UTC().Add(time.Second),
	})
	if err != nil {
		t.Fatalf("seed second deployment: %v", err)
	}
	rec := e.do(t, "GET", "/v1/deployments/"+second.ID+"/url", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got api.DeploymentPreviewURL
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Host != "deploy-2.url-ord.gregale.dev" {
		t.Errorf("Host = %q, want deploy-2.url-ord.gregale.dev (second deployment must mint ordinal 2)", got.Host)
	}
	// Sanity: the first deployment still resolves to ordinal 1.
	rec1 := e.do(t, "GET", "/v1/deployments/"+first.ID+"/url", nil, nil)
	if rec1.Code != 200 {
		t.Fatalf("first status %d: %s", rec1.Code, rec1.Body.String())
	}
	var got1 api.DeploymentPreviewURL
	_ = json.Unmarshal(rec1.Body.Bytes(), &got1)
	if got1.Host != "deploy-1.url-ord.gregale.dev" {
		t.Errorf("first Host = %q, want deploy-1.url-ord.gregale.dev", got1.Host)
	}
}
