package api

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"time"
)

type WorkflowInitialEvidenceRepairRequest struct {
	RequestID string `json:"request_id"`
	Reason    string `json:"reason"`
}

func (r WorkflowInitialEvidenceRepairRequest) Validate() error {
	id, err := uuid.Parse(r.RequestID)
	if err != nil || id == uuid.Nil || id.String() != r.RequestID {
		return fmt.Errorf("request_id must be a canonical nonzero UUID")
	}
	if strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("evidence repair requires a reason")
	}
	return (EventRecoveryControlRequest{Reason: r.Reason}).Validate()
}

type WorkflowInitialEvidenceRepairReceipt struct {
	RequestID  string    `json:"request_id"`
	Reason     string    `json:"reason"`
	ActorKind  string    `json:"actor_kind"`
	ActorID    string    `json:"actor_id"`
	RepairedAt time.Time `json:"repaired_at"`
	Outcome    string    `json:"outcome"`
}
type WorkflowInitialEvidenceDiagnosis struct {
	ReviewID    string                                `json:"review_id"`
	JobID       string                                `json:"job_id,omitempty"`
	RunID       string                                `json:"run_id"`
	ResumeCount int                                   `json:"resume_count"`
	ObservedAt  time.Time                             `json:"observed_at"`
	Reason      string                                `json:"reason"`
	Repairable  bool                                  `json:"repairable"`
	Execution   *EventRecoveryExecution               `json:"execution,omitempty"`
	Repair      *WorkflowInitialEvidenceRepairReceipt `json:"repair,omitempty"`
}

func (c *Client) DiagnoseWorkflowInitialEvidence(ctx context.Context, app, review string) (WorkflowInitialEvidenceDiagnosis, error) {
	var out WorkflowInitialEvidenceDiagnosis
	err := c.do(ctx, http.MethodGet, initialReviewPath(app, review)+"/evidence", nil, &out)
	return out, err
}
func (c *Client) RepairWorkflowInitialEvidence(ctx context.Context, app, review string, req WorkflowInitialEvidenceRepairRequest) (WorkflowInitialEvidenceDiagnosis, error) {
	var out WorkflowInitialEvidenceDiagnosis
	if err := req.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, initialReviewPath(app, review)+"/evidence/repair", req, &out)
	return out, err
}
