package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowScheduleInspectionAndAccountIsolation(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "scheduled-app")
	deployment, err := e.store.CreateDeployment(context.Background(), state.Deployment{AppID: app.ID,
		Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:abc",
		Workflows: json.RawMessage(`[{"name":"nightly","trigger":{"type":"schedule","schedule":"* * * * *"},"steps":[{"name":"main","run":"report"}]}]`)})
	if err != nil {
		t.Fatal(err)
	}
	var schedules state.WorkflowScheduleStore = e.store
	for _, at := range []time.Time{time.Date(2026, 10, 2, 10, 0, 30, 0, time.UTC), time.Date(2026, 10, 2, 10, 1, 30, 0, time.UTC)} {
		if _, _, err := schedules.AdmitScheduledWorkflow(context.Background(), app.ID, deployment.ID, "nightly", at); err != nil {
			t.Fatal(err)
		}
	}
	recorder := e.do(t, "GET", "/v1/apps/"+app.Slug+"/workflows/schedules", nil, nil)
	var response api.ListWorkflowSchedulesResponse
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &response) != nil {
		t.Fatalf("response=%d %s", recorder.Code, recorder.Body.String())
	}
	if len(response.Schedules) != 1 || response.Schedules[0].LastStatus != state.WorkflowScheduleStarted || response.Schedules[0].LastRunID == "" || response.Schedules[0].Timezone != "UTC" {
		t.Fatalf("schedules=%+v", response)
	}
	e.s.WithWorkflowRuntimeEnabled(false)
	recorder = e.do(t, "GET", "/v1/apps/"+app.Slug+"/workflows/schedules", nil, nil)
	response = api.ListWorkflowSchedulesResponse{}
	if json.Unmarshal(recorder.Body.Bytes(), &response) != nil || response.RuntimeEnabled || response.UnavailableReason != "runtime_disabled" || response.Schedules[0].NextFireAt != "" {
		t.Fatal("disabled runtime was not reported")
	}
	other, err := e.store.CreateAccount(context.Background(), "other-schedule@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := e.store.CreateApp(context.Background(), state.App{AccountID: other.ID, Slug: "other-schedule", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	recorder = e.do(t, "GET", "/v1/apps/"+otherApp.Slug+"/workflows/schedules", nil, nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("cross-account read = %d: %s", recorder.Code, recorder.Body.String())
	}
}
