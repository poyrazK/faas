// adr: 597
package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func smokeCommandForTest() []string {
	return []string{api.AppTaskServiceBindingSmokeCommand, "billing", uuid.NewString(), "/ready", "200"}
}

func declareSmokeBinding(t *testing.T, e testEnv, app state.App) {
	t.Helper()
	manifest := state.AppManifest{ServiceBindings: api.ServiceBindingsForTargets([]string{"billing"})}
	if _, err := e.store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
}

func TestBindingSmokeSelectsExactZeroTrafficCaller(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, serving := seedAppTaskDeployment(t, e, "smoke-caller")
	declareSmokeBinding(t, e, app)
	for _, scope := range []string{"default", "staging"} {
		candidate := seedBindingCandidate(t, e, app, scope)
		command := smokeCommandForTest()
		task := createAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{SmokeDeploymentID: candidate.ID, Command: command})
		stored, err := e.store.AppTaskByID(context.Background(), e.acct.ID, app.ID, task.ID)
		if err != nil || task.DeploymentID != candidate.ID || task.DeploymentScope != scope || stored.ArtifactKey != candidate.RootfsKey ||
			stored.ImageDigest != candidate.ImageDigest || stored.BindingVerification != nil {
			t.Fatalf("candidate smoke pin: %+v stored=%+v err=%v", task, stored, err)
		}
		candidateAfter, _ := e.store.DeploymentByID(context.Background(), candidate.ID)
		servingAfter, _ := e.store.DeploymentByID(context.Background(), serving.ID)
		if candidateAfter.TrafficPercent != 0 || servingAfter.TrafficPercent != serving.TrafficPercent {
			t.Fatal("smoke admission changed traffic")
		}
	}
}

func TestBindingSmokeRejectsUnsafeCallerSelections(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, live := seedAppTaskDeployment(t, e, "smoke-validation")
	declareSmokeBinding(t, e, app)
	_, foreign := seedAppTaskDeployment(t, e, "smoke-foreign")
	retired := seedBindingCandidate(t, e, app, "default")
	if err := e.store.UpdateDeploymentStatus(context.Background(), retired.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	unmaterialized, err := e.store.CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: appTaskTestDigest})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, id string
		status   int
	}{{"missing", uuid.NewString(), 404}, {"foreign", foreign.ID, 404}, {"retired", retired.ID, 409}, {"unmaterialized", unmaterialized.ID, 409}, {"invalid", "v12", 422}} {
		t.Run(tc.name, func(t *testing.T) {
			r := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/tasks", api.CreateAppTaskRequest{SmokeDeploymentID: tc.id, Command: smokeCommandForTest()}, nil)
			if r.Code != tc.status {
				t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
			}
		})
	}
	unbound, unboundDeployment := seedAppTaskDeployment(t, e, "smoke-unbound")
	r := e.do(t, http.MethodPost, "/v1/apps/"+unbound.Slug+"/tasks", api.CreateAppTaskRequest{SmokeDeploymentID: unboundDeployment.ID, Command: smokeCommandForTest()}, nil)
	if r.Code != http.StatusForbidden {
		t.Fatalf("unbound smoke: %d %s", r.Code, r.Body.String())
	}
	r = e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/operations/tasks", api.ExclusiveAppTaskOperationRequest{Policy: "maintenance", Key: []byte(`"key"`), Task: api.CreateAppTaskRequest{SmokeDeploymentID: live.ID, Command: smokeCommandForTest()}}, nil)
	if r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("exclusive smoke selector: %d %s", r.Code, r.Body.String())
	}
}

type retiringSmokeCallerStore struct{ *state.MemStore }

func (s retiringSmokeCallerStore) CreateAppTask(ctx context.Context, params state.CreateAppTaskParams) (state.AppTask, error) {
	if err := s.UpdateDeploymentStatus(ctx, params.DeploymentID, state.DeploySuperseded, ""); err != nil {
		return state.AppTask{}, err
	}
	return s.MemStore.CreateAppTask(ctx, params)
}

func TestBindingSmokeCallerMustRemainLiveAtAdmission(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, candidate := seedAppTaskDeployment(t, e, "smoke-race")
	declareSmokeBinding(t, e, app)
	e.s.store = retiringSmokeCallerStore{e.store}
	r := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/tasks", api.CreateAppTaskRequest{SmokeDeploymentID: candidate.ID, Command: smokeCommandForTest()}, nil)
	if r.Code != http.StatusConflict {
		t.Fatalf("retired caller admitted: %d %s", r.Code, r.Body.String())
	}
	rows, err := e.store.ListAppTasks(context.Background(), e.acct.ID, app.ID, 10, 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("retired caller persisted task: %+v %v", rows, err)
	}
}
