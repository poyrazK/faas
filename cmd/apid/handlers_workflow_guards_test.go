package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowGuardInspectionExposesFalseDecision(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "guard-inspection")
	spec := api.WorkflowSpec{Name: "guarded", Steps: []api.WorkflowStepSpec{{Name: "send", Run: "send", When: &api.WorkflowGuardSpec{Ref: "input.active", Op: "eq", Value: json.RawMessage("true")}}}}
	snapshot, _ := json.Marshal(spec)
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, Input: json.RawMessage(`{"active":false}`), DefinitionSnapshot: snapshot}
	ctx := context.Background()
	if err := e.store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := e.store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "send"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.ResolveWorkflowStepGuard(ctx, run.ID, "send"); err != nil {
		t.Fatal(err)
	}
	response := e.do(t, "GET", "/v1/workflows/runs/"+run.ID+"/steps", nil, nil)
	var result api.ListWorkflowStepsResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Steps) != 1 {
		t.Fatalf("inspection=%d %s", response.Code, response.Body.String())
	}
	step := result.Steps[0]
	if step.WhenMatched == nil || *step.WhenMatched || step.WhenEvaluatedAt == nil || step.SkipReason == nil || *step.SkipReason != state.WorkflowSkipWhenFalse || step.Attempt != 0 {
		t.Fatalf("false decision omitted from inspection: %+v", step)
	}
}
