package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type WorkflowRecoveryHistoryOptions struct {
	Workflow      string
	RunID         string
	PolicyVersion int64
	CreatedAfter  time.Time
	CreatedBefore time.Time
	After         string
	Limit         int
}

func (o WorkflowRecoveryHistoryOptions) Validate() error {
	if o.Limit < 0 || o.Limit > EventRecoveryHistoryPageMax || o.PolicyVersion < 0 || len(o.After) > WorkflowRecoveryHistoryCursorMaxBytes {
		return fmt.Errorf("invalid history pagination or policy version")
	}
	if o.Workflow != "" && (strings.TrimSpace(o.Workflow) != o.Workflow || len(o.Workflow) > AutomationNameMaxBytes || strings.ContainsAny(o.Workflow, "/\\\x00")) {
		return fmt.Errorf("invalid history workflow")
	}
	if o.RunID != "" {
		if _, err := uuid.Parse(o.RunID); err != nil {
			return fmt.Errorf("invalid history run id: %w", err)
		}
	}
	if !o.CreatedAfter.IsZero() && !o.CreatedBefore.IsZero() && (o.CreatedAfter.After(o.CreatedBefore) || o.CreatedBefore.Sub(o.CreatedAfter) > WorkflowAutomationHealthMaxRange) {
		return fmt.Errorf("history time window must be ordered and at most 30 days")
	}
	return nil
}

type WorkflowRecoveryCandidateEvidence struct {
	ReviewID      string                           `json:"review_id"`
	AppID         string                           `json:"app_id"`
	JobID         string                           `json:"job_id"`
	PolicyVersion int64                            `json:"policy_version"`
	Policy        WorkflowRecoveryPolicy           `json:"policy"`
	ObservedAt    time.Time                        `json:"observed_at"`
	Decisions     []WorkflowRecoveryPolicyDecision `json:"decisions"`
}

type WorkflowRecoveryHistoryEntry struct {
	PollingNotification *WorkflowRecoveryPollingNotification `json:"polling_notification,omitempty"`
	InitialFailureID    string                               `json:"initial_failure_id,omitempty"`
	EvidenceKind        string                               `json:"evidence_kind,omitempty"`
	ResumeCount         *int                                 `json:"resume_count,omitempty"`
	RequestID           string                               `json:"request_id,omitempty"`
	NotificationEventID string                               `json:"notification_event_id,omitempty"`
	DiagnosisReason     string                               `json:"diagnosis_reason,omitempty"`
	ReviewURL           string                               `json:"review_url,omitempty"`
	EvidenceURL         string                               `json:"evidence_url,omitempty"`
	JobURL              string                               `json:"job_url,omitempty"`
	CandidateEvidence   *WorkflowRecoveryCandidateEvidence   `json:"candidate_evidence,omitempty"`
	EvaluationReason    string                               `json:"evaluation_reason,omitempty"`
	Durable             bool                                 `json:"durable,omitempty"`
	EpisodeID           string                               `json:"episode_id,omitempty"`
	Policy              *WorkflowRecoveryPolicy              `json:"policy,omitempty"`
	ID                  string                               `json:"id"`
	OccurredAt          time.Time                            `json:"occurred_at"`
	Kind                string                               `json:"kind"`
	Outcome             string                               `json:"outcome"`
	Reason              string                               `json:"reason"`
	PolicyVersion       int64                                `json:"policy_version,omitempty"`
	Workflow            string                               `json:"workflow,omitempty"`
	RunID               string                               `json:"run_id,omitempty"`
	ReviewID            string                               `json:"review_id,omitempty"`
	ActionID            string                               `json:"action_id,omitempty"`
	JobID               string                               `json:"job_id,omitempty"`
	ChildJobID          string                               `json:"child_job_id,omitempty"`
	ActorKind           string                               `json:"actor_kind,omitempty"`
	ActorID             string                               `json:"actor_id,omitempty"`
}
type WorkflowRecoveryHistory struct {
	ObservedAt           time.Time                      `json:"observed_at"`
	CreatedAfter         time.Time                      `json:"created_after"`
	CreatedBefore        time.Time                      `json:"created_before"`
	Entries              []WorkflowRecoveryHistoryEntry `json:"entries"`
	NextAfter            string                         `json:"next_after,omitempty"`
	RetainedObservations bool                           `json:"retained_observations"`
}

func (c *Client) GetWorkflowRecoveryHistory(ctx context.Context, app string, o WorkflowRecoveryHistoryOptions) (WorkflowRecoveryHistory, error) {
	var out WorkflowRecoveryHistory
	if err := o.Validate(); err != nil {
		return out, err
	}
	q := url.Values{}
	if o.Workflow != "" {
		q.Set("workflow", o.Workflow)
	}
	if o.RunID != "" {
		q.Set("run_id", o.RunID)
	}
	if o.PolicyVersion != 0 {
		q.Set("policy_version", strconv.FormatInt(o.PolicyVersion, 10))
	}
	if o.Limit != 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.After != "" {
		q.Set("after", o.After)
	}
	if !o.CreatedAfter.IsZero() {
		q.Set("created_after", o.CreatedAfter.Format(time.RFC3339Nano))
	}
	if !o.CreatedBefore.IsZero() {
		q.Set("created_before", o.CreatedBefore.Format(time.RFC3339Nano))
	}
	err := c.do(ctx, http.MethodGet, recoveryPolicyPath(app)+"/history?"+q.Encode(), nil, &out)
	return out, err
}
