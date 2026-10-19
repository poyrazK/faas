package state

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/google/uuid"
)

// SnapshotRecoveryStore is implemented by the durable and memory stores.
// Schedd binds lineage before VM dispatch; imaged verifies it at publication.
// No readiness timestamp can grant a pre-recovery process a new generation.
type SnapshotRecoveryStore interface {
	BindInstanceSnapshotRecovery(context.Context, string, string, string) error
	InstanceSnapshotRecoveryFresh(context.Context, string) (bool, error)
	InvalidateSnapshotRecovery(context.Context, string) (SnapshotRecoveryResult, error)
}

type OperatorColdBootStore interface {
	ExecuteOperatorColdBoot(context.Context, string, string) (OperatorColdBootResult, error)
}

type SnapshotRecoveryResult struct {
	Generation  int64
	SnapshotIDs []string
}

type OperatorColdBootResult struct {
	SnapshotRecoveryResult
	Status        OperatorIntentStatus
	Error         string
	AuditOutboxID int64
	Replayed      bool
}

var ErrOperatorColdBootClaimLost = fmt.Errorf("operator cold-boot claim lost: %w", ErrConflict)

type instanceSnapshotRecovery struct {
	WakeID, SnapshotID string
	Generation         int64
}

func validateSnapshotRecoveryIDs(ids ...string) error {
	for _, value := range ids {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return ErrInvalidArgument
		}
	}
	return nil
}

func coldBootResult(i OperatorIntent, replayed bool) OperatorColdBootResult {
	return OperatorColdBootResult{SnapshotRecoveryResult: SnapshotRecoveryResult{
		Generation: i.ColdBootGeneration, SnapshotIDs: slices.Clone(i.SnapIDsMarkedStale)},
		Status: i.Status, Error: i.Error, AuditOutboxID: i.CompletionAuditOutboxID, Replayed: replayed}
}

func coldBootOutcomeAudit(i OperatorIntent) []byte {
	data := map[string]any{"intent_id": i.ID, "actor": i.ActorID, "target_id": i.TargetID,
		"reason": i.Reason, "result": string(i.Status), "error": i.Error,
		"started_at": i.StartedAt, "finished_at": i.FinishedAt,
		"snap_ids_marked_stale": i.SnapIDsMarkedStale, "recovery_generation": i.ColdBootGeneration}
	if i.TraceID != nil {
		data["trace_id"] = *i.TraceID
	}
	if len(i.Metadata) > 0 {
		data["metadata"] = i.Metadata
	}
	encoded, _ := json.Marshal(data)
	return encoded
}
