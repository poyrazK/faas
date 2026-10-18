package state

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
)

// OperatorRestartStore is the scheduler/vmmd execution seam. Only schedd calls
// Prepare/Complete; vmmd alone writes Start/Acknowledge around physical teardown.
// The destroy token identifies an effect independently of a scheduler's claim.
type OperatorRestartStore interface {
	MigrationInstanceByID(context.Context, string) (Instance, error)
	GetOperatorRestart(context.Context, string) (OperatorRestartExecution, error)
	PrepareOperatorRestart(context.Context, string, string, OperatorRestartTarget) (OperatorRestartExecution, error)
	StartOperatorRestartDestroy(context.Context, string, string, string) (OperatorRestartExecution, bool, error)
	AcknowledgeOperatorRestartDestroy(context.Context, string, string) error
	CompleteOperatorRestart(context.Context, OperatorRestartCompletion) (OperatorRestartResult, error)
}

const (
	RestartPrepared       = "prepared"
	RestartStarted        = "started"
	RestartDestroyed      = "destroyed"
	RestartOutcomeUnknown = "outcome_unknown"
	RestartCompleted      = "completed"
	RestartFailed         = "failed"
)

var (
	ErrOperatorRestartClaimLost     = fmt.Errorf("operator restart claim lost: %w", ErrConflict)
	ErrOperatorRestartTargetChanged = fmt.Errorf("operator restart target changed: %w", ErrConflict)
)

// Target is immutable after preparation. Generation is vmmd's opaque physical
// incarnation, distinct from the SQL wake correlation ID. Snapshots are selected
// and invalidated in the same transaction that records this target.
type OperatorRestartTarget struct {
	InstanceID          string     `json:"instance_id"`
	AppID               string     `json:"app_id"`
	DeploymentID        string     `json:"deployment_id"`
	NodeID              string     `json:"node_id"`
	WakeID              string     `json:"wake_id"`
	Generation          string     `json:"generation"`
	SnapshotIDs         []string   `json:"snapshot_ids"`
	MigrationLeaseToken string     `json:"migration_lease_token,omitempty"`
	MigratedAt          *time.Time `json:"migrated_at,omitempty"`
}

type OperatorRestartExecution struct {
	IntentID     string
	Target       OperatorRestartTarget
	Phase        string
	DestroyToken string
	DestroyedAt  *time.Time
	UpdatedAt    time.Time
}

type OperatorRestartCompletion struct {
	IntentID   string
	ClaimToken string
	Status     OperatorIntentStatus
	Error      string
}

type OperatorRestartResult struct {
	AuditOutboxID int64
	Replayed      bool
}

func validRestartClaim(id, token string) error {
	for _, value := range []string{id, token} {
		parsed, err := uuid.Parse(value)
		if err != nil || parsed == uuid.Nil {
			return fmt.Errorf("operator restart requires nonzero intent and claim UUIDs")
		}
	}
	return nil
}

func validateRestartTarget(t OperatorRestartTarget) error {
	for _, value := range []string{t.InstanceID, t.AppID, t.DeploymentID, t.NodeID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return fmt.Errorf("operator restart target requires instance, app, deployment and node UUIDs")
		}
	}
	if t.Generation == "" || len(t.Generation) > api.OperatorRestartGenerationMaxBytes {
		return fmt.Errorf("operator restart target requires a bounded VM generation")
	}
	return nil
}

func sameRestartID(a, b string) bool {
	x, errA := uuid.Parse(a)
	y, errB := uuid.Parse(b)
	return errA == nil && errB == nil && x == y
}

// MatchesInstance checks the original durable placement and migration lineage.
// Vmmd separately verifies the physical generation before its effect.
func (t OperatorRestartTarget) MatchesInstance(i Instance) bool {
	return sameRestartID(t.InstanceID, i.ID) && sameRestartID(t.AppID, i.AppID) &&
		sameRestartID(t.DeploymentID, i.DeploymentID) && sameRestartID(t.NodeID, i.NodeID) && t.WakeID == i.WakeID &&
		t.MigrationLeaseToken == i.LeaseToken && sameRestartMigrationTime(t.MigratedAt, i.MigratedAt)
}

func sameRestartMigrationTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func cloneOperatorRestart(e OperatorRestartExecution) OperatorRestartExecution {
	e.Target.SnapshotIDs = slices.Clone(e.Target.SnapshotIDs)
	if e.Target.MigratedAt != nil {
		at := *e.Target.MigratedAt
		e.Target.MigratedAt = &at
	}
	if e.DestroyedAt != nil {
		at := *e.DestroyedAt
		e.DestroyedAt = &at
	}
	return e
}

func normalizeRestartCompletion(c OperatorRestartCompletion) (OperatorRestartCompletion, error) {
	if err := validRestartClaim(c.IntentID, c.ClaimToken); err != nil {
		return c, err
	}
	switch c.Status {
	case OperatorIntentSucceeded:
		if c.Error != "" {
			return c, fmt.Errorf("successful restart cannot carry an error")
		}
	case OperatorIntentFailed, OperatorIntentOutcomeUnknown:
		if c.Error == "" {
			return c, fmt.Errorf("unsuccessful restart requires a reason")
		}
	default:
		return c, fmt.Errorf("invalid restart completion status %q", c.Status)
	}
	c.Error = safetext.Truncate(c.Error, api.AuditReasonMaxBytes)
	return c, nil
}

func restartOutcomeAudit(i OperatorIntent, e *OperatorRestartExecution) []byte {
	data := map[string]any{"intent_id": i.ID, "actor": i.ActorID, "target_id": i.TargetID,
		"reason": i.Reason, "result": string(i.Status), "error": i.Error,
		"started_at": i.StartedAt, "finished_at": i.FinishedAt, "snap_ids_marked_stale": i.SnapIDsMarkedStale}
	if i.TraceID != nil {
		data["trace_id"] = *i.TraceID
	}
	if len(i.Metadata) > 0 {
		data["metadata"] = i.Metadata
	}
	if e != nil {
		target := e.Target
		target.MigrationLeaseToken = "" // Migration credentials stay inside the private execution journal.
		data["execution_target"], data["execution_phase"], data["destroyed_at"] = target, e.Phase, e.DestroyedAt
	}
	out, _ := json.Marshal(data)
	return out
}

func restartAuditKey(i OperatorIntent) string {
	return "operator.restart.outcome:" + i.ID + ":" + string(i.Status)
}
