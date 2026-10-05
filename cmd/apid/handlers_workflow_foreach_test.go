package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowForEachAuthoringAndInspection(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "foreach-inspection")
	ctx := context.Background()
	if _, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:x", Workflows: json.RawMessage(`[]`)}); err != nil {
		t.Fatal(err)
	}
	spec := api.WorkflowSpec{Name: "batch", Steps: []api.WorkflowStepSpec{{Name: "send", ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Run: "send"}}}}}
	base := "/v1/apps/" + app.Slug + "/automations"
	response := e.do(t, "POST", base+":validate", api.ValidateAutomationRequest{Definition: spec}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("validate=%d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "PUT", base+"/batch", api.SaveAutomationDraftRequest{Definition: spec}, nil)
	var draft api.AutomationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &draft) != nil {
		t.Fatalf("save=%d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "POST", base+"/batch/publish", api.PublishAutomationRequest{ExpectedVersion: draft.Version}, nil)
	var published api.AutomationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &published) != nil || published.Published == nil || published.Published.Steps[0].ForEach == nil {
		t.Fatalf("publish=%d %s", response.Code, response.Body.String())
	}
	snapshot, _ := json.Marshal(published.Published)
	for _, items := range []string{`[]`, `[true]`} {
		run := &state.WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, Input: json.RawMessage(`{"items":` + items + `}`), DefinitionSnapshot: snapshot}
		if err := e.store.CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err := e.store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "send"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.store.ResolveWorkflowForEach(ctx, run.ID, "send"); err != nil {
			t.Fatal(err)
		}
		response = e.do(t, "GET", "/v1/workflows/runs/"+run.ID+"/steps", nil, nil)
		var result api.ListWorkflowStepsResponse
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("inspect=%d %s", response.Code, response.Body.String())
		}
		seenParent, seenItem := false, false
		for _, step := range result.Steps {
			if step.StepName == "send" {
				if step.ForEachCount == nil {
					t.Fatal("parent omitted item count")
				}
				seenParent = step.ForEachCount != nil && step.Attempt == 0
				if items == "[]" && (*step.ForEachCount != 0 || string(step.Output) != "[]") {
					t.Fatal("empty iteration omitted zero count or [] output")
				}
			} else {
				seenItem = step.ForEachParent != nil && *step.ForEachParent == "send" && step.ForEachIndex != nil && *step.ForEachIndex == 0 && string(step.Input) == "true"
			}
		}
		if !seenParent || (items != "[]" && !seenItem) {
			t.Fatalf("inspect lost iteration metadata: %s", response.Body.String())
		}
	}
}
