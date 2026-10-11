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
	if len(response.Schedules) != 1 || response.Schedules[0].LastStatus != state.WorkflowScheduleStarted || response.Schedules[0].LastRunID == "" || response.Schedules[0].Timezone != "UTC" || response.Schedules[0].CatchUp != "skip" || response.Schedules[0].CatchUpWindow != "" {
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

func TestWorkflowScheduleInspectionShowsRecoveryPolicy(t *testing.T) {
	deployment := state.Deployment{ID: "deployment", Workflows: json.RawMessage(`[{"name":"report","trigger":{"type":"schedule","schedule":"0 7 * * *","catch_up":"latest","catch_up_window":"2h"},"steps":[{"name":"main","run":"report"}]}]`)}
	schedules, err := workflowScheduleResponses(deployment, nil, time.Now().UTC())
	if err != nil || len(schedules) != 1 || schedules[0].CatchUp != "latest" || schedules[0].CatchUpWindow != "2h0m0s" {
		t.Fatalf("schedules=%+v err=%v", schedules, err)
	}
	response := tenantWorkflowScheduleResponse(state.TenantWorkflowSchedule{CatchUp: "latest", CatchUpWindow: "2h0m0s"})
	if response.CatchUp != "latest" || response.CatchUpWindow != "2h0m0s" {
		t.Fatalf("tenant response=%+v", response)
	}
}

func TestWorkflowScheduleHistoryPaginationAndAccountIsolation(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "history-app")
	ctx := context.Background()
	deployment, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:history",
		Workflows: json.RawMessage(`[{"name":"nightly","trigger":{"type":"schedule","schedule":"* * * * *"},"steps":[{"name":"main","run":"report"}]}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Minute)
	for cycle := range 3 {
		if _, _, err := e.store.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at.Add(time.Duration(cycle)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	path := "/v1/apps/" + app.Slug + "/workflows/schedules/occurrences"
	var first api.ListWorkflowScheduleOccurrencesResponse
	rec := e.do(t, "GET", path+"?limit=1", nil, nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &first) != nil || len(first.Occurrences) != 1 || first.NextCursor == "" || first.Occurrences[0].Status != state.WorkflowScheduleSkippedOverlap {
		t.Fatalf("first=%d %s", rec.Code, rec.Body)
	}
	var second api.ListWorkflowScheduleOccurrencesResponse
	rec = e.do(t, "GET", path+"?limit=1&cursor="+first.NextCursor, nil, nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &second) != nil || len(second.Occurrences) != 1 || second.NextCursor != "" || second.Occurrences[0].RunID == "" {
		t.Fatalf("second=%d %s", rec.Code, rec.Body)
	}
	for _, query := range []string{"?limit=0", "?limit=201", "?limit=bad", "?cursor=bad", "?platform_tenant_id=bad"} {
		rec = e.do(t, "GET", path+query, nil, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("query=%s status=%d body=%s", query, rec.Code, rec.Body)
		}
	}
	other, err := e.store.CreateAccount(ctx, "history-other@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := e.store.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "history-other", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, "GET", "/v1/apps/"+otherApp.Slug+"/workflows/schedules/occurrences", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account history=%d %s", rec.Code, rec.Body)
	}
}
