package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type WorkflowRetryEvidenceDiagnosis struct {
	EvidenceKind string                                `json:"evidence_kind,omitempty"`
	ItemPosition int64                                 `json:"item_position,omitempty"`
	ReviewID     string                                `json:"review_id"`
	ActionID     string                                `json:"action_id"`
	JobID        string                                `json:"job_id,omitempty"`
	RunID        string                                `json:"run_id"`
	ResumeCount  int                                   `json:"resume_count"`
	ObservedAt   time.Time                             `json:"observed_at"`
	Reason       string                                `json:"reason"`
	Repairable   bool                                  `json:"repairable"`
	Execution    *EventRecoveryExecution               `json:"execution,omitempty"`
	Repair       *WorkflowInitialEvidenceRepairReceipt `json:"repair,omitempty"`
}

func retryEvidencePath(app, review, action string) string {
	return recoveryPolicyPath(app) + "/reviews/" + url.PathEscape(review) + "/actions/" + url.PathEscape(action) + "/evidence"
}
func (c *Client) DiagnoseWorkflowRetryEvidence(ctx context.Context, app, review, action string) (WorkflowRetryEvidenceDiagnosis, error) {
	var out WorkflowRetryEvidenceDiagnosis
	err := c.do(ctx, http.MethodGet, retryEvidencePath(app, review, action), nil, &out)
	return out, err
}
func (c *Client) RepairWorkflowRetryEvidence(ctx context.Context, app, review, action string, req WorkflowInitialEvidenceRepairRequest) (WorkflowRetryEvidenceDiagnosis, error) {
	var out WorkflowRetryEvidenceDiagnosis
	if err := req.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, retryEvidencePath(app, review, action)+"/repair", req, &out)
	return out, err
}

func (c *Client) DiagnoseWorkflowRetryParentEvidence(ctx context.Context, app, review, action string) (WorkflowRetryEvidenceDiagnosis, error) {
	var out WorkflowRetryEvidenceDiagnosis
	err := c.do(ctx, http.MethodGet, retryEvidencePath(app, review, action)+"/parent", nil, &out)
	return out, err
}
func (c *Client) RepairWorkflowRetryParentEvidence(ctx context.Context, app, review, action string, req WorkflowInitialEvidenceRepairRequest) (WorkflowRetryEvidenceDiagnosis, error) {
	var out WorkflowRetryEvidenceDiagnosis
	if err := req.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, retryEvidencePath(app, review, action)+"/parent/repair", req, &out)
	return out, err
}
