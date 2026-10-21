package apidserver

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestDeploymentCronStagingRoutes(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "staging"}, nil)
	path := "/v1/apps/staging/deployment-cron-staging"
	reply := e.do(t, "GET", path, nil, nil)
	if reply.Code != 200 {
		t.Fatalf("snapshot=%d %s", reply.Code, reply.Body)
	}
	var snapshot api.DeploymentCronStagingSnapshot
	if err := json.Unmarshal(reply.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	request := api.DeploymentCronStagingRequest{SessionID: strings.Repeat("a", 32), Operation: "stage", ExpectedAppID: snapshot.AppID, ExpectedRevision: snapshot.Revision, ExpectedDeploymentID: snapshot.LatestDeploymentID, Before: snapshot.Crons, Desired: []api.DeploymentCronConfig{{ID: uuid.NewString(), Schedule: "0 * * * *", Path: "/tick", Enabled: true, Timezone: "UTC"}}}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "POST"} {
		body, _ := json.Marshal(request)
		r := httptest.NewRequest(method, path, strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		want := 200
		if method == "POST" {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("scope %s=%d %s", method, w.Code, w.Body)
		}
	}
	for _, operation := range []string{"stage", "stage", "restore", "restore"} {
		request.Operation = operation
		reply = e.do(t, "POST", path, request, nil)
		if reply.Code != 200 {
			t.Fatalf("%s=%d %s", operation, reply.Code, reply.Body)
		}
	}
	deployments, err := e.store.ListDeploymentsForApp(t.Context(), snapshot.AppID, 10, 0)
	if err != nil || len(deployments) != 0 {
		t.Fatalf("created deployments=%v err=%v", deployments, err)
	}
}

func TestDeploymentCronPrepareTracksOpaqueUpload(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "deferred"}, nil)
	path := "/v1/apps/deferred/deployment-cron-staging"
	snapshotReply := e.do(t, "GET", path, nil, nil)
	var snapshot api.DeploymentCronStagingSnapshot
	if err := json.Unmarshal(snapshotReply.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	request := api.DeploymentCronStagingRequest{SessionID: strings.Repeat("c", 32), Operation: "prepare", ExpectedAppID: snapshot.AppID, ExpectedRevision: snapshot.Revision, ExpectedDeploymentID: snapshot.LatestDeploymentID, Before: snapshot.Crons, Desired: []api.DeploymentCronConfig{{ID: uuid.NewString(), Schedule: "0 * * * *", Path: "/tick", Enabled: true, Timezone: "UTC"}}}
	if reply := e.do(t, "POST", path, request, nil); reply.Code != 200 {
		t.Fatalf("prepare=%d %s", reply.Code, reply.Body)
	}
	app, err := e.store.AppByID(t.Context(), snapshot.AppID)
	if err != nil {
		t.Fatal(err)
	}
	manifest := &gregalemanifest.Manifest{Triggers: []gregalemanifest.Trigger{{Kind: gregalemanifest.TriggerKindCron, App: app.Slug, Schedule: "0 * * * *", Path: "/tick"}}}
	if _, problem := e.s.applySourceRefManifest(state.ContextWithDeploymentSubmission(t.Context(), request.SessionID), e.acct, app, manifest, state.DefaultEnvScope, true); problem != nil {
		t.Fatalf("source manifest=%+v", problem)
	}
	if crons, err := e.store.ListCronsForApp(t.Context(), app.ID); err != nil || len(crons) != 0 {
		t.Fatalf("source ingestion activated crons early: %v err=%v", crons, err)
	}
	uploadID := randomToken(12)
	if _, err = e.store.CreateUploadSession(t.Context(), sqlc.CreateUploadSessionParams{ID: uploadID, AccountID: pgtypeFromUUIDString(e.acct.ID), AppSlug: app.Slug, TotalSize: 1024, ChunkSize: 512, PartPath: "/tmp/deferred-opaque-upload.part", DeployOptions: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	request.Operation = "track_upload"
	request.UploadID = uploadID
	if reply := e.do(t, "POST", path, request, nil); reply.Code != 200 {
		t.Fatalf("track=%d %s", reply.Code, reply.Body)
	}
	dep, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.MarkUploadSessionCommitted(t.Context(), sqlc.MarkUploadSessionCommittedParams{ID: uploadID, DeploymentID: pgtype.Text{String: dep.ID, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if err = e.store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	if changes, err := e.store.ReconcileDeploymentCrons(t.Context()); err != nil || len(changes) != 1 {
		t.Fatalf("activation=%v err=%v", changes, err)
	}
	request.Operation = "prepare"
	request.UploadID = ""
	reply := e.do(t, "POST", path, request, nil)
	var receipt api.DeploymentStagingReceipt
	if err = json.Unmarshal(reply.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if reply.Code != 200 || receipt.Status != "activated" {
		t.Fatalf("status=%d %+v", reply.Code, receipt)
	}
}
