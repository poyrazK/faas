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

func (m *MemStore) webhookAutomationEndpointLocked(opts WebhookAutomationBindingOptions) (InboundWebhookEndpoint, error) {
	endpoint, ok := m.inboundWebhookEndpoints[opts.EndpointID]
	app := m.apps[opts.AppID]
	if !ok || endpoint.AppID != opts.AppID || endpoint.AccountID != opts.AccountID || app.AccountID != opts.AccountID || app.Status == AppDeleted {
		return endpoint, ErrNotFound
	}
	return endpoint, nil
}
func (m *MemStore) SaveWebhookAutomationBinding(_ context.Context, opts WebhookAutomationBindingOptions) (WebhookAutomationBinding, error) {
	if err := validateWebhookAutomationBinding(opts); err != nil {
		return WebhookAutomationBinding{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	endpoint, err := m.webhookAutomationEndpointLocked(opts)
	if err != nil {
		return WebhookAutomationBinding{}, err
	}
	previous := m.webhookAutomationBindings[endpoint.ID]
	if previous.Version != opts.ExpectedVersion {
		return WebhookAutomationBinding{}, ErrWebhookAutomationConflict
	}
	if _, exists := m.exclusiveTriggerBindings["inbound_webhook\x00"+endpoint.ID]; exists {
		return WebhookAutomationBinding{}, ErrWebhookAutomationConflict
	}
	for _, binding := range m.workflowCallbackWebhookBindings {
		if binding.EndpointID == endpoint.ID {
			return WebhookAutomationBinding{}, ErrWebhookAutomationConflict
		}
	}
	dep, account, eligible := m.workflowScheduleTargetLocked(endpoint.AppID)
	if !eligible || (endpoint.Provider != InboundWebhookProviderStripe && endpoint.Provider != InboundWebhookProviderGeneric) {
		return WebhookAutomationBinding{}, ErrWebhookAutomationUnavailable
	}
	spec, _, err := webhookAutomationDefinition(dep.Workflows, m.automationRecordsLocked(endpoint.AppID), opts.WorkflowName, account.Plan)
	if err != nil {
		return WebhookAutomationBinding{}, err
	}
	if spec == nil {
		return WebhookAutomationBinding{}, ErrNotFound
	}
	if err := m.validateWorkflowOutboundLocked(endpoint.AppID, endpoint.AccountID, *spec); err != nil {
		return WebhookAutomationBinding{}, err
	}
	m.webhookAutomationRevision++
	binding := WebhookAutomationBinding{EndpointID: endpoint.ID, WorkflowName: opts.WorkflowName, EventType: opts.EventType, Filter: normalizedWebhookFilter(opts.Filter), Version: m.webhookAutomationRevision, UpdatedAt: time.Now().UTC()}
	if m.webhookAutomationBindings == nil {
		m.webhookAutomationBindings = map[string]WebhookAutomationBinding{}
	}
	m.webhookAutomationBindings[endpoint.ID] = binding
	return copyWebhookAutomationBinding(binding), nil
}
func (m *MemStore) GetWebhookAutomationBinding(_ context.Context, id string) (WebhookAutomationBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	binding, ok := m.webhookAutomationBindings[id]
	if !ok {
		return binding, ErrNotFound
	}
	return copyWebhookAutomationBinding(binding), nil
}
func (m *MemStore) DeleteWebhookAutomationBinding(_ context.Context, opts WebhookAutomationBindingOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.webhookAutomationEndpointLocked(opts); err != nil {
		return err
	}
	binding, ok := m.webhookAutomationBindings[opts.EndpointID]
	if !ok {
		return ErrNotFound
	}
	if binding.Version != opts.ExpectedVersion {
		return ErrWebhookAutomationConflict
	}
	delete(m.webhookAutomationBindings, opts.EndpointID)
	return nil
}
func webhookAutomationReceiptKey(endpointID, eventID string) string {
	return endpointID + "\x00" + eventID
}
func (m *MemStore) AcceptWebhookAutomation(ctx context.Context, verified InboundWebhookEndpoint, body json.RawMessage, runtimeEnabled bool) (WebhookAutomationReceipt, bool, error) {
	eventID, eventType, err := webhookEventFromStripeBody(body)
	if err != nil {
		return WebhookAutomationReceipt{}, true, err
	}
	return m.AcceptVerifiedWebhookAutomation(ctx, verified, eventID, eventType, body, runtimeEnabled)
}

func (m *MemStore) AcceptVerifiedWebhookAutomation(_ context.Context, verified InboundWebhookEndpoint, eventID, eventType string, body json.RawMessage, runtimeEnabled bool) (WebhookAutomationReceipt, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	endpoint, err := m.webhookAutomationEndpointLocked(WebhookAutomationBindingOptions{EndpointID: verified.ID, AppID: verified.AppID, AccountID: verified.AccountID})
	if err != nil {
		return WebhookAutomationReceipt{}, true, err
	}
	if !endpoint.Enabled || !bytes.Equal(endpoint.SigningSecretSealed, verified.SigningSecretSealed) {
		return WebhookAutomationReceipt{}, true, ErrWebhookAutomationUnavailable
	}
	if strings.TrimSpace(eventID) == "" || len(eventID) > api.WorkflowWebhookEventMaxBytes ||
		(eventType != "" && !api.ValidInboundWebhookEventType(eventType)) ||
		(endpoint.Provider == InboundWebhookProviderGeneric && (!api.ValidInboundWebhookEventType(eventType) || !api.ValidInboundWebhookEventID(eventID))) {
		return WebhookAutomationReceipt{}, true, ErrAutomationInvalid
	}
	key := webhookAutomationReceiptKey(endpoint.ID, eventID)
	prior, exists := m.webhookAutomationReceipts[key]
	binding, bound := m.webhookAutomationBindings[endpoint.ID]
	if !exists && !bound {
		return WebhookAutomationReceipt{}, false, nil
	}
	account := m.accounts[endpoint.AccountID]
	if !account.Active() || !account.Plan.WorkflowsAllowed() {
		return WebhookAutomationReceipt{}, true, ErrWebhookAutomationUnavailable
	}
	if exists {
		hash, err := webhookAutomationBodyHashForEvent(endpoint.Provider, eventType, body)
		if err != nil {
			return prior, true, err
		}
		if !bytes.Equal(hash, prior.bodyHash) {
			return prior, true, ErrWebhookAutomationConflict
		}
		prior = m.webhookAutomationReceiptProgressLocked(prior)
		prior.Duplicate = true
		return prior, true, nil
	}
	if !runtimeEnabled {
		return WebhookAutomationReceipt{}, true, ErrWebhookAutomationUnavailable
	}
	dep, currentAccount, eligible := m.workflowScheduleTargetLocked(endpoint.AppID)
	if !eligible {
		return WebhookAutomationReceipt{}, true, ErrWebhookAutomationUnavailable
	}
	spec, reason, err := webhookAutomationDefinition(dep.Workflows, m.automationRecordsLocked(endpoint.AppID), binding.WorkflowName, currentAccount.Plan)
	if err != nil {
		if errors.Is(err, ErrAutomationInvalid) {
			err = ErrWebhookAutomationUnavailable
		}
		return WebhookAutomationReceipt{}, true, err
	}
	receipt, envelope, recipients, err := prepareWebhookAutomation(endpoint, binding, spec, reason, eventID, eventType, body, time.Now().UTC())
	if err != nil {
		return receipt, true, err
	}
	// A delivery accepted before opting into automation mode keeps its original
	// app-invocation receipt and cannot gain a second effect from provider retry.
	if _, exists := m.invocations[receipt.ReceiptID]; exists {
		return WebhookAutomationReceipt{}, false, nil
	}
	if spec != nil && reason == "" {
		if err := m.validateWorkflowOutboundLocked(endpoint.AppID, endpoint.AccountID, *spec); err != nil {
			return receipt, true, ErrWebhookAutomationUnavailable
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
	if m.webhookAutomationReceipts == nil {
		m.webhookAutomationReceipts = map[string]WebhookAutomationReceipt{}
	}
	m.webhookAutomationReceipts[key] = receipt
	return receipt, true, nil
}
func (m *MemStore) webhookAutomationReceiptProgressLocked(receipt WebhookAutomationReceipt) WebhookAutomationReceipt {
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
func (m *MemStore) GetWebhookAutomationReceipt(_ context.Context, endpointID, eventID string) (WebhookAutomationReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, ok := m.webhookAutomationReceipts[webhookAutomationReceiptKey(endpointID, eventID)]
	if !ok {
		return receipt, ErrNotFound
	}
	return m.webhookAutomationReceiptProgressLocked(receipt), nil
}
