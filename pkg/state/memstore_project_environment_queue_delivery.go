package state

import (
	"context"
	"encoding/json"
	"time"
)

var _ ProjectEnvironmentQueueDeliveryStore = (*MemStore)(nil)

func (m *MemStore) ClaimNextProjectEnvironmentQueueDelivery(_ context.Context, req ProjectEnvironmentQueueDeliveryRequest) (ProjectEnvironmentQueueDelivery, error) {
	if err := validateEnvironmentQueueDeliveryRequest(req); err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	set, err := m.projectEnvironmentQueueConsumersLocked(req.AccountID, req.ProjectID, req.DeploymentID, false, true)
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	consumer, err := queueConsumerByName(set, req.BindingName)
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	if consumer.Mode != req.Mode {
		return ProjectEnvironmentQueueDelivery{}, ErrConflict
	}
	account, ok := m.accounts[req.AccountID]
	if !ok {
		return ProjectEnvironmentQueueDelivery{}, ErrNotFound
	}
	limits, err := environmentQueueDeliveryLimits(account, req.LeaseSeconds)
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	var next Invocation
	for id, owner := range m.invocationEnvironmentQueueAdmissions {
		inv := m.invocations[id]
		if owner.ConsumerID != consumer.ID || owner.RuntimeSetID != set.ID || inv.State != InvocationPending ||
			inv.DueAt.After(now) || (inv.DeadlineAt != nil && !inv.DeadlineAt.After(now)) {
			continue
		}
		if next.ID == "" || environmentQueueInvocationBefore(inv, next) {
			next = inv
		}
	}
	if next.ID == "" {
		return ProjectEnvironmentQueueDelivery{}, ErrNotFound
	}
	if next.QuotaReserved {
		return ProjectEnvironmentQueueDelivery{}, ErrInvocationEnvironmentWorkIsolation
	}
	if _, err := m.validateInvocationQueueClaimLocked(next, true); err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	// A timeout/reaper counts as a delivery attempt too. Retire an exhausted
	// recovered message without reserving quota or issuing a new receipt.
	if environmentQueueDeliveryAttemptsExhausted(next, limits) {
		m.invocations[next.ID] = exhaustEnvironmentQueueDelivery(next, now)
		return ProjectEnvironmentQueueDelivery{}, ErrNotFound
	}
	if err := m.queueClaimCapacityLocked(next); err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	quota, exists := m.accountAsyncQuota[req.AccountID]
	if !exists {
		quota.MaxInflight = limits.MaxAsyncInvocationsPerAccount
	}
	if quota.CurrentInflight >= quota.MaxInflight {
		return ProjectEnvironmentQueueDelivery{}, ErrQuotaExceeded
	}
	token, tokenHash, err := newEnvironmentQueueReceipt()
	if err != nil {
		return ProjectEnvironmentQueueDelivery{}, err
	}
	ownerHash := environmentQueueReceiptOwnerHash(m.invocationEnvironmentQueueAdmissions[next.ID])
	previous, issued := m.invocationEnvironmentQueueReceipts[next.ID]
	if issued && (previous.Attempt >= next.Attempts+1 || previous.OwnerHash != ownerHash) {
		return ProjectEnvironmentQueueDelivery{}, ErrInvocationEnvironmentWorkIsolation
	}
	lease := now.Add(time.Duration(req.LeaseSeconds) * time.Second)
	next.State, next.QuotaReserved, next.ReceivedAt, next.LeaseExpiresAt = InvocationDispatching, true, &now, &lease
	next.Attempts++
	m.invocationEnvironmentQueueReceipts[next.ID] = environmentQueueReceipt{Attempt: next.Attempts, TokenHash: tokenHash, OwnerHash: ownerHash, IssuedAt: now, LeaseExpiresAt: lease}
	m.invocations[next.ID] = next
	quota.CurrentInflight++
	m.accountAsyncQuota[req.AccountID] = quota
	return cloneEnvironmentQueueDelivery(next, consumer, token), nil
}

func environmentQueueInvocationBefore(a, b Invocation) bool {
	if !a.DueAt.Equal(b.DueAt) {
		return a.DueAt.Before(b.DueAt)
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

func (m *MemStore) CompleteProjectEnvironmentQueueDelivery(_ context.Context, scope ProjectEnvironmentQueueDeliveryScope, id, token string, result json.RawMessage) error {
	if len(result) > 0 && !json.Valid(result) {
		return ErrInvalidArgument
	}
	return m.finishEnvironmentQueueDelivery(scope, id, token, result, nil)
}

func (m *MemStore) RetryProjectEnvironmentQueueDelivery(_ context.Context, scope ProjectEnvironmentQueueDeliveryScope, id, token, lastError string) error {
	return m.finishEnvironmentQueueDelivery(scope, id, token, nil, &lastError)
}

func (m *MemStore) finishEnvironmentQueueDelivery(scope ProjectEnvironmentQueueDeliveryScope, id, token string, result json.RawMessage, lastError *string) error {
	if err := validateEnvironmentQueueDeliveryScope(scope); err != nil {
		return err
	}
	tokenHash, err := environmentQueueReceiptTokenHash(id, token)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	set, err := m.projectEnvironmentQueueConsumersLocked(scope.AccountID, scope.ProjectID, scope.DeploymentID, false, false)
	if err != nil {
		return err
	}
	consumer, err := queueConsumerByName(set, scope.BindingName)
	if err != nil {
		return err
	}
	inv, found := m.invocations[id]
	owner, owned := m.invocationEnvironmentQueueAdmissions[id]
	receipt, issued := m.invocationEnvironmentQueueReceipts[id]
	if !found || !owned || !issued || owner.ConsumerID != consumer.ID || owner.RuntimeSetID != set.ID {
		return ErrNotFound
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := validateEnvironmentQueueReceipt(receipt, owner, inv, tokenHash, now); err != nil {
		return err
	}
	if _, err := m.validateInvocationQueueClaimLocked(inv, false); err != nil {
		return err
	}
	if lastError != nil {
		account, ok := m.accounts[scope.AccountID]
		if !ok {
			return ErrNotFound
		}
		inv, err = environmentQueueRetry(inv, account.Plan, now, *lastError)
		if err != nil {
			return err
		}
	} else {
		inv.State, inv.QuotaReserved, inv.CompletedAt, inv.LastError = InvocationCompleted, false, &now, ""
		outcome := OutcomeSuccess
		inv.Outcome = &outcome
		if len(result) > 0 {
			inv.Result = append(json.RawMessage(nil), result...)
		}
	}
	m.invocations[id] = inv
	m.decrementAccountAsyncInflightLocked(scope.AccountID)
	return nil
}
