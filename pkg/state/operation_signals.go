// adr: 1197
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	// ErrOperationSignalUnexpected: the Operation's workflow never waits for
	// this event, so the signal could only sit unused.
	ErrOperationSignalUnexpected = errors.New("state: operation does not wait for this event")
	// ErrOperationSignalConflict: the signal ID was used with another event
	// or payload.
	ErrOperationSignalConflict = errors.New("state: operation signal id reused with different content")
	// ErrOperationNotActive: the Operation has settled; it takes no signals.
	ErrOperationNotActive = errors.New("state: operation is not active")
)

type OperationSignalInput struct {
	AccountID   string
	OperationID string
	SignalID    string
	Event       string
	Payload     json.RawMessage
	// Source records who delivered the signal (ADR-20261017100853); empty means the
	// account. It is history, not identity: a repeat from another source is
	// still the same signal.
	Source string
}

// OperationSignalStore delivers named events to Operations waiting on them.
// OperationsForSubject lists active Operations of one name about one subject.
type OperationSignalStore interface {
	SignalOperation(ctx context.Context, in OperationSignalInput) (api.OperationSignalReceipt, error)
	// tenantID, when set, keeps the match inside one platform tenant.
	OperationsForSubject(ctx context.Context, accountID, appID, tenantID, scope, name string, subject api.OperationSubject, limit int) ([]string, error)
}

func validateOperationSignal(in *OperationSignalInput) error {
	switch in.Source {
	case "":
		in.Source = api.OperationSignalSourceAccount
	case api.OperationSignalSourceAccount, api.OperationSignalSourceCustomer, api.OperationSignalSourceWebhook:
	default:
		return fmt.Errorf("%w: unknown signal source", ErrInvalidArgument)
	}
	if in.SignalID == "" || len(in.SignalID) > api.OperationSignalIDMaxBytes || strings.IndexFunc(in.SignalID, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: signal_id must be 1-%d printable bytes", ErrInvalidArgument, api.OperationSignalIDMaxBytes)
	}
	if !operations.ValidOperationSignalEvent(in.Event) {
		return fmt.Errorf("%w: event must be a bounded lowercase name", ErrInvalidArgument)
	}
	if len(in.Payload) == 0 {
		in.Payload = json.RawMessage(`{}`)
	}
	if len(in.Payload) > api.OperationSignalPayloadMaxBytes {
		return NewOperationLimitError("signal_payload_bytes", api.OperationSignalPayloadMaxBytes, int64(len(in.Payload)))
	}
	canonical, err := operations.CanonicalJSON(in.Payload)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	in.Payload = canonical
	return nil
}

// operationSignalEventID makes a signal idempotent per Operation.
func operationSignalEventID(operationID, signalID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("gregale-operation-signal-v1\x00"+operationID+"\x00"+signalID)).String()
}

// operationSignalCheck admits a new signal: the Operation is active and its
// workflow waits for the event somewhere.
func operationSignalCheck(op Operation, run WorkflowRun, event string) error {
	if op.WorkflowRunID == "" {
		return ErrOperationSignalUnexpected
	}
	if op.State != api.OperationAccepted && op.State != api.OperationRunning && op.State != api.OperationWaiting {
		return ErrOperationNotActive
	}
	var spec api.WorkflowSpec
	if json.Unmarshal(run.DefinitionSnapshot, &spec) != nil || !operations.OperationSignalEvents(spec)[event] {
		return ErrOperationSignalUnexpected
	}
	return nil
}

func operationSignalReceipt(op Operation, in OperationSignalInput, duplicate bool) api.OperationSignalReceipt {
	waiting := op.State == api.OperationWaiting && op.Waiting != nil && op.Waiting.Kind == api.OperationWaitEvent && op.Waiting.Event == in.Event
	return api.OperationSignalReceipt{OperationID: op.ID, SignalID: in.SignalID, Event: in.Event, Duplicate: duplicate, Waiting: waiting}
}

func (s *PgStore) SignalOperation(ctx context.Context, in OperationSignalInput) (api.OperationSignalReceipt, error) {
	if err := validateOperationSignal(&in); err != nil {
		return api.OperationSignalReceipt{}, err
	}
	snapshot, err := s.OperationByID(ctx, in.AccountID, "", in.OperationID)
	if err != nil {
		return api.OperationSignalReceipt{}, err
	}
	if snapshot.WorkflowRunID == "" {
		return api.OperationSignalReceipt{}, ErrOperationSignalUnexpected
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.OperationSignalReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	runID := mustPgUUID(snapshot.WorkflowRunID)
	// Lock order matches cancellation and approvals: run, then operation.
	if _, err := q.LockWorkflowRecovery(ctx, tx, runID); err != nil {
		return api.OperationSignalReceipt{}, mapErr(err)
	}
	raw, err := q.LockCustomerOperationForWorkflow(ctx, tx, runID)
	if err != nil {
		return api.OperationSignalReceipt{}, mapErr(err)
	}
	op, err := operationPGRecord(raw)
	if err != nil {
		return api.OperationSignalReceipt{}, err
	}
	id := operationSignalEventID(op.ID, in.SignalID)
	var priorEvent string
	var priorPayload json.RawMessage
	err = tx.QueryRow(ctx, `SELECT event_name, payload FROM workflow_events WHERE id = $1`, id).Scan(&priorEvent, &priorPayload)
	if err == nil {
		if priorEvent != in.Event || !equalWorkflowJSON(priorPayload, in.Payload) {
			return api.OperationSignalReceipt{}, ErrOperationSignalConflict
		}
		return operationSignalReceipt(op, in, true), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.OperationSignalReceipt{}, fmt.Errorf("state: read operation signal: %w", err)
	}
	row, err := q.ReadCustomerOperationWorkflowRun(ctx, tx, runID)
	if err != nil {
		return api.OperationSignalReceipt{}, err
	}
	run := *workflowRunFromSQLC(row)
	if err := operationSignalCheck(op, run, in.Event); err != nil {
		return api.OperationSignalReceipt{}, err
	}
	receipt := operationSignalReceipt(op, in, false)
	if err := insertWorkflowEventTx(ctx, tx, "", &WorkflowEvent{ID: id, RunID: op.WorkflowRunID, EventName: in.Event, Payload: in.Payload}); err != nil {
		return api.OperationSignalReceipt{}, err
	}
	event := operationWorkflowEvent(&op, run, "signal_received", map[string]any{"signal_id": in.SignalID, "event": in.Event, "waiting": receipt.Waiting, "source": in.Source}, time.Now().UTC())
	if err := operationSaveTx(ctx, tx, op, event); err != nil {
		return api.OperationSignalReceipt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.OperationSignalReceipt{}, fmt.Errorf("state: commit operation signal: %w", err)
	}
	return receipt, nil
}

func (s *PgStore) OperationsForSubject(ctx context.Context, accountID, appID, tenantID, scope, name string, subject api.OperationSubject, limit int) ([]string, error) {
	a, err := operationUUID(accountID)
	if err != nil {
		return nil, ErrNotFound
	}
	app, err := operationUUID(appID)
	if err != nil {
		return nil, ErrNotFound
	}
	ids, err := sqlc.New().ActiveCustomerOperationsForSubject(ctx, s.pool, sqlc.ActiveCustomerOperationsForSubjectParams{
		AccountID: a, AppID: app, Scope: scope, Name: name, SubjectType: subject.Type, SubjectID: subject.ID, PageLimit: int32(limit),
		TenantID: mustPgUUID(tenantID),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list subject operations: %w", err)
	}
	return ids, nil
}

func (m *MemStore) SignalOperation(_ context.Context, in OperationSignalInput) (api.OperationSignalReceipt, error) {
	if err := validateOperationSignal(&in); err != nil {
		return api.OperationSignalReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	op, ok := data.operations[in.OperationID]
	if !ok || op.AccountID != in.AccountID {
		return api.OperationSignalReceipt{}, ErrNotFound
	}
	if op.WorkflowRunID == "" {
		return api.OperationSignalReceipt{}, ErrOperationSignalUnexpected
	}
	id := operationSignalEventID(op.ID, in.SignalID)
	for _, prior := range m.workflowEvents[op.WorkflowRunID] {
		if prior.ID != id {
			continue
		}
		if prior.EventName != in.Event || !equalWorkflowJSON(prior.Payload, in.Payload) {
			return api.OperationSignalReceipt{}, ErrOperationSignalConflict
		}
		return operationSignalReceipt(op, in, true), nil
	}
	run := m.workflowRuns[op.WorkflowRunID]
	if err := operationSignalCheck(op, run, in.Event); err != nil {
		return api.OperationSignalReceipt{}, err
	}
	receipt := operationSignalReceipt(op, in, false)
	if err := m.insertWorkflowEventLocked("", &WorkflowEvent{ID: id, RunID: op.WorkflowRunID, EventName: in.Event, Payload: in.Payload}); err != nil {
		return api.OperationSignalReceipt{}, err
	}
	op = cloneOperation(op)
	event := operationWorkflowEvent(&op, run, "signal_received", map[string]any{"signal_id": in.SignalID, "event": in.Event, "waiting": receipt.Waiting, "source": in.Source}, time.Now().UTC())
	m.operationSaveLocked(op, event)
	return receipt, nil
}

func (m *MemStore) OperationsForSubject(_ context.Context, accountID, appID, tenantID, scope, name string, subject api.OperationSubject, limit int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	matches := []Operation{}
	for _, op := range data.operations {
		active := op.State == api.OperationAccepted || op.State == api.OperationRunning || op.State == api.OperationWaiting
		if active && op.AccountID == accountID && op.AppID == appID && (tenantID == "" || op.PlatformTenantID == tenantID) && op.Scope == scope && op.Name == name && op.Subject != nil && *op.Subject == subject {
			matches = append(matches, op)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if !matches[i].CreatedAt.Equal(matches[j].CreatedAt) {
			return matches[i].CreatedAt.Before(matches[j].CreatedAt)
		}
		return matches[i].ID < matches[j].ID
	})
	ids := []string{}
	for _, op := range matches[:min(len(matches), limit)] {
		ids = append(ids, op.ID)
	}
	return ids, nil
}

var (
	_ OperationSignalStore = (*PgStore)(nil)
	_ OperationSignalStore = (*MemStore)(nil)
)
