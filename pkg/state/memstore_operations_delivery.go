package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) OperationDeliverySnapshot(_ context.Context, account, id string) (api.OperationDeliveryInspection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	data := m.operationMemoryLocked()
	op, ok := data.operations[id]
	if !ok || op.AccountID != account {
		return api.OperationDeliveryInspection{}, ErrNotFound
	}
	if !operationRetained(op, now) {
		return api.OperationDeliveryInspection{}, ErrOperationExpired
	}
	d, ok := m.appWebhookDeliveries[op.CompletionDelivery.DeliveryID]
	if !ok {
		return operationDeliveryObservation(op, nil, 0, now), nil
	}
	if !operationDeliveryOwned(op, data.definitions[op.DefinitionID], d) {
		return api.OperationDeliveryInspection{}, ErrNotFound
	}
	return operationDeliveryObservation(op, &d, m.appWebhookReplayGenerations[d.ID], now), nil
}

func (m *MemStore) RetryOperationCompletionDelivery(_ context.Context, account, id string, req api.OperationDeliveryRetryRequest) (api.OperationDeliveryRetryResponse, error) {
	if err := validateOperationDeliveryRetry(req); err != nil {
		return api.OperationDeliveryRetryResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	now := time.Now().UTC()
	op, ok := data.operations[id]
	if !ok || op.AccountID != account {
		return api.OperationDeliveryRetryResponse{}, ErrNotFound
	}
	if !operationRetained(op, now) || !op.ExpiresAt.Truncate(time.Microsecond).After(now.Truncate(time.Microsecond)) {
		return api.OperationDeliveryRetryResponse{}, ErrOperationExpired
	}
	key := id + "/" + req.RetryID
	if prior, ok := data.deliveryRetries[key]; ok {
		if !operationDeliveryReceiptMatches(prior, req) {
			return api.OperationDeliveryRetryResponse{}, ErrOperationInputConflict
		}
		return prior, nil
	}
	if !operationDeliveryIDsEqual(req.DeliveryID, op.CompletionDelivery.DeliveryID) {
		return api.OperationDeliveryRetryResponse{}, ErrConflict
	}
	limits, ok := api.LimitsFor(m.accounts[account].Plan)
	if !ok || limits.WebhookPerApp == 0 {
		return api.OperationDeliveryRetryResponse{}, NewOperationLimitError("completion_delivery_plan", 0, 1)
	}
	count := 0
	for _, r := range data.deliveryRetries {
		if r.OperationID == id {
			count++
		}
	}
	if count >= api.OperationDeliveryRetriesMax {
		return api.OperationDeliveryRetryResponse{}, NewOperationLimitError("completion_delivery_retries", api.OperationDeliveryRetriesMax, int64(count+1))
	}
	d, ok := m.appWebhookDeliveries[op.CompletionDelivery.DeliveryID]
	if !ok || !operationDeliveryOwned(op, data.definitions[op.DefinitionID], d) {
		return api.OperationDeliveryRetryResponse{}, ErrNotFound
	}
	if d.Status != AppWebhookDeliveryDead || m.appWebhookReplayGenerations[d.ID] != *req.ExpectedReplayGeneration {
		return api.OperationDeliveryRetryResponse{}, ErrConflict
	}
	receipt := newOperationDeliveryReceipt(op, req, now)
	d.Status = AppWebhookDeliveryPending
	d.Attempt = 0
	d.LastError = ""
	d.LastResponseCode = 0
	d.NextAttemptAt = now
	d.UpdatedAt = now
	m.appWebhookDeliveries[d.ID] = d
	if m.appWebhookReplayGenerations == nil {
		m.appWebhookReplayGenerations = map[string]int{}
	}
	m.appWebhookReplayGenerations[d.ID] = receipt.ReplayGeneration
	if data.deliveryRetries == nil {
		data.deliveryRetries = map[string]api.OperationDeliveryRetryResponse{}
	}
	data.deliveryRetries[key] = receipt
	return receipt, nil
}
