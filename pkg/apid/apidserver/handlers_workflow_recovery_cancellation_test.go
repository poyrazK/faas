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

func TestWorkflowRecoveryCancellationHTTP(t *testing.T) {
	e := setup(t, api.PlanHobby)
	ctx := context.Background()
	app := mustSeedApp(t, e, "cancel-preview")
	run := &state.WorkflowRun{AppID: app, WorkflowName: "recover", DefinitionSnapshot: json.RawMessage(`{"name":"recover","steps":[{"name":"send","run":"send"}]}`)}
	if err := e.store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusDead, nil, nil); err != nil {
		t.Fatal(err)
	}
	job, err := e.store.CreateEventRecovery(ctx, e.acct.ID, app, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: "recover", Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"workflow-cancellation-preview", "workflow-cancel"} {
		path := "/v1/event-recoveries/" + job.ID + "/" + endpoint
		rec := e.do(t, http.MethodPost, path, map[string]any{"position": 1, "expected_resume_count": 1, "reason": "incident"}, nil)
		var out api.WorkflowRecoveryCancellationResponse
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Outcome != "not_admitted" || out.Preview != (endpoint == "workflow-cancellation-preview") {
			t.Fatalf("%s=%d %s", endpoint, rec.Code, rec.Body.String())
		}
		for _, body := range []map[string]any{{"position": 1, "reason": "incident"}, {"position": 1, "expected_resume_count": 0, "reason": "incident"}, {"position": 1, "expected_resume_count": 1, "reason": " "}} {
			if rec = e.do(t, http.MethodPost, path, body, nil); rec.Code != 400 {
				t.Fatalf("invalid=%d %s", rec.Code, rec.Body.String())
			}
		}
		if rec = e.do(t, http.MethodPost, "/v1/event-recoveries/"+uuid.NewString()+"/"+endpoint, map[string]any{"position": 1, "expected_resume_count": 1, "reason": "incident"}, nil); rec.Code != 404 {
			t.Fatalf("missing=%d %s", rec.Code, rec.Body.String())
		}
	}
	h, err := e.store.ListEventRecoveryHistory(ctx, e.acct.ID, job.ID, 0, 100)
	if err != nil || len(h.Entries) != 1 {
		t.Fatalf("HTTP mutated history=%+v %v", h, err)
	}
	deployment, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	if err = e.store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "send", Status: state.WorkflowStepStatusDead, Attempt: 1}}); err != nil {
		t.Fatal(err)
	}
	if did, err := e.store.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil || !did {
		t.Fatalf("admit=%v %v", did, err)
	}
	requestID := uuid.NewString()
	path := "/v1/event-recoveries/" + job.ID + "/workflow-cancel"
	rec := e.do(t, http.MethodPost, path, map[string]any{"request_id": requestID, "position": 1, "expected_resume_count": 1, "reason": "provider incident"}, nil)
	var cancelled api.WorkflowRecoveryCancellationResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &cancelled) != nil || cancelled.Outcome != "cancelled" {
		t.Fatalf("cancel=%d %s", rec.Code, rec.Body.String())
	}
	receiptPath := "/v1/event-recoveries/" + job.ID + "/workflow-action-receipts/" + requestID
	retained := e.do(t, http.MethodGet, receiptPath, nil, nil)
	var receipt api.WorkflowRecoveryActionReceipt
	if retained.Code != 200 || json.Unmarshal(retained.Body.Bytes(), &receipt) != nil || receipt.Response.Outcome != "cancelled" || receipt.Request.RequestID != requestID || receipt.ActorKind != "api_key" || receipt.ActorID == "" {
		t.Fatalf("receipt=%d %s", retained.Code, retained.Body.String())
	}
	conflict := e.do(t, http.MethodPost, path, map[string]any{"request_id": requestID, "position": 1, "expected_resume_count": 1, "reason": "changed"}, nil)
	if conflict.Code != 409 {
		t.Fatalf("changed request=%d %s", conflict.Code, conflict.Body.String())
	}
	for _, suffix := range []string{requestID, uuid.NewString()} {
		retained = e.do(t, http.MethodGet, "/v1/apps/cancel-preview/event-recoveries/by-request/"+suffix, nil, nil)
		if retained.Code != 404 {
			t.Fatalf("missing job lookup=%d %s", retained.Code, retained.Body.String())
		}
	}
	retained = e.do(t, http.MethodGet, "/v1/apps/cancel-preview/event-recoveries/by-request/"+job.Selection.RequestID, nil, nil)
	var found api.EventRecoveryJob
	if retained.Code != 200 || json.Unmarshal(retained.Body.Bytes(), &found) != nil || found.ID != job.ID {
		t.Fatalf("job lookup=%d %s", retained.Code, retained.Body.String())
	}
	h, err = e.store.ListEventRecoveryHistory(ctx, e.acct.ID, job.ID, 0, 100)
	if err != nil || len(h.Entries) != 2 || h.Entries[1].ActorKind != "api_key" || h.Entries[1].ActorID == "" || h.Entries[1].Reason != "provider incident" || h.Entries[1].TargetRunID != run.ID {
		t.Fatalf("authenticated audit=%+v %v", h, err)
	}

}
