package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedGuardedDeploy(t *testing.T, e testEnv) (state.App, state.Deployment) {
	t.Helper()
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "guarded-app", Type: state.AppTypeApp,
		Status: state.AppActive, RAMMB: 256, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Status: state.DeployLive,
		Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64), CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return app, dep
}

type raceGuardedAppStore struct {
	state.Store // teststorecheck:allow implements OrgActivityAppConfigMutationStore, which statetest.Store would hide
	state.OrgActivityAppConfigMutationStore
	beforeUpdate func()
}

type guardedSettingsNotifier struct {
	stubNotifier
	appChanges int
}

func (n *guardedSettingsNotifier) Notify(_ context.Context, channel, _ string) error {
	if channel == db.NotifyAppChanged {
		n.appChanges++
	}
	return nil
}

func (s *raceGuardedAppStore) UpdateApp(ctx context.Context, id string, params state.UpdateAppParams) (state.App, error) {
	s.beforeUpdate()
	return s.Store.UpdateApp(ctx, id, params)
}

func (s *raceGuardedAppStore) UpdateAppWithActivity(ctx context.Context, id string, params state.UpdateAppParams, entry state.OrgActivity, build state.OrgActivityAppConfigBuilder) (state.App, int64, error) {
	s.beforeUpdate()
	return s.OrgActivityAppConfigMutationStore.UpdateAppWithActivity(ctx, id, params, entry, build)
}

func TestDeploySettingsGuardRejectsRacingChanges(t *testing.T) {
	for _, mode := range []string{"target", "new-deployment", "unrelated", "already-desired", "other-field-in-step"} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			notifier := &guardedSettingsNotifier{}
			e.s.notif = notifier
			app, dep := seedGuardedDeploy(t, e)
			value := 30
			req := api.UpdateAppRequest{RequestTimeoutS: &value, DeploySettingsCondition: &api.AppDeploySettingsCondition{
				ExpectedAppID: app.ID, ExpectedDeploymentID: dep.ID,
				ExpectedSettings: map[string]json.RawMessage{"request_timeout_s": json.RawMessage(`0`)},
			}}
			if mode == "other-field-in-step" {
				req.DeploySettingsCondition.ExpectedSettings["stop_signal"] = json.RawMessage(`""`)
			}
			e.s.store = &raceGuardedAppStore{Store: e.store, OrgActivityAppConfigMutationStore: e.store, beforeUpdate: func() {
				current, err := e.store.AppByID(t.Context(), app.ID)
				if err != nil {
					t.Fatal(err)
				}
				manifest := current.Manifest
				switch mode {
				case "target":
					manifest.RequestTimeoutS = 45
				case "already-desired":
					manifest.RequestTimeoutS = 30
				case "unrelated":
					manifest.Env = map[string]string{"PRESERVE": "fixture-secret"}
					manifest.Healthz = "/new-health"
				case "other-field-in-step":
					manifest.StopSignal = "SIGQUIT"
				case "new-deployment":
					if _, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Status: state.DeployPending,
						Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("b", 64), CreatedAt: dep.CreatedAt.Add(time.Second)}); err != nil {
						t.Fatal(err)
					}
					return
				}
				if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
					t.Fatal(err)
				}
			}}
			rec := e.do(t, http.MethodPatch, "/v1/apps/guarded-app", req, nil)
			want := http.StatusConflict
			if mode == "unrelated" {
				want = http.StatusOK
			}
			if rec.Code != want {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			if want == http.StatusConflict && notifier.appChanges != 0 {
				t.Fatal("rejected update notified app configuration change")
			}
			current, err := e.store.AppByID(t.Context(), app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unrelated" {
				if current.Manifest.RequestTimeoutS != 30 || current.Manifest.Env["PRESERVE"] != "fixture-secret" || current.Manifest.Healthz != "/new-health" {
					t.Fatalf("unrelated change overwritten: %+v", current.Manifest)
				}
			} else {
				var problem api.Problem
				if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
					t.Fatal(err)
				}
				if problem.Code != api.CodeDeploySettingsConflict || strings.Contains(rec.Body.String(), "fixture-secret") {
					t.Fatalf("problem=%s", rec.Body)
				}
				if mode == "target" && (current.Manifest.RequestTimeoutS != 45 || len(problem.Errors) != 1 || problem.Errors[0].Field != "request_timeout_s") {
					t.Fatalf("target was overwritten or conflict did not identify field: %+v", problem)
				}
				if (mode == "new-deployment" || mode == "other-field-in-step") && current.Manifest.RequestTimeoutS != 0 {
					t.Fatal("stale settings were applied")
				}
			}
			deployments, err := e.store.ListDeploymentsForApp(t.Context(), app.ID, 10, 0)
			count := 1
			if mode == "new-deployment" {
				count = 2
			}
			if err != nil || len(deployments) != count {
				t.Fatalf("unexpected deployment: count=%d err=%v", len(deployments), err)
			}
		})
	}
}

func TestDeploySettingsGuardValidatesIdentityAndShape(t *testing.T) {
	for _, mode := range []string{"wrong-app", "wrong-deployment", "failed", "unknown-field", "uncovered-field", "unrelated-patch"} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			app, dep := seedGuardedDeploy(t, e)
			value := 30
			condition := &api.AppDeploySettingsCondition{ExpectedAppID: app.ID, ExpectedDeploymentID: dep.ID,
				ExpectedSettings: map[string]json.RawMessage{"request_timeout_s": json.RawMessage(`0`)}}
			req := api.UpdateAppRequest{RequestTimeoutS: &value, DeploySettingsCondition: condition}
			want := http.StatusConflict
			switch mode {
			case "wrong-app":
				condition.ExpectedAppID = dep.ID
			case "wrong-deployment":
				condition.ExpectedDeploymentID = app.ID
			case "failed":
				if err := e.store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployFailed, "fixture failure"); err != nil {
					t.Fatal(err)
				}
			case "unknown-field":
				condition.ExpectedSettings["env"] = json.RawMessage(`{}`)
				want = http.StatusBadRequest
			case "uncovered-field":
				condition.ExpectedSettings = map[string]json.RawMessage{"max_retries": json.RawMessage(`0`)}
				want = http.StatusBadRequest
			case "unrelated-patch":
				req.RAMMB = &value
				want = http.StatusBadRequest
			}
			rec := e.do(t, http.MethodPatch, "/v1/apps/guarded-app", req, nil)
			if rec.Code != want {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			current, err := e.store.AppByID(t.Context(), app.ID)
			if err != nil || current.Manifest.RequestTimeoutS != 0 {
				t.Fatalf("settings changed: err=%v", err)
			}
		})
	}
}

func TestAppReadIncludesGuardedLifecycleSettings(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, _ := seedGuardedDeploy(t, e)
	manifest := app.Manifest
	manifest.StopGracePeriodS, manifest.StopSignal = 12, "SIGTERM"
	manifest.WorkerReplicas = &state.WorkerScaling{Min: 1, Max: 2, Metric: "queue_depth", Name: "queue", Target: 5}
	if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodGet, "/v1/apps/guarded-app", nil, nil)
	var response api.AppResponse
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Manifest.StopGracePeriod != 12*time.Second || response.Manifest.StopSignal != "SIGTERM" || response.Manifest.WorkerReplicas == nil || response.Manifest.WorkerReplicas.Name != "queue" {
		t.Fatalf("incomplete lifecycle snapshot: %+v", response.Manifest)
	}
}
