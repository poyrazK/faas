package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateCreateManagedExecutionWorkflow(t *testing.T) {
	valid := CreateManagedExecutionWorkflowRequest{
		WorkflowID: "agent-check", Version: "v1",
		Steps: []CreateManagedExecutionWorkflowStep{
			{Label: "inspect", Request: CreateExecutionRequest{Runtime: ExecutionRuntimeNode22, Source: "return 1"}},
			{Label: "summarize", InputFromPreviousResult: true, Request: CreateExecutionRequest{Runtime: ExecutionRuntimeNode22, Source: "return input"}},
		},
	}
	if err := ValidateCreateManagedExecutionWorkflow(valid); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	dag := CreateManagedExecutionWorkflowRequest{
		WorkflowID: "agent-dag", Version: "v1", MaxParallelSteps: 2,
		FailurePolicy: ExecutionWorkflowManagedFailureContinueIndependent,
		Steps: []CreateManagedExecutionWorkflowStep{
			{Label: "inspect", Request: CreateExecutionRequest{Runtime: ExecutionRuntimeNode22, Source: "return 1", OutputFiles: []string{"patch.diff"}}},
			{Label: "test"},
			{Label: "summarize", DependsOn: []string{"inspect", "test"}, IncludeDependencyResults: true,
				ArtifactInputs: []ManagedExecutionWorkflowArtifactInput{{FromStep: "inspect", Name: "patch.diff", Path: "input/patch.diff"}},
				Request:        CreateExecutionRequest{Runtime: ExecutionRuntimePython313, Entrypoint: "main.py", Files: []ExecutionFile{{Path: "main.py", Content: []byte("print('summary')")}}}},
		},
	}
	if err := ValidateCreateManagedExecutionWorkflow(dag); err != nil {
		t.Fatalf("valid DAG rejected: %v", err)
	}
	dependencies, err := ManagedExecutionWorkflowStepDependencies(dag)
	if err != nil || len(dependencies["summarize"]) != 2 || dependencies["summarize"][0] != "inspect" || dependencies["summarize"][1] != "test" {
		t.Fatalf("DAG dependencies = %#v, %v", dependencies, err)
	}
	duplicateDestination := dag
	duplicateDestination.Steps = append([]CreateManagedExecutionWorkflowStep(nil), dag.Steps...)
	duplicateDestination.Steps[2].ArtifactInputs = append(duplicateDestination.Steps[2].ArtifactInputs,
		ManagedExecutionWorkflowArtifactInput{FromStep: "inspect", Name: "patch.diff", Path: "input/patch.diff"})
	if err := ValidateCreateManagedExecutionWorkflow(duplicateDestination); err == nil {
		t.Fatal("duplicate artifact destination was accepted")
	}

	tests := []struct {
		name string
		edit func(*CreateManagedExecutionWorkflowRequest)
	}{
		{name: "first step previous result", edit: func(req *CreateManagedExecutionWorkflowRequest) { req.Steps[0].InputFromPreviousResult = true }},
		{name: "artifact input", edit: func(req *CreateManagedExecutionWorkflowRequest) {
			req.Steps[1].Request.ArtifactInputs = []ExecutionArtifactInput{{ExecutionID: "run", Name: "file", Path: "file"}}
		}},
		{name: "undeclared artifact", edit: func(req *CreateManagedExecutionWorkflowRequest) {
			req.Steps[1].ArtifactInputs = []ManagedExecutionWorkflowArtifactInput{{FromStep: "inspect", Name: "missing", Path: "input/missing"}}
		}},
		{name: "forward artifact reference", edit: func(req *CreateManagedExecutionWorkflowRequest) {
			req.Steps[0].Request.OutputFiles = []string{"patch.diff"}
			req.Steps[1].ArtifactInputs = []ManagedExecutionWorkflowArtifactInput{{FromStep: "summarize", Name: "patch.diff", Path: "input/patch.diff"}}
		}},
		{name: "invalid artifact destination", edit: func(req *CreateManagedExecutionWorkflowRequest) {
			req.Steps[1].ArtifactInputs = []ManagedExecutionWorkflowArtifactInput{{FromStep: "inspect", Name: "patch.diff", Path: "../patch.diff"}}
		}},
		{name: "too much parallelism", edit: func(req *CreateManagedExecutionWorkflowRequest) {
			req.MaxParallelSteps = ExecutionWorkflowManagedMaxParallel + 1
		}},
		{name: "unknown failure policy", edit: func(req *CreateManagedExecutionWorkflowRequest) { req.FailurePolicy = "ignore_failures" }},
		{name: "forward dependency", edit: func(req *CreateManagedExecutionWorkflowRequest) { req.Steps[0].DependsOn = []string{"summarize"} }},
		{name: "duplicate dependency", edit: func(req *CreateManagedExecutionWorkflowRequest) {
			req.Steps[1].DependsOn = []string{"inspect", "inspect"}
		}},
		{name: "dependency results without dependency", edit: func(req *CreateManagedExecutionWorkflowRequest) { req.Steps[0].IncludeDependencyResults = true }},
		{name: "dependency results with explicit input", edit: func(req *CreateManagedExecutionWorkflowRequest) {
			req.Steps[1].IncludeDependencyResults = true
			req.Steps[1].Request.Input = []byte(`{"fixed":true}`)
		}},
		{name: "duplicate label", edit: func(req *CreateManagedExecutionWorkflowRequest) { req.Steps[1].Label = req.Steps[0].Label }},
		{name: "caller metadata", edit: func(req *CreateManagedExecutionWorkflowRequest) { req.Steps[0].Request.WorkflowID = "other" }},
		{name: "too many steps", edit: func(req *CreateManagedExecutionWorkflowRequest) {
			req.Steps = make([]CreateManagedExecutionWorkflowStep, ExecutionWorkflowManagedMaxSteps+1)
		}},
		{name: "control character", edit: func(req *CreateManagedExecutionWorkflowRequest) { req.Steps[0].Label = "bad\nlabel" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copy := valid
			copy.Steps = append([]CreateManagedExecutionWorkflowStep(nil), valid.Steps...)
			test.edit(&copy)
			if err := ValidateCreateManagedExecutionWorkflow(copy); err == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
}

func TestManagedWorkflowResultSchemaSizeAndJSONValidation(t *testing.T) {
	request := CreateManagedExecutionWorkflowRequest{
		WorkflowID: "schema-check", Version: "v1",
		Steps: []CreateManagedExecutionWorkflowStep{
			{Label: "collect", ResultSchema: json.RawMessage(`{"type":"object","required":["items"]}`)},
		},
	}
	if err := ValidateCreateManagedExecutionWorkflow(request); err != nil {
		t.Fatalf("valid result schema rejected: %v", err)
	}

	invalidJSON := request
	invalidJSON.Steps = append([]CreateManagedExecutionWorkflowStep(nil), request.Steps...)
	invalidJSON.Steps[0].ResultSchema = json.RawMessage(`{"type":`)
	if err := ValidateCreateManagedExecutionWorkflow(invalidJSON); err == nil {
		t.Fatal("invalid result schema JSON accepted")
	}

	oversized := request
	oversized.Steps = append([]CreateManagedExecutionWorkflowStep(nil), request.Steps...)
	oversized.Steps[0].ResultSchema = json.RawMessage("true" + strings.Repeat(" ", ExecutionWorkflowResultSchemaMaxBytes))
	if err := ValidateCreateManagedExecutionWorkflow(oversized); err == nil {
		t.Fatal("oversized result schema accepted")
	}
}

func TestManagedExecutionWorkflowPlanBound(t *testing.T) {
	request := CreateManagedExecutionWorkflowRequest{WorkflowID: "x", Version: "v1", Steps: []CreateManagedExecutionWorkflowStep{{Label: "step"}}}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > ExecutionWorkflowManagedPlanMaxBytes || !strings.Contains(string(encoded), "workflow_id") {
		t.Fatalf("unexpected encoded request: %d bytes", len(encoded))
	}
}
