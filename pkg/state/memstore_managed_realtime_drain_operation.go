package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

func (m *MemStore) CreateManagedRealtimeDrainOperation(_ context.Context, input ManagedRealtimeDrainOperationInput) (ManagedRealtimeDrainOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	op := ManagedRealtimeDrainOperation{
		ID: uuid.NewString(), AccountID: input.AccountID, AppID: input.AppID,
		EndpointID: input.EndpointID, Status: ManagedRealtimeDrainOperationRunning,
		Reason: input.Reason, DryRun: input.DryRun, Matched: input.Matched, CreatedAt: now,
		Result: json.RawMessage(`{}`),
	}
	m.managedRealtimeDrainOperations[op.ID] = op
	return cloneManagedRealtimeDrainOperation(op), nil
}

func (m *MemStore) CompleteManagedRealtimeDrainOperation(_ context.Context, id, accountID, endpointID string, status ManagedRealtimeDrainOperationStatus, result json.RawMessage, matched, closed, gone, failed int) (ManagedRealtimeDrainOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.managedRealtimeDrainOperations[id]
	if !ok || op.AccountID != accountID || op.EndpointID != endpointID {
		return ManagedRealtimeDrainOperation{}, ErrManagedRealtimeDrainOperationNotFound
	}
	now := time.Now().UTC()
	op.Status, op.Matched, op.Closed, op.Gone, op.Failed = status, matched, closed, gone, failed
	op.Result = append(json.RawMessage(nil), result...)
	op.CompletedAt = &now
	m.managedRealtimeDrainOperations[id] = op
	return cloneManagedRealtimeDrainOperation(op), nil
}

func (m *MemStore) GetManagedRealtimeDrainOperation(_ context.Context, accountID, endpointID, id string) (ManagedRealtimeDrainOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.managedRealtimeDrainOperations[id]
	if !ok || op.AccountID != accountID || op.EndpointID != endpointID {
		return ManagedRealtimeDrainOperation{}, ErrManagedRealtimeDrainOperationNotFound
	}
	return cloneManagedRealtimeDrainOperation(op), nil
}

func cloneManagedRealtimeDrainOperation(op ManagedRealtimeDrainOperation) ManagedRealtimeDrainOperation {
	op.Result = append(json.RawMessage(nil), op.Result...)
	if op.CompletedAt != nil {
		completedAt := *op.CompletedAt
		op.CompletedAt = &completedAt
	}
	return op
}
