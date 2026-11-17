package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) webhookWorkflowEndpointLocked(opts WebhookWorkflowBindingOptions) (InboundWebhookEndpoint, error) {
	endpoint, ok := m.inboundWebhookEndpoints[opts.EndpointID]
	app := m.apps[opts.AppID]
	if !ok || endpoint.AppID != opts.AppID || endpoint.AccountID != opts.AccountID || app.AccountID != opts.AccountID || app.Status == AppDeleted {
		return endpoint, ErrNotFound
	}
	return endpoint, nil
}
func (m *MemStore) SaveWebhookWorkflowBinding(_ context.Context, opts WebhookWorkflowBindingOptions) (WebhookWorkflowBinding, error) {
	if err := validateWebhookWorkflowBinding(opts); err != nil {
		return WebhookWorkflowBinding{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	endpoint, err := m.webhookWorkflowEndpointLocked(opts)
	if err != nil {
		return WebhookWorkflowBinding{}, err
	}
	previous := m.webhookWorkflowBindings[endpoint.ID]
	if previous.Version != opts.ExpectedVersion {
		return WebhookWorkflowBinding{}, ErrWebhookWorkflowConflict
	}
	if _, exists := m.exclusiveTriggerBindings["inbound_webhook\x00"+endpoint.ID]; exists || m.operationWebhookBoundLocked(endpoint.ID) {
		return WebhookWorkflowBinding{}, ErrWebhookWorkflowConflict
	}
	for _, binding := range m.workflowCallbackWebhookBindings {
		if binding.EndpointID == endpoint.ID {
			return WebhookWorkflowBinding{}, ErrWebhookWorkflowConflict
		}
	}
	dep, account, eligible := m.workflowScheduleTargetLocked(endpoint.AppID)
	if !eligible || (endpoint.Provider != InboundWebhookProviderStripe && endpoint.Provider != InboundWebhookProviderGeneric) {
		return WebhookWorkflowBinding{}, ErrWebhookWorkflowUnavailable
	}
	spec, _, err := webhookWorkflowDefinition(dep.Workflows, m.storedWorkflowRecordsLocked(endpoint.AppID), opts.WorkflowName, account.Plan)
	if err != nil {
		return WebhookWorkflowBinding{}, err
	}
	if spec == nil {
		return WebhookWorkflowBinding{}, ErrNotFound
	}
	if err := m.validateWorkflowOutboundLocked(endpoint.AppID, endpoint.AccountID, *spec); err != nil {
		return WebhookWorkflowBinding{}, err
	}
	m.webhookWorkflowRevision++
	binding := WebhookWorkflowBinding{EndpointID: endpoint.ID, WorkflowName: opts.WorkflowName, EventType: opts.EventType, Filter: normalizedWebhookFilter(opts.Filter), Version: m.webhookWorkflowRevision, UpdatedAt: time.Now().UTC()}
	if m.webhookWorkflowBindings == nil {
		m.webhookWorkflowBindings = map[string]WebhookWorkflowBinding{}
	}
	m.webhookWorkflowBindings[endpoint.ID] = binding
	return copyWebhookWorkflowBinding(binding), nil
}
func (m *MemStore) GetWebhookWorkflowBinding(_ context.Context, id string) (WebhookWorkflowBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	binding, ok := m.webhookWorkflowBindings[id]
	if !ok {
		return binding, ErrNotFound
	}
	return copyWebhookWorkflowBinding(binding), nil
}
func (m *MemStore) DeleteWebhookWorkflowBinding(_ context.Context, opts WebhookWorkflowBindingOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.webhookWorkflowEndpointLocked(opts); err != nil {
		return err
	}
	binding, ok := m.webhookWorkflowBindings[opts.EndpointID]
	if !ok {
		return ErrNotFound
	}
	if binding.Version != opts.ExpectedVersion {
		return ErrWebhookWorkflowConflict
	}
	delete(m.webhookWorkflowBindings, opts.EndpointID)
	return nil
}
func webhookWorkflowReceiptKey(endpointID, eventID string) string {
	return endpointID + "\x00" + eventID
}
func (m *MemStore) AcceptWebhookWorkflow(ctx context.Context, verified InboundWebhookEndpoint, body json.RawMessage, runtimeEnabled bool) (WebhookWorkflowReceipt, bool, error) {
	eventID, eventType, err := webhookEventFromStripeBody(body)
	if err != nil {
		return WebhookWorkflowReceipt{}, true, err
	}
	return m.AcceptVerifiedWebhookWorkflow(ctx, verified, eventID, eventType, body, runtimeEnabled)
}

func (m *MemStore) AcceptVerifiedWebhookWorkflow(_ context.Context, verified InboundWebhookEndpoint, eventID, eventType string, body json.RawMessage, runtimeEnabled bool) (WebhookWorkflowReceipt, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	endpoint, err := m.webhookWorkflowEndpointLocked(WebhookWorkflowBindingOptions{EndpointID: verified.ID, AppID: verified.AppID, AccountID: verified.AccountID})
	if err != nil {
		return WebhookWorkflowReceipt{}, true, err
	}
	if !endpoint.Enabled || !bytes.Equal(endpoint.SigningSecretSealed, verified.SigningSecretSealed) {
		return WebhookWorkflowReceipt{}, true, ErrWebhookWorkflowUnavailable
	}
	if strings.TrimSpace(eventID) == "" || len(eventID) > api.WorkflowWebhookEventMaxBytes ||
		(eventType != "" && !api.ValidInboundWebhookEventType(eventType)) ||
		(endpoint.Provider == InboundWebhookProviderGeneric && (!api.ValidInboundWebhookEventType(eventType) || !api.ValidInboundWebhookEventID(eventID))) {
		return WebhookWorkflowReceipt{}, true, ErrStoredWorkflowInvalid
	}
	key := webhookWorkflowReceiptKey(endpoint.ID, eventID)
	prior, exists := m.webhookWorkflowReceipts[key]
	binding, bound := m.webhookWorkflowBindings[endpoint.ID]
	if !exists && !bound {
		return WebhookWorkflowReceipt{}, false, nil
	}
	account := m.accounts[endpoint.AccountID]
	if !account.Active() || !account.Plan.WorkflowsAllowed() {
		return WebhookWorkflowReceipt{}, true, ErrWebhookWorkflowUnavailable
	}
	if exists {
		hash, err := webhookWorkflowBodyHashForEvent(endpoint.Provider, eventType, body)
		if err != nil {
			return prior, true, err
		}
		if !bytes.Equal(hash, prior.bodyHash) {
			return prior, true, ErrWebhookWorkflowConflict
		}
		prior = m.webhookWorkflowReceiptProgressLocked(prior)
		prior.Duplicate = true
		return prior, true, nil
	}
	if !runtimeEnabled {
		return WebhookWorkflowReceipt{}, true, ErrWebhookWorkflowUnavailable
	}
	dep, currentAccount, eligible := m.workflowScheduleTargetLocked(endpoint.AppID)
	if !eligible {
		return WebhookWorkflowReceipt{}, true, ErrWebhookWorkflowUnavailable
	}
	spec, reason, err := webhookWorkflowDefinition(dep.Workflows, m.storedWorkflowRecordsLocked(endpoint.AppID), binding.WorkflowName, currentAccount.Plan)
	if err != nil {
		if errors.Is(err, ErrStoredWorkflowInvalid) {
			err = ErrWebhookWorkflowUnavailable
		}
		return WebhookWorkflowReceipt{}, true, err
	}
	if m.storedWorkflowFailureBlocksAdmissionLocked(endpoint.AppID, binding.WorkflowName, time.Now().UTC()) {
		reason = "stored_workflow_failure_paused"
	}
	receipt, envelope, recipients, err := prepareWebhookWorkflow(endpoint, binding, spec, reason, eventID, eventType, body, time.Now().UTC())
	if err != nil {
		return receipt, true, err
	}
	// A delivery accepted before opting into automation mode keeps its original
	// app-invocation receipt and cannot gain a second effect from provider retry.
	if _, exists := m.invocations[receipt.ReceiptID]; exists {
		return WebhookWorkflowReceipt{}, false, nil
	}
	if spec != nil && reason == "" {
		if err := m.validateWorkflowOutboundLocked(endpoint.AppID, endpoint.AccountID, *spec); err != nil {
			return receipt, true, ErrWebhookWorkflowUnavailable
		}
	}
	for i := range recipients {
		recipients[i].DeploymentID = dep.ID
	}
	if err := m.pinWorkflowEventRecipientsLocked(recipients); err != nil {
		return receipt, true, err
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return receipt, true, err
	}
	if err := m.appendEventLocked("apid", "event.published", &endpoint.AccountID, payload, nil, receipt.AcceptedAt); err != nil {
		return receipt, true, err
	}
	work := m.eventFanout[canonicalMemUUID(endpoint.AccountID)+"\x00"+envelope.Source+"\x00"+envelope.ID]
	work.RecipientSnapshot = recipients
	receipt.outboxID = work.ID
	if m.webhookWorkflowReceipts == nil {
		m.webhookWorkflowReceipts = map[string]WebhookWorkflowReceipt{}
	}
	m.webhookWorkflowReceipts[key] = receipt
	return receipt, true, nil
}
func (m *MemStore) webhookWorkflowReceiptProgressLocked(receipt WebhookWorkflowReceipt) WebhookWorkflowReceipt {
	for _, work := range m.eventFanout {
		if work.ID == receipt.outboxID {
			if p, exists := work.RecipientProgress[receipt.recipientID]; exists {
				receipt.RoutingStatus = p.State
			}
			receipt.RunID = m.eventWorkflowReceipts[eventWorkflowReceiptKey(work.ID, receipt.recipientID)]
			break
		}
	}
	return receipt
}
func (m *MemStore) GetWebhookWorkflowReceipt(_ context.Context, endpointID, eventID string) (WebhookWorkflowReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, ok := m.webhookWorkflowReceipts[webhookWorkflowReceiptKey(endpointID, eventID)]
	if !ok {
		return receipt, ErrNotFound
	}
	return m.webhookWorkflowReceiptProgressLocked(receipt), nil
}
