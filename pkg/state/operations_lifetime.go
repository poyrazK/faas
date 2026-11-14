package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Active HTTP work can outlive the result window. Only settled work or a
// stopped reconciliation expires; active execution keeps its scoped identity.
func operationIsActive(op Operation) bool {
	return op.State == api.OperationAccepted || op.State == api.OperationRunning || op.State == api.OperationWaiting
}

func operationRetained(op Operation, now time.Time) bool {
	return operationIsActive(op) || op.ExpiresAt.After(now) || repairRetainsHistory(op)
}

// An unresolved source outcome or reserved lifecycle transition has no time
// limit. Keep its exact identity and events until a durable result settles it.
func repairRetainsHistory(op Operation) bool {
	if op.DataRepair == nil {
		return false
	}
	switch op.DataRepair.Phase {
	case api.DataRepairApplying, api.DataRepairUnknown, api.DataRepairReplacing, api.DataRepairResolving:
		return true
	default:
		return false
	}
}

func (m *MemStore) operationRetainIdentityLocked(op Operation) {
	if !op.State.Terminal() && op.State != api.OperationRequiresReconciliation {
		return
	}
	until := op.UpdatedAt.Add(time.Duration(op.PlanLimits.IdempotencyRetentionSeconds) * time.Second)
	for key, receipt := range m.operationMemoryLocked().receipts {
		if receipt.OperationID == op.ID && receipt.ExpiresAt.Before(until) {
			receipt.ExpiresAt = until
			m.operationData.receipts[key] = receipt
		}
	}
}
