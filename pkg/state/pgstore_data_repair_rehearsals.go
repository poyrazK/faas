package state

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) EnqueueDataRepairRehearsal(ctx context.Context, account, id string, sequence int64, checks string) (Operation, DataRepairRehearsal, error) {
	var task DataRepairRehearsal
	if !repairHash.MatchString(checks) {
		return Operation{}, task, ErrInvalidArgument
	}
	return s.enqueueDataRepairRehearsal(ctx, account, id, sequence, checks)
}

func (s *PgStore) enqueueDataRepairRehearsal(ctx context.Context, account, id string, sequence int64, checks string) (Operation, DataRepairRehearsal, error) {
	var task DataRepairRehearsal
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Operation{}, task, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	raw, err := sqlc.New().LockDataRepairOperation(ctx, tx, sqlc.LockDataRepairOperationParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account)})
	if err != nil {
		return Operation{}, task, mapErr(err)
	}
	op, err := operationPGRecord(raw)
	if err != nil {
		return op, task, err
	}
	now := time.Now().UTC()
	event, err := advanceDataRepair(&op, DataRepairAdvance{ExpectedSequence: sequence, Phase: api.DataRepairRehearsing}, now)
	if err != nil {
		return op, task, err
	}
	task = DataRepairRehearsal{ID: uuid.NewString(), OperationID: op.ID, AccountID: account, Scope: core.Scope{AccountID: account, AppID: op.AppID, Environment: op.Scope, PlatformTenantID: op.PlatformTenantID}, Binding: op.DataRepairBinding, PlanID: op.DataRepair.PlanID, PlanDigest: op.DataRepair.PlanDigest, ChecksSHA256: checks, PlanExpiresAt: op.DataRepair.PlanExpiresAt, Phase: RepairRehearsalQueued, CreatedAt: now, UpdatedAt: now, PhaseStartedAt: now}
	op.DataRepair.RehearsalTaskID, op.DataRepair.RehearsalStatus = task.ID, task.Phase
	op.DataRepair.RehearsalCancelRequested = false
	event.Data, _ = json.Marshal(map[string]any{"state": op.State, "data_repair": op.DataRepair})
	b, _ := json.Marshal(task)
	if err = sqlc.New().InsertDataRepairRehearsal(ctx, tx, sqlc.InsertDataRepairRehearsalParams{ID: mustPgUUID(task.ID), OperationID: mustPgUUID(id), AccountID: mustPgUUID(account), Record: b}); err != nil {
		return op, task, mapErr(err)
	}
	if err = saveDataRepairTx(ctx, tx, op, event); err != nil {
		return op, task, err
	}
	return op, task, tx.Commit(ctx)
}

func (s *PgStore) DataRepairRehearsal(ctx context.Context, id string) (DataRepairRehearsal, error) {
	b, err := sqlc.New().GetDataRepairRehearsal(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return DataRepairRehearsal{}, mapErr(err)
	}
	return rehearsalFromJSON(b)
}

func (s *PgStore) ListDataRepairRehearsals(ctx context.Context, queued bool, node, after string, limit int) ([]DataRepairRehearsal, error) {
	if limit < 1 || limit > api.DataRepairRehearsalPageSize {
		return nil, ErrInvalidArgument
	}
	if after == "" {
		after = uuid.Nil.String()
	}
	rows, err := sqlc.New().ListDataRepairRehearsals(ctx, s.pool, sqlc.ListDataRepairRehearsalsParams{AfterID: mustPgUUID(after), Queued: queued, NodeID: mustPgUUID(node), PageSize: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]DataRepairRehearsal, 0, len(rows))
	for _, b := range rows {
		t, e := rehearsalFromJSON(b)
		if e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, nil
}

// Every task mutation locks the Operation before its task, preserving the same
// order as admission/cancellation. Only a trusted scheduler calls these methods.
func (s *PgStore) mutateRehearsal(ctx context.Context, id, node string, change func(*DataRepairRehearsal, *Operation, time.Time) error) (Operation, DataRepairRehearsal, error) {
	initial, err := s.DataRepairRehearsal(ctx, id)
	if err != nil {
		return Operation{}, initial, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Operation{}, initial, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if node != "" {
		if err = q.LockDataRepairRehearsalNode(ctx, tx, node); err != nil {
			return Operation{}, initial, err
		}
		count, e := q.CountDataRepairRehearsalNode(ctx, tx, mustPgUUID(node))
		if e != nil {
			return Operation{}, initial, e
		}
		if count >= api.DataRepairRehearsalsPerNode {
			return Operation{}, initial, ErrConflict
		}
	}
	raw, err := q.LockDataRepairOperation(ctx, tx, sqlc.LockDataRepairOperationParams{ID: mustPgUUID(initial.OperationID), AccountID: mustPgUUID(initial.AccountID)})
	if err != nil {
		return Operation{}, initial, mapErr(err)
	}
	op, err := operationPGRecord(raw)
	if err != nil {
		return op, initial, err
	}
	raw, err = q.LockDataRepairRehearsal(ctx, tx, mustPgUUID(id))
	if err != nil {
		return op, initial, mapErr(err)
	}
	task, err := rehearsalFromJSON(raw)
	if err != nil {
		return op, task, err
	}
	if op.DataRepair == nil || op.DataRepair.RehearsalTaskID != id {
		return op, task, ErrConflict
	}
	now := time.Now().UTC()
	beforeTask, _ := json.Marshal(task)
	beforeOp, _ := operationRecordJSON(op)
	beforePhase := task.Phase
	if err = change(&task, &op, now); err != nil {
		return op, task, err
	}
	if task.Phase != beforePhase {
		task.PhaseStartedAt = now
	}
	afterTask, _ := json.Marshal(task)
	afterOp, _ := operationRecordJSON(op)
	if bytes.Equal(beforeTask, afterTask) && bytes.Equal(beforeOp, afterOp) {
		return op, task, nil
	}
	task.UpdatedAt = now
	b, _ := json.Marshal(task)
	p := sqlc.SaveDataRepairRehearsalParams{ID: mustPgUUID(id), Phase: task.Phase, CancelRequested: task.CancelRequested, Record: b}
	if task.Frame != nil {
		p.NodeID = mustPgUUID(task.Frame.NodeID)
		p.Deadline = operationPGTime(&task.Frame.Deadline)
	}
	if err = q.SaveDataRepairRehearsal(ctx, tx, p); err != nil {
		return op, task, mapErr(err)
	}
	event := rehearsalOperationEvent(&op, task, now)
	if err = saveDataRepairTx(ctx, tx, op, event); err != nil {
		return op, task, err
	}
	return op, task, tx.Commit(ctx)
}

func (s *PgStore) ClaimDataRepairRehearsal(ctx context.Context, id string, f core.RehearsalFrame) (DataRepairRehearsal, error) {
	if f.Validate() != nil || f.SnapshotSHA256 != "" {
		return DataRepairRehearsal{}, ErrInvalidArgument
	}
	_, t, err := s.mutateRehearsal(ctx, id, f.NodeID, func(t *DataRepairRehearsal, op *Operation, now time.Time) error {
		if t.Phase != RepairRehearsalQueued || t.CancelRequested || !t.Matches(f) || !now.Before(f.Deadline) || f.Deadline.After(now.Add(time.Duration(api.DataRepairTimeoutSeconds)*time.Second)) || op.DataRepair.Phase != api.DataRepairRehearsing {
			return ErrConflict
		}
		t.Frame = &f
		t.Phase = RepairRehearsalRunning
		return nil
	})
	return t, err
}

func (s *PgStore) DispatchDataRepairRehearsal(ctx context.Context, id, hash string) (DataRepairRehearsal, error) {
	if !repairHash.MatchString(hash) {
		return DataRepairRehearsal{}, ErrInvalidArgument
	}
	_, t, err := s.mutateRehearsal(ctx, id, "", func(t *DataRepairRehearsal, _ *Operation, now time.Time) error {
		if t.Phase != RepairRehearsalRunning || t.Frame == nil || t.Frame.SnapshotSHA256 != "" || t.CancelRequested || !now.Before(t.Frame.Deadline) {
			return ErrConflict
		}
		t.Frame.SnapshotSHA256 = hash
		return nil
	})
	return t, err
}

func (s *PgStore) MarkDataRepairRehearsalCleanup(ctx context.Context, id string) (DataRepairRehearsal, error) {
	_, t, err := s.mutateRehearsal(ctx, id, "", func(t *DataRepairRehearsal, _ *Operation, _ time.Time) error {
		if t.Phase != RepairRehearsalRunning && t.Phase != RepairRehearsalCleanup {
			return ErrConflict
		}
		t.Phase = RepairRehearsalCleanup
		t.FailureCode = "repair_cleanup_unconfirmed"
		return nil
	})
	return t, err
}

func finishRehearsalFailure(t *DataRepairRehearsal, op *Operation, now time.Time) {
	t.Phase, t.FailureCode = RepairRehearsalFailed, "repair_rehearsal_failed"
	if t.CancelRequested {
		t.Phase, t.FailureCode = RepairRehearsalCancelled, "repair_rehearsal_cancelled"
	}
	op.DataRepair.Phase, op.State = api.DataRepairPrepared, api.OperationAccepted
	op.DataRepair.PhaseStartedAt = now
	op.Waiting = nil
	if !now.Before(t.PlanExpiresAt) {
		op.DataRepair.Phase, op.State, op.FailureCode = api.DataRepairFailed, api.OperationFailed, "repair_plan_expired"
	}
}

func (s *PgStore) RetireDataRepairRehearsal(ctx context.Context, id string, e *core.Evidence) (DataRepairRehearsal, error) {
	_, t, err := s.mutateRehearsal(ctx, id, "", func(t *DataRepairRehearsal, op *Operation, now time.Time) error {
		if t.Terminal() {
			return nil
		}
		if t.Phase != RepairRehearsalRunning && t.Phase != RepairRehearsalCleanup && t.Phase != RepairRehearsalQueued {
			return ErrConflict
		}
		if e != nil && (t.Frame == nil || !t.Frame.ValidOutcome(*e)) {
			return ErrInvalidArgument
		}
		if e == nil || t.CancelRequested || !now.Before(t.PlanExpiresAt) {
			finishRehearsalFailure(t, op, now)
			return nil
		}
		t.Evidence = e
		t.FailureCode = ""
		t.Phase = RepairRehearsalPublishing
		return nil
	})
	return t, err
}

func (s *PgStore) CompleteDataRepairRehearsal(ctx context.Context, id string) (Operation, error) {
	op, _, err := s.mutateRehearsal(ctx, id, "", func(t *DataRepairRehearsal, op *Operation, now time.Time) error {
		if t.Phase == RepairRehearsalSucceeded {
			return nil
		}
		if t.Phase == RepairRehearsalFailed && t.FailureCode == "repair_checks_failed" && t.Evidence != nil && !t.Evidence.Passed && t.Frame != nil && t.Frame.ValidOutcome(*t.Evidence) {
			return nil
		}
		if t.Phase != RepairRehearsalPublishing || t.Frame == nil || t.Evidence == nil || !t.Frame.ValidOutcome(*t.Evidence) {
			return ErrConflict
		}
		if t.CancelRequested || !now.Before(t.PlanExpiresAt) {
			finishRehearsalFailure(t, op, now)
			return nil
		}
		if !t.Evidence.Passed {
			finishRehearsalFailure(t, op, now)
			t.FailureCode = "repair_checks_failed"
			return nil
		}
		// Atomic projection with the authoritative application evidence already recorded.
		op.DataRepair.Phase, op.State = api.DataRepairAwaitingApproval, api.OperationWaiting
		op.DataRepair.PhaseStartedAt = now
		op.Waiting = &api.OperationWaitingState{Kind: api.OperationWaitApproval, Step: "repair", Since: now, Approver: api.OperationApproverAccount, Until: &op.DataRepair.PlanExpiresAt}
		t.Phase = RepairRehearsalSucceeded
		return nil
	})
	return op, err
}

func (s *PgStore) CancelDataRepairRehearsal(ctx context.Context, account, operation string, sequence int64) (Operation, error) {
	op, err := s.OperationByID(ctx, account, "", operation)
	if err != nil {
		return op, err
	}
	if op.DataRepair == nil || op.DataRepair.RehearsalTaskID == "" {
		return op, ErrNotFound
	}
	op, _, err = s.mutateRehearsal(ctx, op.DataRepair.RehearsalTaskID, "", func(t *DataRepairRehearsal, op *Operation, now time.Time) error {
		if op.LatestSequence != sequence || op.AccountID != account || t.Terminal() {
			return ErrConflict
		}
		t.CancelRequested = true
		if t.Phase == RepairRehearsalQueued {
			finishRehearsalFailure(t, op, now)
		}
		return nil
	})
	return op, err
}

var _ DataRepairRehearsalStore = (*PgStore)(nil)
