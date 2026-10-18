package api

import "time"

// OperationDataRepair is the safe control-plane projection. Row values and
// database credentials stay in the application database/executor boundary.
type OperationDataRepair struct {
	SourceOperationID        string                     `json:"source_operation_id"`
	PlanID                   string                     `json:"plan_id"`
	PlanDigest               string                     `json:"plan_digest"`
	Phase                    string                     `json:"phase"`
	PhaseStartedAt           time.Time                  `json:"phase_started_at,omitzero"`
	Candidates               int                        `json:"candidates"`
	Conflicts                int                        `json:"conflicts"`
	Unavailable              int                        `json:"unavailable"`
	PlanExpiresAt            time.Time                  `json:"plan_expires_at"`
	Approval                 *OperationApprovalDecision `json:"approval,omitempty"`
	AppliedAt                *time.Time                 `json:"applied_at,omitempty"`
	RehearsalTaskID          string                     `json:"rehearsal_task_id,omitempty"`
	RehearsalStatus          string                     `json:"rehearsal_status,omitempty"`
	RehearsalCancelRequested bool                       `json:"rehearsal_cancel_requested,omitempty"`
	ReplacesPlanID           string                     `json:"replaces_plan_id,omitempty"`
	ReplacesPlanDigest       string                     `json:"replaces_plan_digest,omitempty"`
	ReplacementOperationID   string                     `json:"replacement_operation_id,omitempty"`
	ApplyClosedAt            *time.Time                 `json:"apply_closed_at,omitempty"`
}

const (
	DataRepairPrepared         = "prepared"
	DataRepairRehearsing       = "rehearsing"
	DataRepairAwaitingApproval = "awaiting_approval"
	DataRepairApproved         = "approved"
	DataRepairApplying         = "applying"
	DataRepairUnknown          = "outcome_unknown"
	DataRepairSucceeded        = "succeeded"
	DataRepairFailed           = "failed"
	DataRepairRejected         = "rejected"
	DataRepairReplacing        = "replacing"
	DataRepairReplaced         = "replaced"
	DataRepairResolving        = "resolving"
	DataRepairClosed           = "closed"
)
