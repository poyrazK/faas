package apidserver

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
)

func TestDeploymentConfigurationDetailOwnership(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "configuration"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := e.store.DeploymentCronStagingSnapshot(t.Context(), e.acct.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := api.DeploymentCronStagingRequest{Operation: "prepare", SessionID: strings.Repeat("a", 32), ExpectedAppID: app.ID, ExpectedRevision: snapshot.Revision, ExpectedDeploymentID: snapshot.LatestDeploymentID, Before: snapshot.Crons, Desired: []api.DeploymentCronConfig{{ID: uuid.NewString(), Schedule: "0 * * * *", Path: "/tick", Enabled: true, Timezone: "UTC"}}}
	if _, err := e.store.ApplyDeploymentCronStaging(t.Context(), app.ID, state.DeploymentCronStagingParams{AccountID: e.acct.ID, Request: r, Limits: api.MustLimitsFor(api.PlanPro)}); err != nil {
		t.Fatal(err)
	}
	dep, err := e.store.CreateDeployment(state.ContextWithDeploymentSubmission(t.Context(), r.SessionID), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/deployments/" + dep.ID
	reply := e.do(t, "GET", path, nil, nil)
	var response api.DeploymentResponse
	if err := json.Unmarshal(reply.Body.Bytes(), &response); err != nil || reply.Code != 200 || response.Configuration == nil || response.Configuration.Crons.Status != "pending" {
		t.Fatalf("status=%d response=%+v err=%v body=%s", reply.Code, response, err, reply.Body)
	}
	if _, err := e.store.CreateCron(t.Context(), app.ID, "0 2 * * *", "/operator", true); err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.ReconcileDeploymentCrons(t.Context()); err != nil {
		t.Fatal(err)
	}
	reply = e.do(t, "GET", path, nil, nil)
	if err := json.Unmarshal(reply.Body.Bytes(), &response); err != nil || response.Configuration.Crons.Status != "conflicted" || response.Configuration.Crons.Reason != "cron_changed" || response.Configuration.Complete {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	other, err := e.store.CreateAccount(t.Context(), "configuration-other@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := e.store.CreateApp(t.Context(), state.App{AccountID: other.ID, Slug: "other"})
	if err != nil {
		t.Fatal(err)
	}
	otherDep, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: otherApp.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if reply := e.do(t, "GET", "/v1/deployments/"+otherDep.ID, nil, nil); reply.Code != 404 {
		t.Fatalf("cross-account=%d %s", reply.Code, reply.Body)
	}
	deployments, err := e.store.ListDeploymentsForApp(t.Context(), app.ID, 10, 0)
	if err != nil || len(deployments) != 1 {
		t.Fatalf("read created deployment=%v err=%v", deployments, err)
	}
	crons, err := e.store.ListCronsForApp(t.Context(), app.ID)
	if err != nil || len(crons) != 1 || crons[0].Path != "/operator" {
		t.Fatalf("read overwrote schedule=%v err=%v", crons, err)
	}
}
