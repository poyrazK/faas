package state

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
)

type NodeLifecycleCompletionStore interface {
	CompleteNodeLifecycle(context.Context, NodeLifecycleCompletion) (NodeLifecycleCompletionResult, error)
}

type NodeLifecycleCompletion struct {
	NodeID                                      string
	Generation                                  int64
	Lifecycle                                   NodeLifecycle
	DrainedCount, MigratedCount, RecreatedCount int64
}

// The receipt also implements events.RecoveryEvent without a dependency cycle.
type NodeLifecycleCompletionResult struct {
	NodeLifecycleCompletion
	ResultGeneration         int64
	ResultLifecycle          NodeLifecycle
	NodeName                 string
	InitiatedAt, CompletedAt time.Time
	AuditOutboxID            int64
	Replayed                 bool
}

var ErrNodeLifecycleChanged = fmt.Errorf("node lifecycle generation changed: %w", ErrConflict)

func (in NodeLifecycleCompletion) validate() error {
	if err := validateSnapshotRecoveryIDs(in.NodeID); err != nil {
		return err
	}
	if in.Generation <= 0 || in.Generation == math.MaxInt64 || in.DrainedCount < 0 || in.MigratedCount < 0 || in.RecreatedCount < 0 {
		return ErrInvalidArgument
	}
	switch in.Lifecycle {
	case NodeLifecycleDraining, NodeLifecycleForceDraining, NodeLifecycleRecovering:
		return nil
	default:
		return ErrInvalidArgument
	}
}

func (r NodeLifecycleCompletionResult) Kind() string {
	if r.Lifecycle == NodeLifecycleRecovering {
		return "node.recovered"
	}
	return "node.drained"
}
func (r NodeLifecycleCompletionResult) At() time.Time    { return r.CompletedAt }
func (r NodeLifecycleCompletionResult) Subject() *string { return nil }
func (r NodeLifecycleCompletionResult) Payload() map[string]any {
	data := map[string]any{"node_id": r.NodeID, "node_name": r.NodeName, "lifecycle_generation": r.Generation, "result_generation": r.ResultGeneration}
	if r.Lifecycle == NodeLifecycleRecovering {
		data["recovery_initiated_at"], data["migrated_count"], data["recreated_count"] = r.InitiatedAt, r.MigratedCount, r.RecreatedCount
	} else {
		data["initiated_at"], data["completed_at"], data["drained_instance_count"] = r.InitiatedAt, r.CompletedAt, r.DrainedCount
	}
	return data
}
func (r NodeLifecycleCompletionResult) auditData() []byte {
	body, _ := json.Marshal(r.Payload())
	return body
}
func (r NodeLifecycleCompletionResult) dedupeKey() string {
	return "node.lifecycle.completion:" + r.NodeID + ":" + strconv.FormatInt(r.Generation, 10)
}

func nodeLifecycleRequiresReceipt(before, after NodeLifecycle) bool {
	return (before == NodeLifecycleDraining || before == NodeLifecycleForceDraining) && after == NodeLifecycleMaintenance || before == NodeLifecycleRecovering && after == NodeLifecycleActive
}

func nextNodeLifecycle(prior, next ComputeNode, completed bool) (ComputeNode, error) {
	if prior.ID == "" {
		next.LifecycleGeneration = 1
		return next, nil
	}
	if !completed && nodeLifecycleRequiresReceipt(prior.Lifecycle, next.Lifecycle) {
		return ComputeNode{}, ErrConflict
	}
	next.LifecycleGeneration = prior.LifecycleGeneration
	if next.Lifecycle != prior.Lifecycle || !sameNodeTime(next.DrainInitiatedAt, prior.DrainInitiatedAt) || !sameNodeTime(next.RecoveryInitiatedAt, prior.RecoveryInitiatedAt) {
		if next.LifecycleGeneration == math.MaxInt64 {
			return ComputeNode{}, ErrConflict
		}
		next.LifecycleGeneration++
	}
	return next, nil
}
func sameNodeTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}

func nodeCompletionResult(in NodeLifecycleCompletion, node ComputeNode, now time.Time) NodeLifecycleCompletionResult {
	r := NodeLifecycleCompletionResult{NodeLifecycleCompletion: in, NodeName: node.Name, CompletedAt: now, ResultGeneration: in.Generation + 1, ResultLifecycle: NodeLifecycleMaintenance, InitiatedAt: now}
	start := node.DrainInitiatedAt
	if in.Lifecycle == NodeLifecycleRecovering {
		start = node.RecoveryInitiatedAt
		r.ResultLifecycle = NodeLifecycleActive
	}
	if start != nil {
		r.InitiatedAt = *start
	}
	return r
}
