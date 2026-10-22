package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowInitialFailureInboxHTTP(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "failures")
	path := "/v1/apps/failures/workflow-failures"
	rec := e.do(t, http.MethodGet, path, nil, nil)
	var out api.WorkflowInitialFailures
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Items == nil || out.Coverage != "partial" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("inbox %d %s", rec.Code, rec.Body.String())
	}
	for _, query := range []string{"?limit=101", "?limit=bad", "?after=invalid", "?limit=1&limit=2", "?unexpected=1"} {
		if rec = e.do(t, http.MethodGet, path+query, nil, nil); rec.Code != 400 {
			t.Fatalf("invalid %s %d", query, rec.Code)
		}
	}
	if rec = e.do(t, http.MethodGet, "/v1/apps/absent/workflow-failures", nil, nil); rec.Code != 404 {
		t.Fatalf("app scope %d", rec.Code)
	}
	if rec = e.do(t, http.MethodGet, path+"/"+uuid.NewString()+"/recovery-preview", nil, nil); rec.Code != 404 {
		t.Fatalf("evidence scope %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, http.MethodGet, path+"/invalid/recovery-preview", nil, nil); rec.Code != 400 {
		t.Fatalf("invalid evidence %d", rec.Code)
	}
}

func TestWorkflowInitialRecoveryPreviewHTTP(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := mustSeedApp(t, e, "first-recovery")
	run := &state.WorkflowRun{AppID: app, WorkflowName: "recover", DefinitionSnapshot: json.RawMessage(`{"name":"recover","steps":[{"name":"send","run":"send"}]}`)}
	if err := e.store.CreateWorkflowRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkWorkflowRunStatus(context.Background(), run.ID, state.WorkflowRunStatusDead, nil, nil); err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/first-recovery/workflow-failures"
	rec := e.do(t, http.MethodGet, path, nil, nil)
	var inbox api.WorkflowInitialFailures
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &inbox) != nil || len(inbox.Items) != 1 {
		t.Fatalf("inbox %d %s", rec.Code, rec.Body.String())
	}
	previewPath := path + "/" + inbox.Items[0].ID + "/recovery-preview"
	rec = e.do(t, http.MethodGet, previewPath, nil, nil)
	var out api.WorkflowInitialRecoveryPreview
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Failure.RunID != run.ID || out.Reason != "resume_ineligible" || out.Target != nil || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("preview %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, http.MethodGet, previewPath+"?unexpected=1", nil, nil); rec.Code != 400 {
		t.Fatalf("query %d", rec.Code)
	}
}
