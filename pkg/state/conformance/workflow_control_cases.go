package conformance

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testWorkflowControlSteps(t *testing.T, fx *Fixture) {
	t.Helper()
	spec := api.WorkflowSpec{
		Name: "controls",
		Steps: []api.WorkflowStepSpec{
			{Name: "batch", ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Run: "send"}}},
			{Name: "guarded", When: &api.WorkflowGuardSpec{Ref: "input.allowed", Op: "eq", Value: json.RawMessage("true")}},
			{Name: "left", Run: "left"},
			{Name: "right", Run: "right"},
			{Name: "merge", DependsOn: []string{"left", "right"}, Join: &api.WorkflowJoinSpec{OutputFrom: []string{"right", "left"}}},
			{Name: "skipped"},
		},
	}
	snapshot, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{
		AppID: fx.App.ID, WorkflowName: "control-steps",
		Input: json.RawMessage(`{"items":["one"],"allowed":true}`), DefinitionSnapshot: snapshot,
	}
	if err := fx.Store.CreateWorkflowRun(fx.Ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}
	steps := make([]*state.WorkflowStep, 0, len(spec.Steps))
	for _, step := range spec.Steps {
		steps = append(steps, &state.WorkflowStep{StepName: step.Name})
	}
	if err := fx.Store.CreateWorkflowSteps(fx.Ctx, run.ID, steps); err != nil {
		t.Fatalf("CreateWorkflowSteps: %v", err)
	}

	item, err := fx.Store.ResolveWorkflowForEach(fx.Ctx, run.ID, "batch")
	if err != nil || item.Item == nil || item.Complete || item.Item.StepName != api.WorkflowForEachItemName("batch", 0) {
		t.Fatalf("ResolveWorkflowForEach = (%+v, %v), want first item", item, err)
	}
	matched, err := fx.Store.ResolveWorkflowStepGuard(fx.Ctx, run.ID, "guarded")
	if err != nil || !matched {
		t.Fatalf("ResolveWorkflowStepGuard = (%v, %v), want true", matched, err)
	}
	if ready, err := fx.Store.ResolveWorkflowStepJoin(fx.Ctx, run.ID, "merge"); err != nil || ready {
		t.Fatalf("ResolveWorkflowStepJoin before branches = (%v, %v), want false", ready, err)
	}
	for _, branch := range []string{"left", "right"} {
		if err := fx.Store.MarkWorkflowStepStatus(fx.Ctx, run.ID, branch, state.WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"branch":"`+branch+`"}`), nil); err != nil {
			t.Fatalf("MarkWorkflowStepStatus(%s): %v", branch, err)
		}
	}
	if ready, err := fx.Store.ResolveWorkflowStepJoin(fx.Ctx, run.ID, "merge"); err != nil || !ready {
		t.Fatalf("ResolveWorkflowStepJoin after branches = (%v, %v), want true", ready, err)
	}
	if err := fx.Store.SkipWorkflowStep(fx.Ctx, run.ID, "skipped", state.WorkflowSkipRouteNotTaken); err != nil {
		t.Fatalf("SkipWorkflowStep: %v", err)
	}
	rows, err := fx.Store.GetWorkflowSteps(fx.Ctx, run.ID)
	if err != nil {
		t.Fatalf("GetWorkflowSteps: %v", err)
	}
	for _, step := range rows {
		if step.StepName == "skipped" && (step.Status != state.WorkflowStepStatusSkipped || step.SkipReason == nil || *step.SkipReason != state.WorkflowSkipRouteNotTaken) {
			t.Fatalf("skipped step = %+v, want route_not_taken", step)
		}
	}
}
