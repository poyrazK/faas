package state

import (
	"context"
	"crypto/md5" // #nosec G501 -- Stable routing identity, not a security primitive.
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// EventWorkflowStore reads preview candidates and atomically admits a captured
// workflow recipient. The outbox claim protects against stale routing workers.
type EventWorkflowStore interface {
	ListMatchingEventWorkflows(context.Context, string, string, string, string, int) ([]PublishedEventRecipient, error)
	AdmitEventWorkflow(context.Context, int64, string, string) (string, error)
}

var ErrWorkflowEventDefinitionInvalid = errors.New("workflow: invalid captured event definition")

var ErrWorkflowEventTargetUnavailable = errors.New("workflow: event target is temporarily unavailable")

func workflowEventRecipientID(appID, name string) string {
	return uuid.UUID(md5.Sum([]byte("gregale.workflow.event:" + canonicalMemUUID(appID) + ":" + name))).String() // #nosec G401 -- Must match the database recipient identity.
}

func eventWorkflowRun(recipient PublishedEventRecipient, payload []byte, plan api.Plan) (*WorkflowRun, error) {
	var definition api.WorkflowSpec
	if err := json.Unmarshal(recipient.Workflow, &definition); err != nil {
		return nil, err
	}
	if _, err := api.ValidateWorkflowDAG(definition, plan); err != nil {
		return nil, err
	}
	if recipient.WebhookEndpointID != "" {
		if recipient.Source != webhookAutomationSource(recipient.WebhookEndpointID) || recipient.ID != webhookAutomationRecipientID(recipient.WebhookEndpointID, definition.Name) {
			return nil, ErrWorkflowEventDefinitionInvalid
		}
	} else if definition.Trigger == nil || definition.Trigger.Type != "event" ||
		(definition.Trigger.Enabled != nil && !*definition.Trigger.Enabled) ||
		workflowEventRecipientID(recipient.AppID, definition.Name) != recipient.ID {
		return nil, fmt.Errorf("workflow: invalid captured event definition")
	}
	if int64(len(payload)) > api.WorkflowRunInputMaxBytes {
		return nil, fmt.Errorf("workflow: event envelope exceeds workflow input limit")
	}
	run := &WorkflowRun{AppID: recipient.AppID, WorkflowName: definition.Name,
		Input: cloneWorkflowJSON(payload), DefinitionSnapshot: cloneWorkflowJSON(recipient.Workflow)}
	if err := prepareWorkflowRun(run); err != nil {
		return nil, err
	}
	return run, nil
}

func (m *MemStore) workflowEventRecipientsLocked(accountID, source, typ string) []PublishedEventRecipient {
	result := make([]PublishedEventRecipient, 0)
	for appID, app := range m.apps {
		if !sameMemUUID(app.AccountID, accountID) {
			continue
		}
		deployment, _, eligible := m.workflowScheduleTargetLocked(appID)
		if !eligible {
			continue
		}
		var definitions []api.WorkflowSpec
		if json.Unmarshal(deployment.Workflows, &definitions) != nil {
			continue
		}
		for _, definition := range definitions {
			trigger := definition.Trigger
			if trigger == nil || trigger.Type != "event" || (trigger.Enabled != nil && !*trigger.Enabled) ||
				!eventSubscriptionPatternMatches(trigger.Source, source) || !eventSubscriptionPatternMatches(trigger.EventType, typ) {
				continue
			}
			raw, _ := json.Marshal(definition)
			filter := cloneWorkflowJSON(trigger.Filter)
			if len(filter) == 0 {
				filter = json.RawMessage(`{}`)
			}
			result = append(result, PublishedEventRecipient{ID: workflowEventRecipientID(appID, definition.Name),
				AppID: appID, AccountID: accountID, DeploymentID: deployment.ID,
				Source: trigger.Source, Type: trigger.EventType, Filter: filter, Workflow: raw})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (m *MemStore) ListMatchingEventWorkflows(_ context.Context, accountID, source, typ, after string, limit int) ([]PublishedEventRecipient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]PublishedEventRecipient, 0)
	for _, recipient := range m.workflowEventRecipientsLocked(accountID, source, typ) {
		if recipient.ID > after {
			result = append(result, recipient)
		}
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func eventWorkflowReceiptKey(outboxID int64, recipientID string) string {
	return strconv.FormatInt(outboxID, 10) + "/" + recipientID
}

func (m *MemStore) AdmitEventWorkflow(_ context.Context, outboxID int64, token, recipientID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var work *PublishedEventWork
	for _, candidate := range m.eventFanout {
		if candidate.ID == outboxID {
			work = candidate
			break
		}
	}
	if work == nil || token == "" || work.ClaimToken != token || work.Delivered {
		return "", ErrConflict
	}
	var recipient PublishedEventRecipient
	for _, candidate := range work.RecipientSnapshot {
		if candidate.ID == recipientID && len(candidate.Workflow) != 0 {
			recipient = candidate
			break
		}
	}
	if recipient.ID == "" {
		return "", ErrNotFound
	}
	key := eventWorkflowReceiptKey(outboxID, recipientID)
	if runID, exists := m.eventWorkflowReceipts[key]; exists {
		if _, retained := m.workflowRuns[runID]; !retained {
			return "", nil
		}
		return runID, nil
	}
	app, exists := m.apps[recipient.AppID]
	if !exists || app.Status == AppDeleted || !sameMemUUID(app.AccountID, recipient.AccountID) {
		return "", ErrNotFound
	}
	account, exists := m.accounts[app.AccountID]
	if !exists || !account.Active() || !account.Plan.WorkflowsAllowed() || app.MaintenanceMode || app.PlatformTenantRequired {
		return "", ErrWorkflowEventTargetUnavailable
	}
	run, err := eventWorkflowRun(recipient, work.Payload, account.Plan)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrWorkflowEventDefinitionInvalid, err)
	}
	active := 0
	for _, candidate := range m.workflowRuns {
		if candidate.AppID == app.ID && (candidate.Status == WorkflowRunStatusPending || candidate.Status == WorkflowRunStatusRunning || candidate.Status == WorkflowRunStatusAwaitingEvent) {
			active++
		}
	}
	if active >= account.Plan.WorkflowMaxConcurrentRuns() {
		return "", ErrWorkflowRunQuotaExceeded
	}
	if err := m.insertWorkflowRunLocked(run); err != nil {
		return "", err
	}
	if m.eventWorkflowReceipts == nil {
		m.eventWorkflowReceipts = make(map[string]string)
	}
	m.eventWorkflowReceipts[key] = run.ID
	return run.ID, nil
}
