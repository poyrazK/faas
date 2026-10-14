package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type WorkflowInitialRecoveryReviewRequest struct {
	ExpectedPolicyVersion int64 `json:"expected_policy_version"`
}

func (r WorkflowInitialRecoveryReviewRequest) Validate() error {
	if r.ExpectedPolicyVersion < 1 {
		return fmt.Errorf("expected_policy_version must be positive")
	}
	return nil
}

type WorkflowInitialRecoveryReviewDecisionRequest struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

func (r WorkflowInitialRecoveryReviewDecisionRequest) Validate() error {
	if r.Decision != "submit" && r.Decision != "cancel" {
		return fmt.Errorf("decision must be submit or cancel")
	}
	if strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("initial review decision requires a reason")
	}
	return (EventRecoveryControlRequest{Reason: r.Reason}).Validate()
}

type WorkflowInitialRecoveryReviewEvent struct {
	Status     string    `json:"status"`
	Reason     string    `json:"reason"`
	OccurredAt time.Time `json:"occurred_at"`
	ActorKind  string    `json:"actor_kind"`
	ActorID    string    `json:"actor_id"`
}
type WorkflowInitialRecoveryAttention struct {
	Reason         string                                        `json:"reason"`
	BlockReason    string                                        `json:"block_reason,omitempty"`
	Sequence       int64                                         `json:"sequence"`
	ObservedAt     time.Time                                     `json:"observed_at"`
	Diagnosis      *WorkflowInitialEvidenceDiagnosis             `json:"diagnosis,omitempty"`
	OperatorAction string                                        `json:"operator_action,omitempty"`
	EvidenceURL    string                                        `json:"evidence_url,omitempty"`
	CLICommand     string                                        `json:"cli_command,omitempty"`
	Notification   *WorkflowRecoveryAutomaticBlockedNotification `json:"notification,omitempty"`
}

type WorkflowInitialObservationFailure struct {
	Code     string    `json:"code"`
	Attempts int64     `json:"attempts"`
	FailedAt time.Time `json:"failed_at"`
	RetryAt  time.Time `json:"retry_at"`
}

type WorkflowInitialRecoveryReview struct {
	PollingNotification        *WorkflowRecoveryPollingNotification          `json:"polling_notification,omitempty"`
	ObservationFailure         *WorkflowInitialObservationFailure            `json:"observation_failure,omitempty"`
	Attention                  *WorkflowInitialRecoveryAttention             `json:"attention,omitempty"`
	Result                     *WorkflowRecoveryReviewActionStatus           `json:"result,omitempty"`
	EvidenceRepair             *WorkflowInitialEvidenceRepairReceipt         `json:"evidence_repair,omitempty"`
	AutomaticBlockReason       string                                        `json:"automatic_block_reason,omitempty"`
	AutomaticBlockSequence     int64                                         `json:"automatic_block_sequence,omitempty"`
	AutomaticBlockNotification *WorkflowRecoveryAutomaticBlockedNotification `json:"automatic_block_notification,omitempty"`
	AutomaticResult            *WorkflowRecoveryReviewActionStatus           `json:"automatic_result,omitempty"`
	AutomaticEvaluation        *WorkflowRecoveryAutomaticEvaluation          `json:"automatic_evaluation,omitempty"`
	ID                         string                                        `json:"id"`
	AppID                      string                                        `json:"app_id"`
	App                        string                                        `json:"app"`
	Failure                    WorkflowInitialFailure                        `json:"failure"`
	Target                     WorkflowRunRecoveryTarget                     `json:"target"`
	PolicyVersion              int64                                         `json:"policy_version"`
	Policy                     WorkflowRecoveryPolicy                        `json:"policy"`
	Status                     string                                        `json:"status"`
	Reason                     string                                        `json:"reason"`
	PreparedAt                 time.Time                                     `json:"prepared_at"`
	ExpiresAt                  time.Time                                     `json:"expires_at"`
	ObservedAt                 time.Time                                     `json:"observed_at"`
	JobID                      string                                        `json:"job_id,omitempty"`
	SubmittedAt                *time.Time                                    `json:"submitted_at,omitempty"`
	Notification               *WorkflowRecoveryReviewNotification           `json:"notification,omitempty"`
	Events                     []WorkflowInitialRecoveryReviewEvent          `json:"events"`
}

func initialReviewPath(app, id string) string {
	return "/v1/apps/" + url.PathEscape(app) + "/initial-recovery-reviews/" + url.PathEscape(id)
}
func (c *Client) PrepareWorkflowInitialRecoveryReview(ctx context.Context, app, id string, req WorkflowInitialRecoveryReviewRequest) (WorkflowInitialRecoveryReview, error) {
	var out WorkflowInitialRecoveryReview
	if err := req.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(app)+"/workflow-failures/"+url.PathEscape(id)+"/recovery-reviews", req, &out)
	return out, err
}
func (c *Client) GetWorkflowInitialRecoveryReview(ctx context.Context, app, id string) (WorkflowInitialRecoveryReview, error) {
	var out WorkflowInitialRecoveryReview
	err := c.do(ctx, http.MethodGet, initialReviewPath(app, id), nil, &out)
	return out, err
}
func (c *Client) DecideWorkflowInitialRecoveryReview(ctx context.Context, app, id string, req WorkflowInitialRecoveryReviewDecisionRequest) (WorkflowInitialRecoveryReview, error) {
	var out WorkflowInitialRecoveryReview
	if err := req.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, initialReviewPath(app, id)+"/decision", req, &out)
	return out, err
}

type WorkflowInitialRecoveryReviews struct {
	Reviews   []WorkflowInitialRecoveryReview `json:"reviews"`
	NextAfter string                          `json:"next_after,omitempty"`
}

func (c *Client) ListWorkflowInitialRecoveryReviews(ctx context.Context, app, after string, limit int) (WorkflowInitialRecoveryReviews, error) {
	var out WorkflowInitialRecoveryReviews
	q := url.Values{"after": {after}, "limit": {fmt.Sprint(limit)}}
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/initial-recovery-reviews?"+q.Encode(), nil, &out)
	return out, err
}
