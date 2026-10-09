// adr: 662
package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type OperationRecoveryReceiptStore interface {
	RecoverOperationWithReceipt(context.Context, string, string, api.OperationRecoveryRequest) (api.OperationRecoveryDecision, error)
}

func newOperationRecoveryDecision(op Operation, req api.OperationRecoveryRequest, fingerprint string, now, expiry time.Time) api.OperationRecoveryDecision {
	return api.OperationRecoveryDecision{OperationID: op.ID, RecoveryID: req.RecoveryID, RequestFingerprint: fingerprint, ExpectedGeneration: req.ExpectedGeneration, Generation: op.Generation, ExpectedInspectionRevision: req.ExpectedInspectionRevision, Resolution: req.Resolution, State: op.State, InvocationID: op.CurrentInvocationID, WorkflowRunID: op.WorkflowRunID, JobRunID: op.JobRunID, RecordedAt: now, ExpiresAt: expiry}
}

func (m *MemStore) saveOperationRecoveryDecisionLocked(op Operation, req api.OperationRecoveryRequest, fingerprint string, now, expiry time.Time) {
	data := m.operationMemoryLocked()
	if data.recoveryDecisions == nil {
		data.recoveryDecisions = map[string]api.OperationRecoveryDecision{}
	}
	key := op.ID + "/" + req.RecoveryID
	data.recoveries[key] = fingerprint
	data.recoveryDecisions[key] = newOperationRecoveryDecision(op, req, fingerprint, now, expiry)
}

func checkOperationRecoveryDecision(decision api.OperationRecoveryDecision, now time.Time) error {
	if decision.OperationID == "" {
		return ErrOperationRecoveryReceiptUnavailable
	}
	if !decision.ExpiresAt.After(now) {
		return ErrOperationExpired
	}
	return nil
}

func (m *MemStore) RecoverOperationWithReceipt(ctx context.Context, account, id string, req api.OperationRecoveryRequest) (api.OperationRecoveryDecision, error) {
	empty := api.OperationRecoveryDecision{}
	if _, err := m.RecoverOperation(ctx, account, "", id, req); err != nil {
		return empty, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	op, ok := data.operations[id]
	if !ok || op.AccountID != account {
		return empty, ErrNotFound
	}
	decision := data.recoveryDecisions[id+"/"+req.RecoveryID]
	return decision, checkOperationRecoveryDecision(decision, time.Now().UTC())
}

func (s *PgStore) RecoverOperationWithReceipt(ctx context.Context, account, id string, req api.OperationRecoveryRequest) (api.OperationRecoveryDecision, error) {
	empty := api.OperationRecoveryDecision{}
	if _, err := s.RecoverOperation(ctx, account, "", id, req); err != nil {
		return empty, err
	}
	prior, err := sqlc.New().GetCustomerOperationRecoveryDecision(ctx, s.pool, sqlc.GetCustomerOperationRecoveryDecisionParams{OperationID: mustPgUUID(id), RecoveryID: req.RecoveryID, AccountID: mustPgUUID(account)})
	if err != nil {
		return empty, mapErr(err)
	}
	var decision api.OperationRecoveryDecision
	if len(prior.Decision) > 0 {
		if err := json.Unmarshal(prior.Decision, &decision); err != nil {
			return empty, err
		}
	}
	return decision, checkOperationRecoveryDecision(decision, time.Now().UTC())
}

// A matching retained receipt does not need current code availability. New
// decisions still take the original code/execution locks and recheck ownership.
func (s *PgStore) replayOperationRecovery(ctx context.Context, op Operation, req api.OperationRecoveryRequest) (bool, error) {
	if !op.ExpiresAt.After(time.Now().UTC()) {
		return false, ErrOperationExpired
	}
	fingerprint, err := operationRecoveryFingerprint(req, api.Limits{MaxSourceBytesPerInvocation: op.ValueMaxBytes})
	if err != nil {
		return false, err
	}
	prior, err := sqlc.New().GetCustomerOperationRecoveryDecision(ctx, s.pool, sqlc.GetCustomerOperationRecoveryDecisionParams{OperationID: mustPgUUID(op.ID), RecoveryID: req.RecoveryID, AccountID: mustPgUUID(op.AccountID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if prior.Fingerprint != fingerprint {
		return false, ErrOperationInputConflict
	}
	return true, nil
}
