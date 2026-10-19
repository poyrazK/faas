// adr: 1194
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	// ErrOperationApproverMismatch means the caller is not the declared approver kind.
	ErrOperationApproverMismatch = errors.New("state: caller is not the declared operation approver")
	// ErrOperationApprovalDecided means a different decision was already recorded.
	ErrOperationApprovalDecided = errors.New("state: operation approval already decided differently")
	// ErrOperationNotAwaitingApproval means the operation is not waiting on that approval.
	ErrOperationNotAwaitingApproval = errors.New("state: operation is not waiting for this approval")
)

var operationApprovalStep = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// OperationApprovalInput is one decision. PlatformTenantID scopes a customer
// caller; it is empty for the account. ApproverKind is the caller's kind.
type OperationApprovalInput struct {
	AccountID          string
	PlatformTenantID   string
	OperationID        string
	Step               string
	ApproverKind       string
	ExpectedGeneration int
	Decision           string
	Comment            string
	Actor              api.OperationApprovalActor
	// Input is the approver's form input (ADR-20261019001500); only apid's
	// gated decision path sets it.
	Input json.RawMessage
}

// OperationApprovalStore records approval decisions for waiting operations.
type OperationApprovalStore interface {
	DecideOperationApproval(context.Context, OperationApprovalInput) (Operation, error)
}

func validateOperationApproval(in OperationApprovalInput) error {
	if in.ExpectedGeneration < 1 || !operationApprovalStep.MatchString(in.Step) || len(in.Comment) > api.OperationApprovalCommentMaxBytes ||
		in.Decision != api.OperationApprovalApproved && in.Decision != api.OperationApprovalRejected ||
		in.ApproverKind != api.OperationApproverAccount && in.ApproverKind != api.OperationApproverCustomer || in.Actor.Kind != in.ApproverKind ||
		len(in.Input) > api.OperationApprovalInputMaxBytes {
		return ErrInvalidArgument
	}
	return nil
}

// operationApprovalPayload is written only by this path; generic callback
// completion is refused for operation-linked runs, so it cannot be forged.
func operationApprovalPayload(in OperationApprovalInput) json.RawMessage {
	if len(in.Input) > 0 {
		raw, _ := json.Marshal(api.OperationApprovalInputPayload{Approval: api.OperationApprovalInputDecision{Step: in.Step, Decision: in.Decision, Actor: in.Actor, Comment: in.Comment, Input: in.Input}})
		return raw
	}
	raw, _ := json.Marshal(api.OperationApprovalPayload{Approval: api.OperationApprovalDecision{Step: in.Step, Decision: in.Decision, Actor: in.Actor, Comment: in.Comment}})
	return raw
}

func operationApprovalEventData(in OperationApprovalInput) map[string]any {
	data := map[string]any{"step": in.Step, "decision": in.Decision, "actor": in.Actor}
	if in.Comment != "" {
		data["comment"] = in.Comment
	}
	return data
}

// checkOperationApproval validates the declared approver and current wait.
// awaitingRequired is false when an identical decision already exists.
func checkOperationApproval(op Operation, def OperationDefinition, in OperationApprovalInput, awaitingRequired bool, now time.Time) error {
	approval, ok := operations.ApprovalFor(def.Spec, in.Step)
	if !ok || op.WorkflowRunID == "" {
		return ErrNotFound
	}
	if approval.Approver != in.ApproverKind {
		return ErrOperationApproverMismatch
	}
	if op.Generation != in.ExpectedGeneration {
		return ErrConflict
	}
	if !awaitingRequired {
		return nil
	}
	if op.State != api.OperationWaiting || op.Waiting == nil || op.Waiting.Kind != api.OperationWaitApproval || op.Waiting.Step != in.Step || op.CancellationRequested {
		return ErrOperationNotAwaitingApproval
	}
	if op.Waiting.Until != nil && !now.Before(*op.Waiting.Until) {
		return ErrWorkflowCallbackExpired
	}
	return nil
}

func (s *PgStore) DecideOperationApproval(ctx context.Context, in OperationApprovalInput) (Operation, error) {
	if err := validateOperationApproval(in); err != nil {
		return Operation{}, err
	}
	snapshot, err := s.OperationByID(ctx, in.AccountID, in.PlatformTenantID, in.OperationID)
	if err != nil {
		return Operation{}, err
	}
	def, err := s.OperationDefinitionByID(ctx, snapshot.AccountID, snapshot.DefinitionID)
	if err != nil {
		return Operation{}, err
	}
	if snapshot.WorkflowRunID == "" {
		return Operation{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	runID := mustPgUUID(snapshot.WorkflowRunID)
	// Lock order matches cancellation: run, then operation.
	if _, err := q.LockWorkflowRecovery(ctx, tx, runID); err != nil {
		return Operation{}, mapErr(err)
	}
	raw, err := q.LockCustomerOperationForWorkflow(ctx, tx, runID)
	if err != nil {
		return Operation{}, mapErr(err)
	}
	op, err := operationPGRecord(raw)
	if err != nil {
		return Operation{}, err
	}
	payload := operationApprovalPayload(in)
	eventID := api.WorkflowCallbackID(op.WorkflowRunID, in.Step)
	var prior json.RawMessage
	err = tx.QueryRow(ctx, `SELECT payload FROM workflow_events WHERE id = $1`, eventID).Scan(&prior)
	if err == nil {
		if err := checkOperationApproval(op, def, in, false, time.Now().UTC()); err != nil {
			return Operation{}, err
		}
		if !equalWorkflowJSON(prior, payload) {
			return Operation{}, ErrOperationApprovalDecided
		}
		return s.OperationByID(ctx, in.AccountID, in.PlatformTenantID, in.OperationID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, fmt.Errorf("state: read operation approval: %w", err)
	}
	now := time.Now().UTC()
	if err := checkOperationApproval(op, def, in, true, now); err != nil {
		return Operation{}, err
	}
	form, err := operationApprovalFormTx(ctx, tx, op.AccountID, def, in.Step)
	if err != nil {
		return Operation{}, err
	}
	if err := checkOperationApprovalInput(form, in, op.PlanLimits); err != nil {
		return Operation{}, err
	}
	row, err := q.ReadCustomerOperationWorkflowRun(ctx, tx, runID)
	if err != nil {
		return Operation{}, err
	}
	run := *workflowRunFromSQLC(row)
	wait := operationWorkflowWaits(run)[in.Step]
	if _, err := completeWorkflowCallbackTx(ctx, tx, op.PlatformTenantID, op.WorkflowRunID, in.Step, api.WorkflowCallbackEventName(op.WorkflowRunID, in.Step), eventID, wait.Duration, payload); err != nil {
		return Operation{}, err
	}
	event := operationWorkflowEvent(&op, run, "approval_decided", operationApprovalEventData(in), now)
	if err := operationSaveTx(ctx, tx, op, event); err != nil {
		return Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Operation{}, fmt.Errorf("state: commit operation approval: %w", err)
	}
	return s.OperationByID(ctx, in.AccountID, in.PlatformTenantID, in.OperationID)
}

func (m *MemStore) DecideOperationApproval(_ context.Context, in OperationApprovalInput) (Operation, error) {
	if err := validateOperationApproval(in); err != nil {
		return Operation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	op, ok := data.operations[in.OperationID]
	if !ok || op.AccountID != in.AccountID || in.PlatformTenantID != "" && op.PlatformTenantID != in.PlatformTenantID {
		return Operation{}, ErrNotFound
	}
	def, ok := data.definitions[op.DefinitionID]
	if !ok || op.WorkflowRunID == "" {
		return Operation{}, ErrNotFound
	}
	payload := operationApprovalPayload(in)
	eventID := api.WorkflowCallbackID(op.WorkflowRunID, in.Step)
	for _, event := range m.workflowEvents[op.WorkflowRunID] {
		if event.ID != eventID {
			continue
		}
		if err := checkOperationApproval(op, def, in, false, time.Now().UTC()); err != nil {
			return Operation{}, err
		}
		if !equalWorkflowJSON(event.Payload, payload) {
			return Operation{}, ErrOperationApprovalDecided
		}
		return cloneOperation(m.operationDeliveryLocked(op)), nil
	}
	now := time.Now().UTC()
	if err := checkOperationApproval(op, def, in, true, now); err != nil {
		return Operation{}, err
	}
	if err := checkOperationApprovalInput(m.operationApprovalFormLocked(op.AccountID, def, in.Step), in, op.PlanLimits); err != nil {
		return Operation{}, err
	}
	run := m.workflowRuns[op.WorkflowRunID]
	wait := operationWorkflowWaits(run)[in.Step]
	if _, err := m.completeWorkflowCallbackLocked(op.PlatformTenantID, run.ID, in.Step, api.WorkflowCallbackEventName(run.ID, in.Step), eventID, wait.Duration, payload); err != nil {
		return Operation{}, err
	}
	event := operationWorkflowEvent(&op, run, "approval_decided", operationApprovalEventData(in), now)
	m.operationSaveLocked(op, event)
	return cloneOperation(m.operationDeliveryLocked(m.operationData.operations[op.ID])), nil
}
