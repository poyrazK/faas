package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ADR-20261113120824 §2: a code workflow's wf.call records a call operation
// and, in the same transaction, enqueues the callee's async invocation with a
// send record naming the run and the operation. When that invocation is
// terminal, its own transaction settles the operation and wakes the run, so
// no VM waits while the callee runs.

// workflowCallOutcome maps a terminal invocation to the call operation's row:
// succeeded with the callee's result, or failed with its error.
func workflowCallOutcome(inv Invocation) (status string, output json.RawMessage, message *string) {
	if inv.State == InvocationCompleted && (inv.Outcome == nil || *inv.Outcome == OutcomeSuccess) {
		if len(inv.Result) > 0 && json.Valid(inv.Result) {
			output = append(json.RawMessage(nil), inv.Result...)
		} else {
			output = json.RawMessage(`null`)
		}
		return WorkflowStepStatusSucceeded, output, nil
	}
	text := inv.LastError
	if text == "" {
		text = fmt.Sprintf("function call %s", inv.State)
	}
	text = truncateWorkflowCallError(text)
	return WorkflowStepStatusFailed, nil, &text
}

func truncateWorkflowCallError(text string) string {
	const maxBytes = 4096
	if len(text) <= maxBytes {
		return text
	}
	cut := maxBytes
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut]
}

func validateWorkflowCallWrite(runID string, op WorkflowCodeOpWrite) error {
	if op.Call == nil {
		return nil
	}
	call := op.Call
	if !op.New || op.Status != WorkflowStepStatusPending || op.Attempt != 0 {
		return fmt.Errorf("%w: call operation %q must be a new pending operation", ErrWorkflowInvalidRecord, op.StepName)
	}
	if call.ID == "" || call.AppID == "" || call.AccountID == "" || call.SentByAppID == "" ||
		call.WorkflowCallRunID != runID || call.WorkflowCallOp != op.StepName {
		return fmt.Errorf("%w: call operation %q needs its invocation, callee, sender, run and operation", ErrWorkflowInvalidRecord, op.StepName)
	}
	return nil
}

// enqueueWorkflowCallTx writes a call operation's invocation and send record
// in the transaction that records the operation.
func enqueueWorkflowCallTx(ctx context.Context, tx pgx.Tx, call Invocation) error {
	if _, err := enqueueInvocationRow(ctx, tx, call); err != nil {
		return fmt.Errorf("pgstore: enqueue workflow call %q: %w", call.WorkflowCallOp, err)
	}
	if err := insertInvocationSendDB(ctx, tx, call); err != nil {
		return fmt.Errorf("pgstore: record workflow call %q: %w", call.WorkflowCallOp, err)
	}
	return nil
}

// settleWorkflowCallTx settles the call operation a terminal invocation ran
// for and wakes its run. The run lock is taken first, the engine's order. A
// run that already moved on (timed-out call, closed run) is left alone.
func settleWorkflowCallTx(ctx context.Context, tx pgx.Tx, inv Invocation, runID, op string) error {
	var runUUID pgtype.UUID
	if err := runUUID.Scan(runID); err != nil {
		return nil //nolint:nilerr // a malformed run id names no run to settle
	}
	q := sqlc.New()
	if _, err := q.LockWorkflowRunForCallSettle(ctx, tx, runUUID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("pgstore: lock workflow run for call: %w", err)
	}
	status, output, message := workflowCallOutcome(inv)
	rows, err := q.SettleWorkflowCallOp(ctx, tx, sqlc.SettleWorkflowCallOpParams{
		Status: status, Output: output, Error: pgTextPtr(message), RunID: runUUID, StepName: op, InvocationID: inv.ID,
	})
	if err != nil {
		return fmt.Errorf("pgstore: settle workflow call %q: %w", op, err)
	}
	if rows == 0 {
		return nil
	}
	if err := q.WakeWorkflowRunForCall(ctx, tx, runUUID); err != nil {
		return fmt.Errorf("pgstore: wake workflow run for call: %w", err)
	}
	return nil
}

// enqueueWorkflowCallLocked mirrors enqueueWorkflowCallTx; the caller holds
// m.mu.
func (m *MemStore) enqueueWorkflowCallLocked(call Invocation) error {
	if _, err := m.enqueueInvocationLocked(call); err != nil {
		return err
	}
	m.recordInvocationSendLocked(call)
	return nil
}

// settleWorkflowCallLocked mirrors settleWorkflowCallTx; the caller holds
// m.mu.
func (m *MemStore) settleWorkflowCallLocked(inv Invocation, send InvocationSend) {
	steps := m.workflowSteps[send.WorkflowRunID]
	step, ok := steps[send.WorkflowOp]
	if !ok || (step.Status != WorkflowStepStatusPending && step.Status != WorkflowStepStatusAwaitingEvent) {
		return
	}
	var descriptor struct {
		Op struct {
			InvocationID string `json:"invocation_id"`
		} `json:"gregale_code_op"`
	}
	if json.Unmarshal(step.Input, &descriptor) != nil || descriptor.Op.InvocationID != inv.ID {
		return
	}
	now := time.Now().UTC()
	status, output, message := workflowCallOutcome(inv)
	step.Status, step.Output, step.Error, step.FinishedAt = status, output, message, &now
	steps[send.WorkflowOp] = step
	if run, ok := m.workflowRuns[send.WorkflowRunID]; ok && run.Status == WorkflowRunStatusAwaitingEvent {
		run.Status, run.ScheduledFor, run.UpdatedAt = WorkflowRunStatusPending, now, now
		m.workflowRuns[send.WorkflowRunID] = run
	}
}

func pgTextPtr(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}
