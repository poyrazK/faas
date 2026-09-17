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
		Reason: input.Reason, DryRun: input.DryRun, Matched: input.Matched,
		ConnectionIDs: append([]string(nil), input.ConnectionIDs...), Limit: input.Limit,
		Truncated: input.Truncated, Partial: input.Partial, NodesQueried: input.NodesQueried,
		NodesUnavailable: input.NodesUnavailable, NextAttemptAt: now, CreatedAt: now,
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
	op.ConnectionIDs = nil
	op.Result = append(json.RawMessage(nil), result...)
	op.ClaimToken, op.ClaimedAt, op.LastError = "", nil, ""
	op.CompletedAt = &now
	m.managedRealtimeDrainOperations[id] = op
	return cloneManagedRealtimeDrainOperation(op), nil
}

func (m *MemStore) ClaimManagedRealtimeDrainOperations(_ context.Context, limit int, lease time.Duration) ([]ManagedRealtimeDrainOperationClaim, error) {
	if limit <= 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	claims := make([]ManagedRealtimeDrainOperationClaim, 0, limit)
	for id, op := range m.managedRealtimeDrainOperations {
		if len(claims) == limit || op.Status != ManagedRealtimeDrainOperationRunning || op.NextAttemptAt.After(now) {
			continue
		}
		if op.ClaimedAt != nil && now.Sub(*op.ClaimedAt) < lease {
			continue
		}
		claimedAt := now
		op.ClaimedAt = &claimedAt
		op.ClaimToken = uuid.NewString()
		op.Attempts++
		m.managedRealtimeDrainOperations[id] = op
		claims = append(claims, ManagedRealtimeDrainOperationClaim{Operation: cloneManagedRealtimeDrainOperation(op), ClaimToken: op.ClaimToken})
	}
	return claims, nil
}

func (m *MemStore) RetryManagedRealtimeDrainOperation(_ context.Context, id, claimToken string, connectionIDs []string, result json.RawMessage, closed, gone, failed int, nextAttemptAt time.Time, lastError string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.managedRealtimeDrainOperations[id]
	if !ok || op.ClaimToken != claimToken || op.Status != ManagedRealtimeDrainOperationRunning {
		return ErrManagedRealtimeDrainOperationNotFound
	}
	op.ConnectionIDs = append([]string(nil), connectionIDs...)
	op.Result = append(json.RawMessage(nil), result...)
	op.Closed, op.Gone, op.Failed = closed, gone, failed
	op.NextAttemptAt = nextAttemptAt.UTC()
	op.ClaimToken, op.ClaimedAt, op.LastError = "", nil, lastError
	m.managedRealtimeDrainOperations[id] = op
	return nil
}

func (m *MemStore) FinishManagedRealtimeDrainOperation(_ context.Context, id, claimToken string, status ManagedRealtimeDrainOperationStatus, result json.RawMessage, closed, gone, failed int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.managedRealtimeDrainOperations[id]
	if !ok || op.ClaimToken != claimToken || op.Status != ManagedRealtimeDrainOperationRunning {
		return ErrManagedRealtimeDrainOperationNotFound
	}
	now := time.Now().UTC()
	op.Status, op.ConnectionIDs = status, nil
	op.Result = append(json.RawMessage(nil), result...)
	op.Closed, op.Gone, op.Failed = closed, gone, failed
	op.ClaimToken, op.ClaimedAt, op.LastError, op.CompletedAt = "", nil, "", &now
	m.managedRealtimeDrainOperations[id] = op
	return nil
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
	op.ConnectionIDs = append([]string(nil), op.ConnectionIDs...)
	if op.ClaimedAt != nil {
		claimedAt := *op.ClaimedAt
		op.ClaimedAt = &claimedAt
	}
	if op.CompletedAt != nil {
		completedAt := *op.CompletedAt
		op.CompletedAt = &completedAt
	}
	return op
}
