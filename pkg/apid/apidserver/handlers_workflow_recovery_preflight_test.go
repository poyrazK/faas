package apidserver

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"testing"
	"time"
)

func TestWorkflowRecoveryPreflightHTTP(t *testing.T) {
	e := setup(t, api.PlanHobby)
	ctx := context.Background()
	appID := mustSeedApp(t, e, "preflight")
	run := &state.WorkflowRun{AppID: appID, WorkflowName: "recover", DefinitionSnapshot: json.RawMessage(`{"name":"recover","steps":[{"name":"send","run":"send"}]}`)}
	if err := e.store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusDead, nil, nil); err != nil {
		t.Fatal(err)
	}
	job, err := e.store.CreateEventRecovery(ctx, e.acct.ID, appID, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: "recover", Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/event-recoveries/" + job.ID + "/preflight"
	rec := e.do(t, http.MethodGet, path, nil, nil)
	var report api.EventRecoveryPreflight
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &report) != nil || report.LikelySkippedCount != 1 || report.Sample[0].Reason != "resume_ineligible" {
		t.Fatalf("pending=%d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cached safety report")
	}
	if rec = e.do(t, http.MethodGet, path+"?retry=true", nil, nil); rec.Code != 409 {
		t.Fatalf("active=%d %s", rec.Code, rec.Body.String())
	}
	if _, err := e.store.CancelEventRecovery(ctx, e.acct.ID, job.ID); err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodGet, path+"?retry=true", nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &report) != nil || report.ReviewMode != "workflow_retry" || report.ReviewedCount != 1 || report.PendingCount != 0 || report.Sample[0].Reason != "not_admitted" {
		t.Fatalf("retry=%d %s", rec.Code, rec.Body.String())
	}
	for _, query := range []string{"?retry=false", "?retry=true&retry=true", "?retry=true&extra=1"} {
		if rec = e.do(t, http.MethodGet, path+query, nil, nil); rec.Code != 400 {
			t.Fatalf("query=%s code=%d", query, rec.Code)
		}
	}
	if rec = e.do(t, http.MethodGet, "/v1/event-recoveries/"+uuid.NewString()+"/preflight?retry=true", nil, nil); rec.Code != 404 {
		t.Fatalf("missing=%d", rec.Code)
	}
	saved, err := e.store.GetEventRecovery(ctx, e.acct.ID, job.ID)
	if err != nil || saved.QueuedCount != 0 || saved.CancelledCount != 1 || saved.UpdatedAt.After(time.Now().UTC()) {
		t.Fatalf("mutation=%+v %v", saved, err)
	}
}
