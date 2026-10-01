package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

var (
	ErrApplicationStandardOperationInProgress = errors.New("state: application standard assignment has an unfinished operation")
	ErrApplicationStandardReviewBusy          = errors.New("state: application standard review inputs are being modified")
)

// Approval is private until runtime admission and materialization are wired.
// It changes the admission pointer, not the adoption of an existing service.
type ApplicationStandardOperationStore interface {
	ApproveApplicationStandardReview(context.Context, string, string, string, string) (ApplicationStandardOperation, error)
	GetApplicationStandardOperation(context.Context, string, string) (ApplicationStandardOperation, error)
}

type ApplicationStandardOperation struct {
	ID           string                               `json:"id"`
	OrgID        string                               `json:"org_id"`
	PlanID       string                               `json:"plan_id"`
	AssignmentID string                               `json:"assignment_id"`
	ApprovalHash string                               `json:"approval_hash"`
	ApprovedBy   string                               `json:"approved_by"`
	BatchSize    int                                  `json:"batch_size"`
	State        string                               `json:"state"`
	ErrorCode    string                               `json:"error_code,omitempty"`
	Targets      []ApplicationStandardOperationTarget `json:"targets"`
	CreatedAt    time.Time                            `json:"created_at"`
	UpdatedAt    time.Time                            `json:"updated_at"`
}

type ApplicationStandardOperationTarget struct {
	AppID           string                         `json:"app_id"`
	Position        int                            `json:"position"`
	ApprovedApp     ApplicationStandardReviewedApp `json:"approved_app"`
	State           string                         `json:"state"`
	DesiredRevision int64                          `json:"desired_revision"`
	ErrorCode       string                         `json:"error_code,omitempty"`
	UpdatedAt       time.Time                      `json:"updated_at"`
	approvalInput   appstandards.ApplicationApprovalInput
}

// The private frozen proof accompanies the proposed projection so a worker
// can reject a changed application before installing the approved settings.
type standardOperationTargetBody struct {
	Application ApplicationStandardReviewedApp        `json:"application"`
	Input       appstandards.ApplicationApprovalInput `json:"input"`
}

func standardOperationActive(state string) bool {
	return state == "queued" || state == "running" || state == "waiting" || state == "paused"
}

func standardApprovalIdentityValid(orgID, actorID, planID string) bool {
	return validStandardResourceRead(orgID, actorID) && validStandardResourceRead(orgID, planID)
}

func authorizeStandardApproval(s standardReviewSnapshot) error {
	if s.DeletedPending {
		return ErrNotFound
	}
	if !s.ActorAuthorized {
		return ErrApplicationStandardReviewForbidden
	}
	if s.OrgStatus != string(OrgStatusActive) {
		return ErrConflict
	}
	return nil
}

func newStandardOperation(p ApplicationStandardReviewPlan, actorID string, now time.Time) (ApplicationStandardOperation, error) {
	now = now.UTC().Truncate(time.Microsecond) // Match persisted PostgreSQL timestamps on retries.
	o := ApplicationStandardOperation{ID: uuid.NewString(), OrgID: p.OrgID, PlanID: p.ID, AssignmentID: p.Request.AssignmentID, ApprovalHash: p.ApprovalHash, ApprovedBy: canonicalStandardUUID(actorID), BatchSize: p.Request.BatchSize, State: "queued", Targets: []ApplicationStandardOperationTarget{}, CreatedAt: now, UpdatedAt: now}
	inputs := map[string]appstandards.ApplicationApprovalInput{}
	for _, input := range p.approvalInputs.Applications {
		inputs[input.AppID] = input
	}
	for position, app := range p.Applications {
		input, exists := inputs[app.AppID]
		if !exists {
			return o, fmt.Errorf("approved application has no input proof")
		}
		hash, err := standardReviewDigest(app.Effective)
		if err != nil || hash != input.EffectiveHash {
			return o, fmt.Errorf("approved application projection differs from its proof")
		}
		o.Targets = append(o.Targets, ApplicationStandardOperationTarget{AppID: app.AppID, Position: position, ApprovedApp: app, State: "queued", UpdatedAt: now, approvalInput: input})
	}
	if len(inputs) != len(o.Targets) {
		return o, fmt.Errorf("approved application membership differs from its proof")
	}
	return o, nil
}

func cloneStandardOperation(in ApplicationStandardOperation) ApplicationStandardOperation {
	raw, _ := json.Marshal(in)
	var out ApplicationStandardOperation
	_ = json.Unmarshal(raw, &out)
	for i := range in.Targets {
		proof, _ := json.Marshal(in.Targets[i].approvalInput)
		_ = json.Unmarshal(proof, &out.Targets[i].approvalInput)
	}
	return out
}

func standardApprovalAudit(o ApplicationStandardOperation) AuditLog {
	// The approving identity is retained as an opaque UUID in the operation
	// and data. Do not capture contact information or destination credentials.
	data, _ := json.Marshal(map[string]any{"org_id": o.OrgID, "operation_id": o.ID, "plan_id": o.PlanID, "assignment_id": o.AssignmentID, "approval_hash": o.ApprovalHash, "approved_by": o.ApprovedBy, "affected_applications": len(o.Targets), "batch_size": o.BatchSize})
	return AuditLog{ID: uuid.New(), Kind: "application_standard.approved", ReceivedAt: o.CreatedAt, Data: data}
}
