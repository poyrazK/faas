package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
)

const (
	RepairRehearsalQueued     = "queued"
	RepairRehearsalRunning    = "running"
	RepairRehearsalCleanup    = "cleanup"
	RepairRehearsalPublishing = "publishing"
	RepairRehearsalSucceeded  = "succeeded"
	RepairRehearsalFailed     = "failed"
	RepairRehearsalCancelled  = "cancelled"
)

// DataRepairRehearsal never contains a snapshot, row values, check SQL, tokens,
// or credentials. Expiry permits cleanup only; it never permits VM replay.
type DataRepairRehearsal struct {
	ID              string               `json:"id"`
	OperationID     string               `json:"operation_id"`
	AccountID       string               `json:"account_id"`
	Scope           core.Scope           `json:"scope"`
	Binding         string               `json:"binding"`
	PlanID          string               `json:"plan_id"`
	PlanDigest      string               `json:"plan_digest"`
	ChecksSHA256    string               `json:"checks_sha256"`
	PlanExpiresAt   time.Time            `json:"plan_expires_at"`
	Phase           string               `json:"phase"`
	CancelRequested bool                 `json:"cancel_requested"`
	Frame           *core.RehearsalFrame `json:"frame,omitempty"`
	Evidence        *core.Evidence       `json:"evidence,omitempty"`
	FailureCode     string               `json:"failure_code,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
	PhaseStartedAt  time.Time            `json:"phase_started_at,omitzero"`
}

type DataRepairRehearsalStore interface {
	EnqueueDataRepairRehearsal(context.Context, string, string, int64, string) (Operation, DataRepairRehearsal, error)
	DataRepairRehearsal(context.Context, string) (DataRepairRehearsal, error)
	ListDataRepairRehearsals(context.Context, bool, string, string, int) ([]DataRepairRehearsal, error)
	ClaimDataRepairRehearsal(context.Context, string, core.RehearsalFrame) (DataRepairRehearsal, error)
	DispatchDataRepairRehearsal(context.Context, string, string) (DataRepairRehearsal, error)
	MarkDataRepairRehearsalCleanup(context.Context, string) (DataRepairRehearsal, error)
	RetireDataRepairRehearsal(context.Context, string, *core.Evidence) (DataRepairRehearsal, error)
	CompleteDataRepairRehearsal(context.Context, string) (Operation, error)
	CancelDataRepairRehearsal(context.Context, string, string, int64) (Operation, error)
}

func rehearsalFromJSON(b []byte) (DataRepairRehearsal, error) {
	var t DataRepairRehearsal
	if json.Unmarshal(b, &t) != nil || t.ID == "" || t.OperationID == "" {
		return t, ErrConflict
	}
	return t, nil
}

func (t DataRepairRehearsal) Matches(f core.RehearsalFrame) bool {
	return f.TaskID == t.ID && f.OperationID == t.OperationID && f.PlanID == t.PlanID && f.PlanDigest == t.PlanDigest && f.Scope == t.Scope && f.Binding == t.Binding && f.ChecksSHA256 == t.ChecksSHA256 && f.PlanExpiresAt.Equal(t.PlanExpiresAt)
}

func (t DataRepairRehearsal) Terminal() bool {
	return t.Phase == RepairRehearsalSucceeded || t.Phase == RepairRehearsalFailed || t.Phase == RepairRehearsalCancelled
}

func rehearsalOperationEvent(op *Operation, t DataRepairRehearsal, now time.Time) api.OperationEvent {
	op.DataRepair.RehearsalTaskID = t.ID
	op.DataRepair.RehearsalStatus = t.Phase
	op.DataRepair.RehearsalCancelRequested = t.CancelRequested
	return dataRepairEvent(op, "progress", now)
}
