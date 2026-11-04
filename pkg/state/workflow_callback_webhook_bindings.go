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
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationchecks"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrWebhookAutomationReceiptPrecedesCallback = errors.New("webhook automation receipt precedes callback")
var ErrWebhookCallbackBindingChanged = errors.New("webhook callback binding changed during delivery")
var ErrWebhookLegacyReceiptPrecedesCallback = errors.New("inbound webhook invocation receipt precedes callback")

func inboundWebhookLegacyReceiptID(endpointID, eventID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:inbound-webhook:"+endpointID+"\x00"+eventID)).String()
}

type workflowCallbackWebhookReceipt struct {
	BodyHash           []byte
	CallbackID, Status string
	AcceptedAt         time.Time
}

// WebhookCallbackAlreadyHandled identifies an earlier callback delivery after
// an endpoint is rebound to another routing mode.
type WebhookCallbackAlreadyHandled struct{ CallbackID, Status string }

func (e *WebhookCallbackAlreadyHandled) Error() string {
	return "webhook event was already handled as a callback"
}

const workflowCallbackWebhookReceiptRetention = 30 * 24 * time.Hour

type WorkflowCallbackWebhookReceiptRetentionStore interface {
	PruneWorkflowCallbackWebhookReceipts(context.Context, time.Time, int) (int64, error)
}

func (m *MemStore) PruneWorkflowCallbackWebhookReceipts(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit < 1 {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var removed int64
	for key, receipt := range m.workflowCallbackWebhookReceipts {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if receipt.AcceptedAt.Before(now.Add(-workflowCallbackWebhookReceiptRetention)) {
			delete(m.workflowCallbackWebhookReceipts, key)
			removed++
			if removed == int64(limit) {
				break
			}
		}
	}
	return removed, nil
}

func (s *PgStore) PruneWorkflowCallbackWebhookReceipts(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit < 1 {
		return 0, ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM workflow_callback_webhook_receipts WHERE (endpoint_id,provider_event_id) IN (SELECT endpoint_id,provider_event_id FROM workflow_callback_webhook_receipts WHERE accepted_at<$1 ORDER BY accepted_at,endpoint_id,provider_event_id LIMIT $2 FOR UPDATE SKIP LOCKED)`, now.Add(-workflowCallbackWebhookReceiptRetention), limit)
	return tag.RowsAffected(), err
}

func callbackWebhookBodyHash(payload json.RawMessage) ([]byte, error) {
	canonical, err := automationchecks.CanonicalJSON(payload)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(canonical)
	return hash[:], nil
}

func callbackReceiptMatches(receipt workflowCallbackWebhookReceipt, payload json.RawMessage) (bool, error) {
	hash, err := callbackWebhookBodyHash(payload)
	if err != nil {
		return false, err
	}
	if bytes.Equal(receipt.BodyHash, hash) {
		return true, nil
	}
	// Preserve exact retries for receipts written before numeric normalization.
	legacy, err := webhookAutomationBodyHash(payload)
	return err == nil && bytes.Equal(receipt.BodyHash, legacy), err
}

func callbackReceiptOutcome(receipt workflowCallbackWebhookReceipt, payload json.RawMessage) (bool, error) {
	match, err := callbackReceiptMatches(receipt, payload)
	if err != nil {
		return false, err
	}
	if !match {
		return false, ErrConflict
	}
	return false, &WebhookCallbackAlreadyHandled{CallbackID: receipt.CallbackID, Status: receipt.Status}
}

// FencedWorkflowCallbackWebhookStore verifies the binding and routing receipt
// in the same transaction that completes the workflow callback.
type FencedWorkflowCallbackWebhookStore interface {
	CompleteBoundWorkflowCallback(context.Context, InboundWebhookEndpoint, WorkflowCallbackWebhookBinding, string, time.Duration, json.RawMessage) (bool, error)
}

func (m *MemStore) CompleteBoundWorkflowCallback(ctx context.Context, verified InboundWebhookEndpoint, binding WorkflowCallbackWebhookBinding, providerEventID string, timeout time.Duration, payload json.RawMessage) (bool, error) {
	if strings.TrimSpace(providerEventID) == "" || len(providerEventID) > api.WorkflowWebhookEventMaxBytes {
		return false, ErrAutomationInvalid
	}
	if err := validateWorkflowJSON(payload, false); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	endpoint, ok := m.inboundWebhookEndpoints[verified.ID]
	if !ok || !endpoint.Enabled || endpoint.AppID != verified.AppID || endpoint.AccountID != verified.AccountID || !bytes.Equal(endpoint.SigningSecretSealed, verified.SigningSecretSealed) {
		return false, ErrWebhookAutomationUnavailable
	}
	if _, exists := m.webhookAutomationReceipts[webhookAutomationReceiptKey(endpoint.ID, providerEventID)]; exists {
		return false, ErrWebhookAutomationReceiptPrecedesCallback
	}
	if _, exists := m.invocations[inboundWebhookLegacyReceiptID(endpoint.ID, providerEventID)]; exists {
		return false, ErrWebhookLegacyReceiptPrecedesCallback
	}
	hash, err := callbackWebhookBodyHash(payload)
	if err != nil {
		return false, err
	}
	receiptKey := webhookAutomationReceiptKey(endpoint.ID, providerEventID)
	if receipt, exists := m.workflowCallbackWebhookReceipts[receiptKey]; exists {
		return callbackReceiptOutcome(receipt, payload)
	}
	current, exists := m.workflowCallbackWebhookBindings[binding.ID]
	if !exists || !sameWorkflowCallbackWebhookBinding(current, binding) {
		return false, ErrWebhookCallbackBindingChanged
	}
	duplicate, err := m.completeWorkflowCallbackLocked("", binding.RunID, binding.StepName, api.WorkflowCallbackEventName(binding.RunID, binding.StepName), binding.ID, timeout, payload)
	status := "received"
	if errors.Is(err, ErrWorkflowCallbackClosed) || errors.Is(err, ErrWorkflowCallbackExpired) || errors.Is(err, ErrConflict) {
		status = "ignored"
	} else if err != nil {
		return false, err
	}
	if m.workflowCallbackWebhookReceipts == nil {
		m.workflowCallbackWebhookReceipts = make(map[string]workflowCallbackWebhookReceipt)
	}
	m.workflowCallbackWebhookReceipts[receiptKey] = workflowCallbackWebhookReceipt{BodyHash: hash, CallbackID: binding.ID, Status: status, AcceptedAt: time.Now().UTC()}
	return duplicate, err
}

func (s *PgStore) CompleteBoundWorkflowCallback(ctx context.Context, verified InboundWebhookEndpoint, binding WorkflowCallbackWebhookBinding, providerEventID string, timeout time.Duration, payload json.RawMessage) (bool, error) {
	if strings.TrimSpace(providerEventID) == "" || len(providerEventID) > api.WorkflowWebhookEventMaxBytes {
		return false, ErrAutomationInvalid
	}
	if err := validateWorkflowJSON(payload, false); err != nil {
		return false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Callback binding creation takes the run lock before the endpoint lock.
	var runID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM workflow_runs WHERE id=$1 FOR UPDATE`, binding.RunID).Scan(&runID); err != nil {
		return false, mapErr(err)
	}
	q := sqlc.New()
	endpoint, err := q.LockWebhookAutomationEndpoint(ctx, tx, sqlc.LockWebhookAutomationEndpointParams{ID: mustPgUUID(verified.ID), AppID: mustPgUUID(verified.AppID), AccountID: mustPgUUID(verified.AccountID)})
	if err != nil {
		return false, mapErr(err)
	}
	if !endpoint.Enabled || !bytes.Equal(endpoint.SigningSecretSealed, verified.SigningSecretSealed) {
		return false, ErrWebhookAutomationUnavailable
	}
	_, err = q.GetWebhookAutomationReceipt(ctx, tx, sqlc.GetWebhookAutomationReceiptParams{EndpointID: mustPgUUID(verified.ID), ProviderEventID: providerEventID})
	if err == nil {
		return false, ErrWebhookAutomationReceiptPrecedesCallback
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	legacy, err := q.WebhookAutomationLegacyReceiptExists(ctx, tx, mustPgUUID(inboundWebhookLegacyReceiptID(verified.ID, providerEventID)))
	if err != nil {
		return false, err
	}
	if legacy {
		return false, ErrWebhookLegacyReceiptPrecedesCallback
	}
	hash, err := callbackWebhookBodyHash(payload)
	if err != nil {
		return false, err
	}
	var prior workflowCallbackWebhookReceipt
	err = tx.QueryRow(ctx, `SELECT body_hash,callback_id::text,status,accepted_at FROM workflow_callback_webhook_receipts WHERE endpoint_id=$1 AND provider_event_id=$2`, verified.ID, providerEventID).Scan(&prior.BodyHash, &prior.CallbackID, &prior.Status, &prior.AcceptedAt)
	if err == nil {
		return callbackReceiptOutcome(prior, payload)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	current, err := scanWorkflowCallbackWebhookBinding(tx.QueryRow(ctx, "SELECT "+workflowCallbackWebhookBindingColumns+" FROM workflow_callback_webhook_bindings WHERE id=$1 FOR SHARE", binding.ID))
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !sameWorkflowCallbackWebhookBinding(current, binding) {
		return false, ErrWebhookCallbackBindingChanged
	}
	if err != nil {
		return false, err
	}
	duplicate, err := completeWorkflowCallbackTx(ctx, tx, "", binding.RunID, binding.StepName, api.WorkflowCallbackEventName(binding.RunID, binding.StepName), binding.ID, timeout, payload)
	status := "received"
	if errors.Is(err, ErrWorkflowCallbackClosed) || errors.Is(err, ErrWorkflowCallbackExpired) || errors.Is(err, ErrConflict) {
		status = "ignored"
	} else if err != nil {
		return false, err
	}
	resultErr := err
	if _, err := tx.Exec(ctx, `INSERT INTO workflow_callback_webhook_receipts(endpoint_id,provider_event_id,body_hash,callback_id,status) VALUES($1,$2,$3,$4,$5)`, verified.ID, providerEventID, hash, binding.ID, status); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return duplicate, resultErr
}

// WorkflowCallbackWebhookBinding routes one verified provider event to a
// callback. Its ID is the callback ID, so duplicate management calls are
// naturally idempotent.
type WorkflowCallbackWebhookBinding struct {
	ID         string
	EndpointID string
	RunID      string
	StepName   string
	EventType  string
	ObjectID   string
	CreatedAt  time.Time
}

type WorkflowCallbackWebhookBindingStore interface {
	CreateWorkflowCallbackWebhookBinding(context.Context, WorkflowCallbackWebhookBinding) (WorkflowCallbackWebhookBinding, error)
	WorkflowCallbackWebhookBindingByID(context.Context, string) (WorkflowCallbackWebhookBinding, error)
	WorkflowCallbackWebhookBindingByMatch(context.Context, string, string, string) (WorkflowCallbackWebhookBinding, error)
	DeleteWorkflowCallbackWebhookBinding(context.Context, string) error
}

func validateWorkflowCallbackWebhookBinding(binding WorkflowCallbackWebhookBinding) error {
	if _, err := uuid.Parse(binding.ID); err != nil {
		return ErrWorkflowInvalidRecord
	}
	if _, err := uuid.Parse(binding.EndpointID); err != nil {
		return ErrWorkflowInvalidRecord
	}
	if _, err := uuid.Parse(binding.RunID); err != nil {
		return ErrWorkflowInvalidRecord
	}
	if binding.StepName == "" ||
		!api.ValidStripeWorkflowCallbackMatch(binding.EventType, binding.ObjectID) {
		return ErrWorkflowInvalidRecord
	}
	if binding.ID != api.WorkflowCallbackID(binding.RunID, binding.StepName) {
		return ErrWorkflowInvalidRecord
	}
	return nil
}

func sameWorkflowCallbackWebhookBinding(left, right WorkflowCallbackWebhookBinding) bool {
	return left.ID == right.ID && left.EndpointID == right.EndpointID &&
		left.RunID == right.RunID && left.StepName == right.StepName &&
		left.EventType == right.EventType && left.ObjectID == right.ObjectID
}

func (m *MemStore) CreateWorkflowCallbackWebhookBinding(_ context.Context, binding WorkflowCallbackWebhookBinding) (WorkflowCallbackWebhookBinding, error) {
	if err := validateWorkflowCallbackWebhookBinding(binding); err != nil {
		return WorkflowCallbackWebhookBinding{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.webhookAutomationBindings[binding.EndpointID]; exists {
		return WorkflowCallbackWebhookBinding{}, ErrConflict
	}
	if existing, ok := m.workflowCallbackWebhookBindings[binding.ID]; ok {
		if sameWorkflowCallbackWebhookBinding(existing, binding) {
			return existing, nil
		}
		return WorkflowCallbackWebhookBinding{}, ErrConflict
	}
	run, ok := m.workflowRuns[binding.RunID]
	if !ok {
		return WorkflowCallbackWebhookBinding{}, ErrWorkflowRunNotFound
	}
	if run.Status == WorkflowRunStatusSucceeded || run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead {
		return WorkflowCallbackWebhookBinding{}, ErrWorkflowCallbackClosed
	}
	for _, event := range m.workflowEvents[binding.RunID] {
		if event.ID == binding.ID {
			return WorkflowCallbackWebhookBinding{}, ErrWorkflowCallbackClosed
		}
	}
	endpoint, ok := m.inboundWebhookEndpoints[binding.EndpointID]
	app, appOK := m.apps[run.AppID]
	if !ok || !appOK || endpoint.AppID != run.AppID || endpoint.AccountID != app.AccountID || endpoint.Provider != InboundWebhookProviderStripe {
		return WorkflowCallbackWebhookBinding{}, ErrNotFound
	}
	if step, ok := m.workflowSteps[binding.RunID][binding.StepName]; ok {
		if step.Status == WorkflowStepStatusSucceeded || step.Status == WorkflowStepStatusFailed || step.Status == WorkflowStepStatusDead || step.Status == WorkflowStepStatusSkipped {
			return WorkflowCallbackWebhookBinding{}, ErrWorkflowCallbackClosed
		}
	}
	for _, existing := range m.workflowCallbackWebhookBindings {
		if existing.RunID == binding.RunID && existing.StepName == binding.StepName {
			return WorkflowCallbackWebhookBinding{}, ErrConflict
		}
		if existing.EndpointID == binding.EndpointID && existing.EventType == binding.EventType && existing.ObjectID == binding.ObjectID {
			return WorkflowCallbackWebhookBinding{}, ErrConflict
		}
	}
	if m.workflowCallbackWebhookBindings == nil {
		m.workflowCallbackWebhookBindings = make(map[string]WorkflowCallbackWebhookBinding)
	}
	binding.CreatedAt = time.Now().UTC()
	m.workflowCallbackWebhookBindings[binding.ID] = binding
	return binding, nil
}

func (m *MemStore) WorkflowCallbackWebhookBindingByID(_ context.Context, id string) (WorkflowCallbackWebhookBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	binding, ok := m.workflowCallbackWebhookBindings[id]
	if !ok {
		return WorkflowCallbackWebhookBinding{}, ErrNotFound
	}
	return binding, nil
}

func (m *MemStore) WorkflowCallbackWebhookBindingByMatch(_ context.Context, endpointID, eventType, objectID string) (WorkflowCallbackWebhookBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, binding := range m.workflowCallbackWebhookBindings {
		if binding.EndpointID == endpointID && binding.EventType == eventType && binding.ObjectID == objectID {
			return binding, nil
		}
	}
	return WorkflowCallbackWebhookBinding{}, ErrNotFound
}

func (m *MemStore) DeleteWorkflowCallbackWebhookBinding(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.workflowCallbackWebhookBindings[id]; !ok {
		return ErrNotFound
	}
	delete(m.workflowCallbackWebhookBindings, id)
	return nil
}

const workflowCallbackWebhookBindingColumns = "id, endpoint_id, run_id, step_name, event_type, object_id, created_at"

func scanWorkflowCallbackWebhookBinding(row pgx.Row) (WorkflowCallbackWebhookBinding, error) {
	var binding WorkflowCallbackWebhookBinding
	err := row.Scan(&binding.ID, &binding.EndpointID, &binding.RunID, &binding.StepName, &binding.EventType, &binding.ObjectID, &binding.CreatedAt)
	return binding, err
}

func (s *PgStore) CreateWorkflowCallbackWebhookBinding(ctx context.Context, binding WorkflowCallbackWebhookBinding) (WorkflowCallbackWebhookBinding, error) {
	if err := validateWorkflowCallbackWebhookBinding(binding); err != nil {
		return WorkflowCallbackWebhookBinding{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WorkflowCallbackWebhookBinding{}, fmt.Errorf("state: begin workflow callback webhook binding: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	var runStatus, appID string
	if err := tx.QueryRow(ctx, "SELECT status, app_id FROM workflow_runs WHERE id = $1 FOR UPDATE", binding.RunID).Scan(&runStatus, &appID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkflowCallbackWebhookBinding{}, ErrWorkflowRunNotFound
		}
		return WorkflowCallbackWebhookBinding{}, fmt.Errorf("state: lock callback binding run: %w", err)
	}
	existing, err := scanWorkflowCallbackWebhookBinding(tx.QueryRow(ctx, "SELECT "+workflowCallbackWebhookBindingColumns+" FROM workflow_callback_webhook_bindings WHERE id = $1", binding.ID))
	if err == nil {
		if !sameWorkflowCallbackWebhookBinding(existing, binding) {
			return WorkflowCallbackWebhookBinding{}, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return WorkflowCallbackWebhookBinding{}, fmt.Errorf("state: commit duplicate callback binding: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return WorkflowCallbackWebhookBinding{}, fmt.Errorf("state: inspect callback binding: %w", err)
	}
	if runStatus == WorkflowRunStatusSucceeded || runStatus == WorkflowRunStatusFailed || runStatus == WorkflowRunStatusDead {
		return WorkflowCallbackWebhookBinding{}, ErrWorkflowCallbackClosed
	}
	var callbackCompleted bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM workflow_events WHERE id = $1)", binding.ID).Scan(&callbackCompleted); err != nil {
		return WorkflowCallbackWebhookBinding{}, fmt.Errorf("state: inspect completed callback: %w", err)
	}
	if callbackCompleted {
		return WorkflowCallbackWebhookBinding{}, ErrWorkflowCallbackClosed
	}
	var endpointExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM inbound_webhook_endpoints e
		JOIN apps a ON a.id = e.app_id
		WHERE e.id = $1 AND e.app_id = $2 AND e.account_id = a.account_id AND e.provider = 'stripe'
	)`, binding.EndpointID, appID).Scan(&endpointExists); err != nil {
		return WorkflowCallbackWebhookBinding{}, fmt.Errorf("state: inspect callback binding endpoint: %w", err)
	}
	if !endpointExists {
		return WorkflowCallbackWebhookBinding{}, ErrNotFound
	}
	var stepStatus string
	err = tx.QueryRow(ctx, "SELECT status FROM workflow_steps WHERE run_id = $1 AND step_name = $2", binding.RunID, binding.StepName).Scan(&stepStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return WorkflowCallbackWebhookBinding{}, fmt.Errorf("state: inspect callback binding step: %w", err)
	}
	if err == nil && (stepStatus == WorkflowStepStatusSucceeded || stepStatus == WorkflowStepStatusFailed || stepStatus == WorkflowStepStatusDead || stepStatus == WorkflowStepStatusSkipped) {
		return WorkflowCallbackWebhookBinding{}, ErrWorkflowCallbackClosed
	}
	created, err := scanWorkflowCallbackWebhookBinding(tx.QueryRow(ctx, `INSERT INTO workflow_callback_webhook_bindings
		(id, endpoint_id, run_id, step_name, event_type, object_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+workflowCallbackWebhookBindingColumns,
		binding.ID, binding.EndpointID, binding.RunID, binding.StepName, binding.EventType, binding.ObjectID))
	if err != nil {
		if isUniqueViolation(err) {
			return WorkflowCallbackWebhookBinding{}, ErrConflict
		}
		return WorkflowCallbackWebhookBinding{}, fmt.Errorf("state: insert callback binding: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return WorkflowCallbackWebhookBinding{}, fmt.Errorf("state: commit callback binding: %w", err)
	}
	return created, nil
}

func (s *PgStore) WorkflowCallbackWebhookBindingByID(ctx context.Context, id string) (WorkflowCallbackWebhookBinding, error) {
	binding, err := scanWorkflowCallbackWebhookBinding(s.pool.QueryRow(ctx, "SELECT "+workflowCallbackWebhookBindingColumns+" FROM workflow_callback_webhook_bindings WHERE id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkflowCallbackWebhookBinding{}, ErrNotFound
	}
	if err != nil {
		return WorkflowCallbackWebhookBinding{}, fmt.Errorf("state: get callback binding: %w", err)
	}
	return binding, nil
}

func (s *PgStore) WorkflowCallbackWebhookBindingByMatch(ctx context.Context, endpointID, eventType, objectID string) (WorkflowCallbackWebhookBinding, error) {
	binding, err := scanWorkflowCallbackWebhookBinding(s.pool.QueryRow(ctx, "SELECT "+workflowCallbackWebhookBindingColumns+" FROM workflow_callback_webhook_bindings WHERE endpoint_id = $1 AND event_type = $2 AND object_id = $3", endpointID, eventType, objectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkflowCallbackWebhookBinding{}, ErrNotFound
	}
	if err != nil {
		return WorkflowCallbackWebhookBinding{}, fmt.Errorf("state: match callback binding: %w", err)
	}
	return binding, nil
}

func (s *PgStore) DeleteWorkflowCallbackWebhookBinding(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM workflow_callback_webhook_bindings WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("state: delete callback binding: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

var _ WorkflowCallbackWebhookBindingStore = (*MemStore)(nil)
var _ WorkflowCallbackWebhookBindingStore = (*PgStore)(nil)
