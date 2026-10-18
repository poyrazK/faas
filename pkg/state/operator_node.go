package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// OperatorNodeStore commits a lifecycle request, its immutable result and audit
// handoff together. Success acknowledges the request; evacuation is asynchronous.
type OperatorNodeStore interface {
	ExecuteOperatorNode(context.Context, string, string) (OperatorNodeResult, error)
}

type OperatorNodeResult struct {
	Status            OperatorIntentStatus
	Error             string
	NodeID            string
	PreviousLifecycle NodeLifecycle
	Lifecycle         NodeLifecycle
	Changed           bool
	FinishedAt        time.Time
	AuditOutboxID     int64
	Replayed          bool
}

var ErrOperatorNodeClaimLost = fmt.Errorf("operator node claim lost: %w", ErrConflict)

func IsOperatorNodeKind(kind OperatorIntentKind) bool {
	switch kind {
	case OperatorIntentKindNodeDrain, OperatorIntentKindNodeForceDrain, OperatorIntentKindNodeActivate, OperatorIntentKindNodeRetire:
		return true
	default:
		return false
	}
}

func operatorNodeTransition(kind OperatorIntentKind, node ComputeNode, hasLive bool) (NodeLifecycle, error) {
	current := node.Lifecycle
	if current == NodeLifecycleRetired {
		if kind == OperatorIntentKindNodeRetire {
			return current, nil
		}
		return current, fmt.Errorf("operator_intent: retired nodes cannot change lifecycle")
	}
	switch kind {
	case OperatorIntentKindNodeDrain:
		if current == NodeLifecycleDraining {
			return current, nil
		}
		if current == NodeLifecycleActive {
			return NodeLifecycleDraining, nil
		}
	case OperatorIntentKindNodeForceDrain:
		if current == NodeLifecycleMaintenance || current == NodeLifecycleForceDraining {
			return current, nil
		}
		return NodeLifecycleForceDraining, nil
	case OperatorIntentKindNodeActivate:
		if current == NodeLifecycleActive || current == NodeLifecycleUnavailable || current == NodeLifecycleMaintenance {
			return NodeLifecycleActive, nil
		}
	case OperatorIntentKindNodeRetire:
		if node.Name == DefaultLocalNodeName {
			return current, fmt.Errorf("operator_intent: default-local cannot be retired")
		}
		if current == NodeLifecycleMaintenance && !hasLive {
			return NodeLifecycleRetired, nil
		}
	}
	return current, fmt.Errorf("operator_intent: %s refused from lifecycle %s (live instances: %t)", kind, current, hasLive)
}

func operatorNodeOutcomeAudit(i OperatorIntent, r OperatorNodeResult) []byte {
	data := map[string]any{"intent_id": i.ID, "actor": i.ActorID, "target_id": i.TargetID,
		"reason": i.Reason, "result": string(r.Status), "error": r.Error,
		"started_at": i.StartedAt, "finished_at": r.FinishedAt,
		"previous_lifecycle": r.PreviousLifecycle, "result_lifecycle": r.Lifecycle, "changed": r.Changed}
	if i.TraceID != nil {
		data["trace_id"] = *i.TraceID
	}
	if len(i.Metadata) > 0 {
		data["metadata"] = i.Metadata
	}
	encoded, _ := json.Marshal(data)
	return encoded
}
