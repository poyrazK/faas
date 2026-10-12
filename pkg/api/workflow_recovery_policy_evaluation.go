package api

import (
	"context"
	"net/http"
	"time"
)

type WorkflowRecoveryPolicyEvaluationRequest struct {
	ExpectedVersion int64                  `json:"expected_version"`
	Policy          WorkflowRecoveryPolicy `json:"policy"`
	Workflow        string                 `json:"workflow,omitempty"`
	CreatedAfter    time.Time              `json:"created_after,omitempty"`
	CreatedBefore   time.Time              `json:"created_before,omitempty"`
}

func (r WorkflowRecoveryPolicyEvaluationRequest) Validate() error {
	if err := (SetWorkflowRecoveryPolicyRequest{ExpectedVersion: r.ExpectedVersion, Policy: r.Policy}).Validate(); err != nil {
		return err
	}
	return (WorkflowRecoveryHistoryOptions{Workflow: r.Workflow, CreatedAfter: r.CreatedAfter, CreatedBefore: r.CreatedBefore}).Validate()
}

type WorkflowRecoveryPolicyAssessment struct {
	Eligibility       string `json:"eligibility"`
	Reason            string `json:"reason"`
	Preparation       string `json:"preparation"`
	ExecutionPressure string `json:"execution_pressure"`
}
type WorkflowRecoveryPolicyEvaluationCandidate struct {
	LaterEligibility          *WorkflowRecoveryFailureEligibilityAssessment `json:"later_eligibility,omitempty"`
	EvidenceSource            string                                        `json:"evidence_source"`
	FailureEvidenceID         string                                        `json:"failure_evidence_id,omitempty"`
	CandidateEvidenceID       string                                        `json:"candidate_evidence_id,omitempty"`
	ReviewID                  string                                        `json:"review_id,omitempty"`
	JobID                     string                                        `json:"job_id,omitempty"`
	RunID                     string                                        `json:"run_id"`
	Workflow                  string                                        `json:"workflow"`
	ObservedAt                time.Time                                     `json:"observed_at"`
	PolicyVersion             int64                                         `json:"policy_version,omitempty"`
	Position                  int64                                         `json:"position,omitempty"`
	ResumeCount               int                                           `json:"resume_count"`
	Trigger                   string                                        `json:"trigger"`
	RecordedOutcome           string                                        `json:"recorded_outcome"`
	ObservedHourlyExecutions  int                                           `json:"observed_hourly_executions"`
	LastObservedExecutionAt   *time.Time                                    `json:"last_observed_execution_at,omitempty"`
	ExecutionEvidenceIDs      []string                                      `json:"execution_evidence_ids"`
	HistoricalFailureStatuses []string                                      `json:"historical_failure_statuses"`
	Current                   WorkflowRecoveryPolicyAssessment              `json:"current"`
	Proposed                  WorkflowRecoveryPolicyAssessment              `json:"proposed"`
}
type WorkflowRecoveryPolicyEvaluationTotals struct {
	Candidates                int `json:"candidates"`
	WouldPrepare              int `json:"would_prepare"`
	Excluded                  int `json:"excluded"`
	Unknown                   int `json:"unknown"`
	Cooldown                  int `json:"cooldown"`
	PreparationBudget         int `json:"preparation_budget"`
	Deduplicated              int `json:"deduplicated"`
	ExecutionBudgetPressure   int `json:"execution_budget_pressure"`
	ExecutionCooldownPressure int `json:"execution_cooldown_pressure"`
}
type WorkflowRecoveryFailureCoverage struct {
	CaptureStartedAt           time.Time `json:"capture_started_at"`
	Coverage                   string    `json:"coverage"`
	CapturedFailures           int       `json:"captured_failures"`
	ReviewedFailures           int       `json:"reviewed_failures"`
	UnreviewedFailures         int       `json:"unreviewed_failures"`
	MissingEligibilityEvidence int       `json:"missing_eligibility_evidence"`
	Truncated                  bool      `json:"truncated"`
}

type WorkflowRecoveryPolicyEvaluation struct {
	FailureCoverage     WorkflowRecoveryFailureCoverage             `json:"failure_coverage"`
	Current             WorkflowRecoveryPolicyResponse              `json:"current"`
	Proposed            WorkflowRecoveryPolicy                      `json:"proposed"`
	ObservedAt          time.Time                                   `json:"observed_at"`
	CreatedAfter        time.Time                                   `json:"created_after"`
	CreatedBefore       time.Time                                   `json:"created_before"`
	InspectedReviews    int                                         `json:"inspected_reviews"`
	ReviewsTruncated    bool                                        `json:"reviews_truncated"`
	CandidatesTruncated bool                                        `json:"candidates_truncated"`
	ExecutionsTruncated bool                                        `json:"executions_truncated"`
	ObservedExecutions  int                                         `json:"observed_executions"`
	CurrentTotals       WorkflowRecoveryPolicyEvaluationTotals      `json:"current_totals"`
	ProposedTotals      WorkflowRecoveryPolicyEvaluationTotals      `json:"proposed_totals"`
	Candidates          []WorkflowRecoveryPolicyEvaluationCandidate `json:"candidates"`
	CoverageNotes       []string                                    `json:"coverage_notes"`
}

func (c *Client) EvaluateWorkflowRecoveryPolicy(ctx context.Context, app string, r WorkflowRecoveryPolicyEvaluationRequest) (WorkflowRecoveryPolicyEvaluation, error) {
	var out WorkflowRecoveryPolicyEvaluation
	if err := r.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, recoveryPolicyPath(app)+"/evaluate", r, &out)
	return out, err
}

// WorkflowRecoveryFailureEligibility records checks of retained state after a failure.
// It is not proof of eligibility at failure time or a guarantee of current admission.
type WorkflowRecoveryFailureEligibility struct {
	ObservedAt     time.Time `json:"observed_at"`
	ParentJobID    string    `json:"parent_job_id,omitempty"`
	ParentPosition int64     `json:"parent_position,omitempty"`
	Reason         string    `json:"reason"`
	Blockers       []string  `json:"blockers"`
}
type WorkflowRecoveryFailureEligibilityAssessment struct {
	Observation WorkflowRecoveryFailureEligibility `json:"observation"`
	Freshness   string                             `json:"freshness"`
	Current     WorkflowRecoveryPolicyAssessment   `json:"current"`
	Proposed    WorkflowRecoveryPolicyAssessment   `json:"proposed"`
}
