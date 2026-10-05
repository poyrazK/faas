package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowJoinAuthoringAndInspection(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "join-inspection")
	ctx := context.Background()
	if _, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:x", Workflows: json.RawMessage(`[]`)}); err != nil {
		t.Fatal(err)
	}
	spec := api.WorkflowSpec{Name: "joined", Steps: []api.WorkflowStepSpec{
		{Name: "a", Run: "a"}, {Name: "b", Run: "b"},
		{Name: "merge", DependsOn: []string{"a", "b"}, Join: &api.WorkflowJoinSpec{OutputFrom: []string{"b", "a"}}},
	}}
	base := "/v1/apps/" + app.Slug + "/automations"
	response := e.do(t, "POST", base+":validate", api.ValidateAutomationRequest{Definition: spec}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("validate=%d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "PUT", base+"/joined", api.SaveAutomationDraftRequest{Definition: spec}, nil)
	var draft api.AutomationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &draft) != nil {
		t.Fatalf("save=%d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "POST", base+"/joined/publish", api.PublishAutomationRequest{ExpectedVersion: draft.Version}, nil)
	var published api.AutomationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &published) != nil || published.Published == nil || published.Published.Steps[2].Join == nil || published.Published.Steps[2].Join.OutputFrom[0] != "b" {
		t.Fatalf("publication lost ordered join=%d %s", response.Code, response.Body.String())
	}
	snapshot, _ := json.Marshal(published.Published)
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, Input: json.RawMessage(`{}`), DefinitionSnapshot: snapshot}
	if err := e.store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := e.store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "a"}, {StepName: "b"}, {StepName: "merge"}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b", "a"} {
		if err := e.store.MarkWorkflowStepStatus(ctx, run.ID, name, state.WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"done":true}`), nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.store.ResolveWorkflowStepJoin(ctx, run.ID, "merge"); err != nil {
		t.Fatal(err)
	}
	response = e.do(t, "GET", "/v1/workflows/runs/"+run.ID+"/steps", nil, nil)
	var result api.ListWorkflowStepsResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatalf("inspection=%d %s", response.Code, response.Body.String())
	}
	for _, step := range result.Steps {
		if step.StepName == "merge" {
			var output struct {
				Source string          `json:"source"`
				Value  map[string]bool `json:"value"`
			}
			if json.Unmarshal(step.Output, &output) != nil || output.Source != "b" || !output.Value["done"] || step.Attempt != 0 || step.Status != "succeeded" || step.FinishedAt == nil {
				t.Fatalf("join inspection lost selection: %+v", step)
			}
			return
		}
	}
	t.Fatal("inspection omitted join")
}
