package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventfilter"
)

var ErrWebhookAutomationConflict = errors.New("webhook automation revision, routing mode or event content conflict")
var ErrWebhookAutomationUnavailable = errors.New("webhook automation target unavailable")

type WebhookAutomationBinding struct {
	EndpointID, WorkflowName, EventType string
	Filter                              json.RawMessage
	Version                             int64
	UpdatedAt                           time.Time
}
type WebhookAutomationBindingOptions struct {
	EndpointID, AppID, AccountID, WorkflowName, EventType string
	Filter                                                json.RawMessage
	ExpectedVersion                                       int64
}
type WebhookAutomationReceipt struct {
	ReceiptID, EndpointID, ProviderEventID, WorkflowName, Status, IgnoredReason, EventSource, RoutingStatus, RunID string
	AcceptedAt                                                                                                     time.Time
	Duplicate                                                                                                      bool
	outboxID                                                                                                       int64
	recipientID                                                                                                    string
	bodyHash                                                                                                       []byte
}
type WebhookAutomationStore interface {
	SaveWebhookAutomationBinding(context.Context, WebhookAutomationBindingOptions) (WebhookAutomationBinding, error)
	GetWebhookAutomationBinding(context.Context, string) (WebhookAutomationBinding, error)
	DeleteWebhookAutomationBinding(context.Context, WebhookAutomationBindingOptions) error
	AcceptWebhookAutomation(context.Context, InboundWebhookEndpoint, json.RawMessage, bool) (WebhookAutomationReceipt, bool, error)
	AcceptVerifiedWebhookAutomation(context.Context, InboundWebhookEndpoint, string, string, json.RawMessage, bool) (WebhookAutomationReceipt, bool, error)
	GetWebhookAutomationReceipt(context.Context, string, string) (WebhookAutomationReceipt, error)
}

func validateWebhookAutomationBinding(opts WebhookAutomationBindingOptions) error {
	if opts.ExpectedVersion < 0 || len(opts.WorkflowName) == 0 || len(opts.WorkflowName) > api.WorkflowWebhookNameMaxBytes || len(opts.EventType) > api.WorkflowWebhookEventMaxBytes {
		return ErrAutomationInvalid
	}
	if err := eventfilter.ValidatePattern(opts.EventType); err != nil {
		return fmt.Errorf("%w: %w", ErrAutomationInvalid, err)
	}
	base := strings.Trim(opts.EventType, "*")
	if opts.EventType != "*" && !api.ValidInboundWebhookEventType(base) {
		return ErrAutomationInvalid
	}
	if len(opts.Filter) > api.WorkflowWebhookFilterMaxBytes {
		return ErrAutomationInvalid
	}
	if err := eventfilter.ValidateFilter(opts.Filter); err != nil {
		return fmt.Errorf("%w: %w", ErrAutomationInvalid, err)
	}
	return nil
}
func normalizedWebhookFilter(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return json.RawMessage(`{}`)
	}
	return cloneWorkflowJSON(raw)
}
func webhookAutomationSource(provider InboundWebhookProvider, endpointID string) string {
	if provider == "" {
		provider = InboundWebhookProviderStripe
	}
	return "gregale.inbound." + string(provider) + "." + canonicalMemUUID(endpointID)
}
func webhookEventFromStripeBody(body json.RawMessage) (string, string, error) {
	var event struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &event); err != nil || strings.TrimSpace(event.ID) == "" || len(event.ID) > api.WorkflowWebhookEventMaxBytes || !api.ValidInboundWebhookEventType(event.Type) {
		return "", "", ErrAutomationInvalid
	}
	return event.ID, event.Type, nil
}
func webhookAutomationRecipientID(endpointID, name string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale.workflow.webhook:"+canonicalMemUUID(endpointID)+":"+name)).String()
}
func webhookAutomationDefinition(raw json.RawMessage, records []Automation, name string, plan api.Plan) (*api.WorkflowSpec, string, error) {
	var definitions []api.WorkflowSpec
	if err := json.Unmarshal(raw, &definitions); err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrAutomationInvalid, err)
	}
	for _, spec := range definitions {
		if spec.Name != name {
			continue
		}
		for _, record := range records {
			if record.Name == name && len(record.Published) > 0 && !record.Enabled {
				return &spec, "automation_paused", nil
			}
		}
		if spec.Trigger != nil && spec.Trigger.Enabled != nil && !*spec.Trigger.Enabled {
			return &spec, "automation_paused", nil
		}
		if _, err := api.ValidateWorkflowDAG(spec, plan); err != nil {
			return nil, "", fmt.Errorf("%w: %w", ErrAutomationInvalid, err)
		}
		return &spec, "", nil
	}
	return nil, "automation_unpublished", nil
}

type webhookAutomationEnvelope struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Time            time.Time       `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	Data            json.RawMessage `json:"data"`
	AccountID       string          `json:"accountid"`
}

func prepareWebhookAutomation(endpoint InboundWebhookEndpoint, binding WebhookAutomationBinding, spec *api.WorkflowSpec, reason, eventID, eventType string, body json.RawMessage, now time.Time) (WebhookAutomationReceipt, webhookAutomationEnvelope, []PublishedEventRecipient, error) {
	if strings.TrimSpace(eventID) == "" || len(eventID) > api.WorkflowWebhookEventMaxBytes || !api.ValidInboundWebhookEventType(eventType) ||
		(endpoint.Provider == InboundWebhookProviderGeneric && !api.ValidInboundWebhookEventID(eventID)) {
		return WebhookAutomationReceipt{}, webhookAutomationEnvelope{}, nil, ErrAutomationInvalid
	}
	envelope := webhookAutomationEnvelope{SpecVersion: "1.0", ID: eventID, Source: webhookAutomationSource(endpoint.Provider, endpoint.ID), Type: eventType, Data: body, AccountID: canonicalMemUUID(endpoint.AccountID), Time: now.UTC(), DataContentType: "application/json"}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return WebhookAutomationReceipt{}, envelope, nil, err
	}
	if int64(len(encoded)) > api.WorkflowRunInputMaxBytes {
		return WebhookAutomationReceipt{}, envelope, nil, ErrWorkflowEventDefinitionInvalid
	}
	if reason == "" && !eventSubscriptionPatternMatches(binding.EventType, envelope.Type) {
		reason = "event_filtered"
	}
	receipt := WebhookAutomationReceipt{ReceiptID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:inbound-webhook:"+endpoint.ID+"\x00"+eventID)).String(), EndpointID: endpoint.ID, ProviderEventID: eventID, WorkflowName: binding.WorkflowName, Status: "ignored", IgnoredReason: reason, AcceptedAt: now, EventSource: envelope.Source, RoutingStatus: "ignored"}
	receipt.bodyHash, err = webhookAutomationBodyHashForEvent(endpoint.Provider, eventType, body)
	if err != nil {
		return receipt, envelope, nil, err
	}
	recipients := []PublishedEventRecipient{}
	if reason == "" {
		if spec == nil {
			return receipt, envelope, nil, ErrAutomationInvalid
		}
		snapshot, err := json.Marshal(spec)
		if err != nil {
			return receipt, envelope, nil, err
		}
		receipt.Status, receipt.RoutingStatus, receipt.recipientID = "accepted", "pending", webhookAutomationRecipientID(endpoint.ID, spec.Name)
		recipients = append(recipients, PublishedEventRecipient{ID: receipt.recipientID, AccountID: endpoint.AccountID, AppID: endpoint.AppID, WebhookEndpointID: endpoint.ID, WebhookProvider: endpoint.Provider, Source: envelope.Source, Type: binding.EventType, Filter: binding.Filter, Workflow: snapshot})
	}
	return receipt, envelope, recipients, nil
}
func webhookAutomationBodyHash(body json.RawMessage) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canonical)
	return sum[:], nil
}
func webhookAutomationBodyHashForEvent(provider InboundWebhookProvider, eventType string, body json.RawMessage) ([]byte, error) {
	bodyHash, err := webhookAutomationBodyHash(body)
	if err != nil || provider != InboundWebhookProviderGeneric {
		return bodyHash, err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(eventType))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(bodyHash)
	return hash.Sum(nil), nil
}
func copyWebhookAutomationBinding(value WebhookAutomationBinding) WebhookAutomationBinding {
	value.Filter = cloneWorkflowJSON(value.Filter)
	return value
}
