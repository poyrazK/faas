package api

import (
	"github.com/google/uuid"
	"testing"
)

func TestWorkflowRunRecoveryValidation(t *testing.T) {
	for _, name := range []string{"valid", "missing_key", "wrong_mode", "filter", "duplicate", "count", "receipt_protection", "mixed", "missing_plan"} {
		t.Run(name, func(t *testing.T) {
			r := EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &WorkflowRunRecoveryPlan{Name: "recover", Runs: []WorkflowRunRecoveryTarget{{RunID: uuid.NewString()}}}}
			switch name {
			case "missing_key":
				r.RequestID = ""
			case "wrong_mode":
				r.Mode = "execution"
			case "filter":
				r.EventSource = "orders"
			case "duplicate":
				r.WorkflowRuns.Runs = append(r.WorkflowRuns.Runs, r.WorkflowRuns.Runs[0])
			case "count":
				r.WorkflowRuns.Runs[0].ExpectedResumeCount = -1
			case "receipt_protection":
				r.ProtectReceipts = true
			case "mixed":
				r.WorkflowRouting = &EventWorkflowRecoveryPlan{}
			case "missing_plan":
				r.Mode = "workflow"
				r.WorkflowRuns = nil
			}
			if err := r.Validate(); (err == nil) != (name == "valid") {
				t.Fatalf("validation=%v", err)
			}
		})
	}
}

func TestWorkflowRunRecoveryRetryValidation(t *testing.T) {
	for _, scenario := range []string{"valid", "missing_position", "missing_parent", "zero_count", "duplicate_position"} {
		t.Run(scenario, func(t *testing.T) {
			r := EventRecoveryRequest{RequestID: uuid.NewString(), ParentJobID: uuid.NewString(), WorkflowRuns: &WorkflowRunRecoveryPlan{Name: "recover", Runs: []WorkflowRunRecoveryTarget{{RunID: uuid.NewString(), ExpectedResumeCount: 1, ParentPosition: 1}}}}
			switch scenario {
			case "missing_position":
				r.WorkflowRuns.Runs[0].ParentPosition = 0
			case "missing_parent":
				r.ParentJobID = ""
			case "zero_count":
				r.WorkflowRuns.Runs[0].ExpectedResumeCount = 0
			case "duplicate_position":
				r.WorkflowRuns.Runs = append(r.WorkflowRuns.Runs, WorkflowRunRecoveryTarget{RunID: uuid.NewString(), ExpectedResumeCount: 1, ParentPosition: 1})
			}
			if err := r.Validate(); (err == nil) != (scenario == "valid") {
				t.Fatalf("validation=%v", err)
			}
		})
	}
}

func TestWorkflowRunRecoveryExecutionPolicyValidation(t *testing.T) {
	for _, seconds := range []int{-1, 0, 1, 59, 60, 900, 604800, 604801} {
		for _, deadline := range []bool{true, false} {
			r := EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &WorkflowRunRecoveryPlan{Name: "recover", Runs: []WorkflowRunRecoveryTarget{{RunID: uuid.NewString()}}}}
			if deadline {
				r.WorkflowRuns.ExecutionDeadlineSeconds = seconds
			} else {
				r.WorkflowRuns.ExecutionStallSeconds = seconds
			}
			valid := seconds == 0 || seconds >= 60 && seconds <= 604800
			if err := r.Validate(); (err == nil) != valid {
				t.Fatalf("seconds=%d deadline=%v err=%v", seconds, deadline, err)
			}
		}
	}
}
