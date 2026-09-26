package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

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
