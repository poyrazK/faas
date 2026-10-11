package main

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"testing"
)

func TestWorkflowRunRecoveryHTTP(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "run-recovery")
	e.s.workflowRuntimeEnabled = false
	req := api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: "recover", Runs: []api.WorkflowRunRecoveryTarget{{RunID: uuid.NewString()}}}}
	// Native recovery preserves the direct resume runtime gate.
	rec := e.do(t, http.MethodPost, "/v1/apps/run-recovery/event-recoveries", req, nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("disabled=%d %s", rec.Code, rec.Body.String())
	}
	e.s.workflowRuntimeEnabled = true
	rec = e.do(t, http.MethodPost, "/v1/apps/run-recovery/event-recoveries", req, nil)
	var problem api.Problem
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != api.CodeEventRecoveryChanged {
		t.Fatalf("missing=%d %s", rec.Code, rec.Body.String())
	}
	req.EventSource = "orders"
	rec = e.do(t, http.MethodPost, "/v1/apps/run-recovery/event-recoveries", req, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mixed=%d %s", rec.Code, rec.Body.String())
	}
}
