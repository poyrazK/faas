package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/devloop"
	"github.com/onebox-faas/faas/pkg/state"
)

const devFindingsWorkspace = "0123456789abcdef0123456789abcdef"

func createDevSessionForTest(t *testing.T, e apidserver.TestEnv, project string) api.DevSessionResponse {
	t.Helper()
	rec := e.Do(t, "PUT", "/v1/dev/sessions/"+project, api.UpsertDevSessionRequest{WorkspaceID: devFindingsWorkspace}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	return session
}

// A renewal of an environment the janitor or --stop already claimed is a
// state the developer can act on, not "briefly at capacity".
func TestDevSessionRenewalDuringTeardownIsConflict(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	session := createDevSessionForTest(t, e, "teardown-api")
	row, err := (*e.Store()).AppBySlug(t.Context(), session.App.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).SetPreviewPrState(t.Context(), row.ID, state.PreviewPrStateTearingDown); err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, "PUT", "/v1/dev/sessions/teardown-api", api.UpsertDevSessionRequest{WorkspaceID: devFindingsWorkspace}, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), api.CodeDevSessionTearingDown) {
		t.Fatalf("renewal during teardown = %d %s, want 409 %s", rec.Code, rec.Body.String(), api.CodeDevSessionTearingDown)
	}
}

func TestDevSessionReportsLivePatchDelivery(t *testing.T) {
	t.Setenv("FAAS_DEV_PATCH_DELIVERY", "")
	e := apidserver.Setup(t, api.PlanHobby)
	session := createDevSessionForTest(t, e, "patches-api")
	if session.LivePatches == nil || *session.LivePatches {
		t.Fatalf("live_patches = %v, want false while delivery is off", session.LivePatches)
	}
}

// The platform decides within_slo from the reported time, so an old or
// modified CLI cannot move the fleet edit-to-live SLO.
func TestRecordDevSyncDerivesSLOVerdict(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	session := createDevSessionForTest(t, e, "slo-api")
	app, err := (*e.Store()).AppBySlug(t.Context(), session.App.Slug)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:slo"})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, "POST", "/v1/dev/sessions/slo-api/syncs", api.RecordDevSyncRequest{
		WorkspaceID: devFindingsWorkspace, DeploymentID: dep.ID, Status: "live",
		EditToLiveMS: 170000, SLOTargetMS: 3600000, WithinSLO: true,
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("record status = %d: %s", rec.Code, rec.Body.String())
	}
	var item api.DevSyncHistoryItem
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.WithinSLO || item.SLOTargetMS != api.DevEditToLiveTarget.Milliseconds() {
		t.Fatalf("recorded within_slo=%v target=%d; want false and %d", item.WithinSLO, item.SLOTargetMS, api.DevEditToLiveTarget.Milliseconds())
	}
}

func TestDevSessionListAndStopByApp(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	session := createDevSessionForTest(t, e, "moved-api")

	rec := e.Do(t, "GET", "/v1/dev/sessions", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", rec.Code, rec.Body.String())
	}
	var list api.DevSessionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].App != session.App.Slug || list.Sessions[0].Project != "moved-api" {
		t.Fatalf("list = %+v", list.Sessions)
	}

	// Another project's name cannot reach this environment.
	if rec := e.Do(t, "DELETE", "/v1/dev/sessions/other-api?app="+session.App.Slug, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("stop under another project = %d, want 404", rec.Code)
	}
	if rec := e.Do(t, "DELETE", "/v1/dev/sessions/moved-api?app="+session.App.Slug, nil, nil); rec.Code/100 != 2 {
		t.Fatalf("stop by app = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWatchUnsupportedAdviceFollowsPlan(t *testing.T) {
	open := state.App{Type: state.AppTypeApp, RAMMB: 1024}
	free := devloop.WatchUnsupported(open, api.PlanFree, &api.DevWatch{Command: "npm run dev"})
	if free == nil || strings.Contains(free.Detail, "require authentication on this developer environment") {
		t.Fatalf("Free advice = %+v; must not ask for authentication the plan lacks", free)
	}
	pro := devloop.WatchUnsupported(open, api.PlanPro, &api.DevWatch{Command: "npm run dev"})
	if pro == nil || !strings.Contains(pro.Detail, "require authentication") {
		t.Fatalf("Pro advice = %+v", pro)
	}
}
