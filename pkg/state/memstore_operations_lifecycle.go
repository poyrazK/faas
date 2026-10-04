package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) operationForInvocationLocked(invocationID string) (Operation, OperationDefinition, bool) {
	if m.operationData == nil {
		return Operation{}, OperationDefinition{}, false
	}
	id, exists := m.operationData.executions[invocationID]
	if !exists {
		return Operation{}, OperationDefinition{}, false
	}
	op := cloneOperation(m.operationData.operations[id])
	return op, m.operationData.definitions[op.DefinitionID], true
}

func (m *MemStore) operationClaimLocked(inv Invocation) (Invocation, error) {
	op, _, exists := m.operationForInvocationLocked(inv.ID)
	if !exists {
		return inv, nil
	}
	capability, event, err := operationClaim(&op, inv, time.Now().UTC())
	if err != nil {
		return Invocation{}, err
	}
	m.operationSaveLocked(op, event)
	return operationExecutionHeaders(inv, op, capability), nil
}

func (m *MemStore) operationSaveLocked(op Operation, event api.OperationEvent) {
	m.operationPinsLocked(op)
	m.operationData.operations[op.ID] = cloneOperation(op)
	m.operationData.events[op.ID] = append(m.operationData.events[op.ID], event)
}

func (m *MemStore) operationTransitionLocked(inv Invocation, uncertain bool) error {
	op, def, exists := m.operationForInvocationLocked(inv.ID)
	if !exists {
		return nil
	}
	limits := api.MustLimitsFor(m.accounts[op.AccountID].Plan)
	event, err := operationInvocationTransition(&op, inv, def, limits, uncertain, time.Now().UTC())
	if err != nil {
		return err
	}
	if op.State.Terminal() {
		if err := m.operationCompletionLocked(&op, def); err != nil {
			return err
		}
	}
	m.operationSaveLocked(op, event)
	return nil
}

func (m *MemStore) operationCompletionLocked(op *Operation, def OperationDefinition) error {
	if def.Spec.CompletionWebhookID == "" {
		return nil
	}
	hook, exists := m.appWebhooks[def.Spec.CompletionWebhookID]
	if !exists || hook.AccountID != op.AccountID || hook.AppID != op.AppID || !hook.Enabled {
		op.CompletionDelivery = api.OperationDeliveryResponse{State: "configuration_failed", LastError: "completion destination is unavailable"}
		return nil
	}
	id := newOperationID()
	op.CompletionDelivery = api.OperationDeliveryResponse{State: "pending", DeliveryID: id}
	payload, err := operationCompletionPayload(*op)
	if err != nil {
		return err
	}
	delivery := AppWebhookDelivery{ID: id, WebhookID: hook.ID, AccountID: op.AccountID, AppID: op.AppID, Event: AppWebhookEventOperationFinished, Payload: payload, Status: AppWebhookDeliveryPending, NextAttemptAt: op.UpdatedAt, CreatedAt: op.UpdatedAt, UpdatedAt: op.UpdatedAt}
	if m.appWebhookDeliveries == nil {
		m.appWebhookDeliveries = map[string]AppWebhookDelivery{}
	}
	m.appWebhookDeliveries[id] = delivery
	return nil
}

func (m *MemStore) operationDeliveryLocked(op Operation) Operation {
	if id := op.CompletionDelivery.DeliveryID; id != "" {
		delivery, exists := m.appWebhookDeliveries[id]
		if !exists {
			op.CompletionDelivery.State = "delivery_expired"
		} else {
			op.CompletionDelivery = operationDeliveryProjection(delivery)
		}
	}
	return op
}

func operationDeliveryProjection(delivery AppWebhookDelivery) api.OperationDeliveryResponse {
	projection := api.OperationDeliveryResponse{State: string(delivery.Status), DeliveryID: delivery.ID, Attempts: delivery.Attempt, LastError: delivery.LastError}
	if delivery.Status == AppWebhookDeliveryPending || delivery.Status == AppWebhookDeliveryFailed {
		next := delivery.NextAttemptAt
		projection.NextAttemptAt = &next
	}
	return projection
}

func (m *MemStore) ReportOperationProgress(_ context.Context, operationID string, authority OperationExecutionAuthority, report api.OperationReportRequest) (Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	op, exists := data.operations[operationID]
	if !exists {
		return Operation{}, ErrNotFound
	}
	inv, exists := m.invocations[authority.InvocationID]
	if !exists {
		return Operation{}, ErrNotFound
	}
	now := time.Now().UTC()
	if err := ValidateOperationExecutionAuthority(op, inv, authority, now); err != nil {
		return Operation{}, err
	}
	key := fmt.Sprintf("%s/%s/%d/%s", op.ID, inv.ID, inv.Attempts, report.ReportID)
	fingerprint := operationReportFingerprint(report)
	if prior, exists := data.reports[key]; exists {
		if prior != fingerprint {
			return Operation{}, ErrOperationInputConflict
		}
		return cloneOperation(op), nil
	}
	limits := api.MustLimitsFor(m.accounts[op.AccountID].Plan)
	event, err := operationProgress(&op, inv, data.definitions[op.DefinitionID], report, limits.Operations, now)
	if err != nil {
		return Operation{}, err
	}
	data.reports[key] = fingerprint
	m.operationSaveLocked(op, event)
	return cloneOperation(op), nil
}
