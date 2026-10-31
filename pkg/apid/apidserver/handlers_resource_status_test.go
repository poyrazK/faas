package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// The status subresources return conditions derived from existing evidence
// (ADR-20261030151810): an idle app is ready without being woken, a failed
// deployment is terminal, and another account's resources are not found.
func TestResourceStatusAPI(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "status-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	// A live deployment that finished its readiness stage, as one is after a
	// deploy; until then it is routable but reports verifying.
	readiness := []byte(`{"current":"live","history":[{"name":"readiness","status":"completed"}]}`)
	live, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Status: state.DeployLive, TrafficPercent: 100, TrafficPercentExplicit: true, RootfsPath: "/artifact", StageState: readiness})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Status: state.DeployFailed, Error: "build exited 1"})
	if err != nil {
		t.Fatal(err)
	}

	appStatus := readStatus(t, e, "/v1/apps/status-app/status")
	if appStatus.Kind != api.ResourceKindApp || appStatus.Name != "status-app" || appStatus.ObservedAt == "" {
		t.Fatalf("app status header = %+v", appStatus)
	}
	if ready := condition(t, appStatus, api.ConditionReady); ready.Status != api.ConditionTrue || ready.Reason != "idle" {
		t.Errorf("idle app Ready = %+v, want True/idle", ready)
	}
	instances, err := (*e.Store()).ListInstancesForApp(t.Context(), app.ID)
	if err != nil || len(instances) != 0 {
		t.Fatal("a status read must not wake an idle app")
	}

	if ready := condition(t, readStatus(t, e, "/v1/deployments/"+live.ID+"/status"), api.ConditionReady); ready.Status != api.ConditionTrue {
		t.Errorf("live deployment Ready = %+v", ready)
	}
	ready := condition(t, readStatus(t, e, "/v1/deployments/"+failed.ID+"/status"), api.ConditionReady)
	if ready.Status != api.ConditionFalse || ready.Reason != "failed" || !ready.Terminal || ready.Message != "The deployment failed: build exited 1" {
		t.Errorf("failed deployment Ready = %+v", ready)
	}

	other, err := (*e.Store()).CreateAccount(t.Context(), "other-status@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherAppID := apidserver.MustSeedAppFor(t, (*e.Store()), other.ID, "private-status")
	otherDeployment, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: otherAppID, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/apps/private-status/status", "/v1/apps/missing/status", "/v1/deployments/" + otherDeployment.ID + "/status"} {
		if rec := e.Do(t, http.MethodGet, path, nil, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestResourceStatusAPI_ReadScopeRequired(t *testing.T) {
	e := apidserver.SetupWithScopes(t, []string{api.ScopeDeployWrite})
	apidserver.MustSeedApp(t, e, "status-app")
	if rec := e.Do(t, http.MethodGet, "/v1/apps/status-app/status", nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func readStatus(t *testing.T, e apidserver.TestEnv, path string) api.ResourceStatus {
	t.Helper()
	rec := e.Do(t, http.MethodGet, path, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("%s: conditions must not be cached", path)
	}
	var got api.ResourceStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func condition(t *testing.T, s api.ResourceStatus, typ string) api.Condition {
	t.Helper()
	for _, c := range s.Conditions {
		if c.Type == typ {
			return c
		}
	}
	t.Fatalf("%s has no %s condition: %+v", s.Name, typ, s.Conditions)
	return api.Condition{}
}
