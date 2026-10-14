package api

import (
	"fmt"
	"github.com/google/uuid"
	"strings"
)

type WorkflowRunRecoveryPlan struct {
	ExecutionDeadlineSeconds int                         `json:"execution_deadline_seconds,omitempty"`
	ExecutionStallSeconds    int                         `json:"execution_stall_seconds,omitempty"`
	Name                     string                      `json:"name"`
	Runs                     []WorkflowRunRecoveryTarget `json:"runs"`
}
type WorkflowRunRecoveryTarget struct {
	FailureEvidenceID   string `json:"failure_evidence_id,omitempty"`
	ParentPosition      int64  `json:"parent_position,omitempty"`
	RunID               string `json:"run_id"`
	ExpectedResumeCount int    `json:"expected_resume_count"`
}

func (r EventRecoveryRequest) validateWorkflowRuns() error {
	p := r.WorkflowRuns
	if r.WorkflowRouting != nil || r.ProtectReceipts || r.Mode != "" && r.Mode != "workflow" || r.Outcome != "" || r.SubscriptionID != "" || r.EventSource != "" || r.EventType != "" || r.FailureCode != "" || r.MinAgeSeconds != 0 || r.IncludeNonRetryable {
		return fmt.Errorf("workflow_runs cannot be combined with event recovery selections or receipt protection")
	}
	if r.RequestID == "" || strings.TrimSpace(p.Name) == "" || len(p.Name) > EventBacklogFilterMaxBytes || len(p.Runs) < 1 || len(p.Runs) > AutomationRecoveryBatchMaxRuns {
		return fmt.Errorf("workflow_runs requires name, request_id and 1..%d runs", AutomationRecoveryBatchMaxRuns)
	}
	for _, seconds := range []int{p.ExecutionDeadlineSeconds, p.ExecutionStallSeconds} {
		if seconds != 0 && (seconds < WorkflowRecoveryExecutionMonitorMinSeconds || seconds > WorkflowRecoveryExecutionMonitorMaxSeconds) {
			return fmt.Errorf("execution monitoring duration must be zero or %d..%d seconds", WorkflowRecoveryExecutionMonitorMinSeconds, WorkflowRecoveryExecutionMonitorMaxSeconds)
		}
	}
	seen := map[string]bool{}
	positions := map[int64]bool{}
	for _, t := range p.Runs {
		id, err := uuid.Parse(t.RunID)
		if err != nil || id.String() != t.RunID || seen[t.RunID] || t.ExpectedResumeCount < 0 || t.ExpectedResumeCount > WorkflowRunMaxResumes {
			return fmt.Errorf("invalid or duplicate workflow run recovery target")
		}
		if t.FailureEvidenceID != "" {
			evidence, err := uuid.Parse(t.FailureEvidenceID)
			if err != nil || evidence.String() != t.FailureEvidenceID || t.ExpectedResumeCount != 0 || r.ParentJobID != "" {
				return fmt.Errorf("failure evidence requires an initial run target without a recovery parent")
			}
		}
		if t.ParentPosition < 0 || (r.ParentJobID == "") != (t.ParentPosition == 0) || t.ParentPosition > AutomationRecoveryBatchMaxRuns || positions[t.ParentPosition] && t.ParentPosition != 0 || r.ParentJobID != "" && t.ExpectedResumeCount < 1 {
			return fmt.Errorf("workflow retry targets require unique parent positions and an admitted resume count")
		}
		positions[t.ParentPosition] = true
		seen[t.RunID] = true
	}
	return nil
}
