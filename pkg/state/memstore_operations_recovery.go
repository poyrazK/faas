package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) RecoverOperation(_ context.Context, accountID, tenantID, operationID string, req api.OperationRecoveryRequest) (Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	op, exists := data.operations[operationID]
	if !exists || op.AccountID != accountID || (tenantID != "" && op.PlatformTenantID != tenantID) {
		return Operation{}, ErrNotFound
	}
	op = cloneOperation(op)
	now := time.Now().UTC()
	if !op.ExpiresAt.After(now) {
		return Operation{}, ErrOperationExpired
	}
	limits := api.MustLimitsFor(m.accounts[accountID].Plan)
	fingerprintLimits := limits
	fingerprintLimits.MaxSourceBytesPerInvocation = op.ValueMaxBytes
	fingerprint, err := operationRecoveryFingerprint(req, fingerprintLimits)
	if err != nil {
		return Operation{}, err
	}
	key := operationID + "/" + req.RecoveryID
	if prior, exists := data.recoveries[key]; exists {
		if prior != fingerprint {
			return Operation{}, ErrOperationInputConflict
		}
		return cloneOperation(m.operationDeliveryLocked(op)), nil
	}
	original, exists := m.invocations[op.CurrentInvocationID]
	if !exists {
		return Operation{}, ErrNotFound
	}
	if req.Resolution == "safe_to_retry" {
		if !limits.Operations.Allowed {
			return Operation{}, NewOperationLimitError("plan_admission", 0, 1)
		}
		if err := m.platformTenantInvocationAllowedLocked(original); err != nil {
			return Operation{}, err
		}
	}
	def := data.definitions[op.DefinitionID]
	inv, event, err := prepareOperationRecovery(&op, original, def, limits, req, now)
	if err != nil {
		return Operation{}, err
	}
	if req.Resolution == "safe_to_retry" {
		m.invocations[inv.ID] = inv
		data.executions[inv.ID] = op.ID
	} else {
		if err := m.operationCompletionLocked(&op, def); err != nil {
			return Operation{}, err
		}
	}
	data.recoveries[key] = fingerprint
	m.operationSaveLocked(op, event)
	return cloneOperation(op), nil
}
