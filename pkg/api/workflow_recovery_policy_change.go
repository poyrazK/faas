package api

import (
	"context"
	"net/http"
	"time"
)

type WorkflowRecoveryPolicyChangeEntry struct {
	Entry  WorkflowRecoveryAutomaticQueueEntry `json:"entry"`
	Effect string                              `json:"effect"`
}
type WorkflowRecoveryPolicyChangeCandidate struct {
	JobID    string                         `json:"job_id"`
	Decision WorkflowRecoveryPolicyDecision `json:"decision"`
}
type WorkflowRecoveryPolicyChangePreview struct {
	InitialFailures            *WorkflowRecoveryInitialPolicyChangePreview `json:"initial_failures,omitempty"`
	Current                    WorkflowRecoveryPolicyResponse              `json:"current"`
	Proposed                   WorkflowRecoveryPolicy                      `json:"proposed"`
	ObservedAt                 time.Time                                   `json:"observed_at"`
	NextVersion                int64                                       `json:"next_version"`
	PendingSuppressed          int                                         `json:"pending_suppressed"`
	OutstandingRetained        int                                         `json:"outstanding_retained"`
	Entries                    []WorkflowRecoveryPolicyChangeEntry         `json:"entries"`
	EntriesTruncated           bool                                        `json:"entries_truncated"`
	AutomaticExecutionEnabled  bool                                        `json:"automatic_execution_enabled"`
	HourlyExecutions           int                                         `json:"hourly_executions"`
	RemainingExecutionBudget   int                                         `json:"remaining_execution_budget"`
	ExecutionCooldownUntil     *time.Time                                  `json:"execution_cooldown_until,omitempty"`
	HourlyPreparedActions      int                                         `json:"hourly_prepared_actions"`
	RemainingPreparationBudget int                                         `json:"remaining_preparation_budget"`
	PreparationCooldownUntil   *time.Time                                  `json:"preparation_cooldown_until,omitempty"`
	InspectedJobs              int                                         `json:"inspected_jobs"`
	JobsTruncated              bool                                        `json:"jobs_truncated"`
	Candidates                 []WorkflowRecoveryPolicyChangeCandidate     `json:"candidates"`
	CandidatesTruncated        bool                                        `json:"candidates_truncated"`
}

func (c *Client) PreviewWorkflowRecoveryPolicyChange(ctx context.Context, app string, req SetWorkflowRecoveryPolicyRequest) (WorkflowRecoveryPolicyChangePreview, error) {
	var out WorkflowRecoveryPolicyChangePreview
	if err := req.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, recoveryPolicyPath(app)+"/change-preview", req, &out)
	return out, err
}
