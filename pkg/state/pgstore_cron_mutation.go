package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) CronMutationReceipt(ctx context.Context, accountID, appID, requestID, proof string) (api.CronMutationReceipt, error) {
	app, err := s.AppByID(ctx, appID)
	if err != nil || app.AccountID != accountID || app.Status == AppDeleted {
		return api.CronMutationReceipt{}, ErrNotFound
	}
	data, err := sqlc.New().ReadCronMutationRecord(ctx, s.pool, sqlc.ReadCronMutationRecordParams{AppID: appID, AccountID: accountID, RequestID: requestID})
	if err != nil {
		return api.CronMutationReceipt{}, mapErr(err)
	}
	var saved cronMutationRecord
	if err := json.Unmarshal(data, &saved); err != nil {
		return api.CronMutationReceipt{}, err
	}
	if saved.Receipt.AppID != appID || saved.Receipt.RequestID != requestID || !envBatchProofMatches(saved.ProofHash, proof) {
		return api.CronMutationReceipt{}, ErrNotFound
	}
	current, err := s.AppByID(ctx, appID)
	if err != nil || current.AccountID != accountID || current.Status == AppDeleted {
		return api.CronMutationReceipt{}, ErrNotFound
	}
	return saved.Receipt, nil
}

func (s *PgStore) ApplyCronMutation(ctx context.Context, accountID, appID string, request api.CronMutationRequest) (api.CronMutationReceipt, error) {
	now := time.Now().UTC()
	want, err := newCronMutation(appID, request, now)
	if err != nil {
		return api.CronMutationReceipt{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return api.CronMutationReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	// Match native cron writers' lock order: namespace, app, cron, dependents.
	if _, err := q.LockCronAccountForApp(ctx, tx, appID); err != nil {
		return api.CronMutationReceipt{}, mapErr(err)
	}
	if _, err := q.LockCronMutationApp(ctx, tx, sqlc.LockCronMutationAppParams{AppID: appID, AccountID: accountID}); err != nil {
		return api.CronMutationReceipt{}, mapErr(err)
	}
	if !time.Now().Before(request.ExpiresAt) {
		return api.CronMutationReceipt{}, ErrCronReviewExpired
	}
	r, exists, err := readCronMutationTx(ctx, tx, accountID, appID, request, want)
	if err != nil {
		return api.CronMutationReceipt{}, err
	}
	if exists && (r.Receipt.Status == "completed" || r.Receipt.Status == "conflict") {
		return r.Receipt, tx.Commit(ctx)
	}
	data, err := q.ReadCronMutationSnapshot(ctx, tx, sqlc.ReadCronMutationSnapshotParams{AppID: appID, CronID: request.CronID})
	var cron Cron
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return api.CronMutationReceipt{}, err
	}
	if err == nil {
		if err := json.Unmarshal(data, &cron); err != nil {
			return api.CronMutationReceipt{}, err
		}
	}
	if cron.ID == "" || CronReviewToken(cron) != request.ExpectedState {
		if !exists {
			return api.CronMutationReceipt{}, ErrCronReviewConflict
		}
		r.Receipt.Status = "conflict"
	} else {
		if err := validateCronMutationTarget(cron, request); err != nil {
			return api.CronMutationReceipt{}, err
		}
		if !exists {
			if err := reserveCronMutationTx(ctx, tx, appID, request.CronID); err != nil {
				return api.CronMutationReceipt{}, err
			}
		}
		if !request.Delete {
			if err := updateReviewedCronTx(ctx, tx, request); err != nil {
				return api.CronMutationReceipt{}, err
			}
			r.Receipt.Status, r.Receipt.Phase = "completed", "complete"
		} else if exists {
			if err := advanceCronDeletionTx(ctx, tx, &r); err != nil {
				return api.CronMutationReceipt{}, err
			}
		}
	}
	r.Receipt.UpdatedAt = now
	if err := writeCronMutationTx(ctx, tx, accountID, r); err != nil {
		return api.CronMutationReceipt{}, err
	}
	return r.Receipt, tx.Commit(ctx)
}

func readCronMutationTx(ctx context.Context, tx pgx.Tx, accountID, appID string, request api.CronMutationRequest, want cronMutationRecord) (cronMutationRecord, bool, error) {
	data, err := sqlc.New().ReadCronMutationRecord(ctx, tx, sqlc.ReadCronMutationRecordParams{AppID: appID, AccountID: accountID, RequestID: request.RequestID})
	if errors.Is(err, pgx.ErrNoRows) {
		return want, false, nil
	}
	if err != nil {
		return want, false, err
	}
	var saved cronMutationRecord
	if err := json.Unmarshal(data, &saved); err != nil {
		return saved, false, err
	}
	if !saved.matches(want, request.ReceiptProof) {
		return saved, true, ErrConflict
	}
	return saved, true, nil
}

func reserveCronMutationTx(ctx context.Context, tx pgx.Tx, appID, cronID string) error {
	q := sqlc.New()
	if err := q.PruneCronMutations(ctx, tx, sqlc.PruneCronMutationsParams{AppID: appID, BatchLimit: api.CronMutationReceiptMax}); err != nil {
		return err
	}
	count, err := q.CountCronMutations(ctx, tx, appID)
	if err != nil {
		return err
	}
	if count >= api.CronMutationReceiptMax {
		return ErrQuotaExceeded
	}
	return q.SupersedeCronMutations(ctx, tx, sqlc.SupersedeCronMutationsParams{AppID: appID, CronID: cronID})
}

func writeCronMutationTx(ctx context.Context, tx pgx.Tx, accountID string, r cronMutationRecord) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	v := r.Receipt
	return sqlc.New().WriteCronMutationRecord(ctx, tx, sqlc.WriteCronMutationRecordParams{AppID: v.AppID, AccountID: accountID, CronID: v.CronID, RequestID: v.RequestID, Status: v.Status, Record: data, RetainedUntil: pgTimestamptzUTC(v.RetainedUntil)})
}

func updateReviewedCronTx(ctx context.Context, tx pgx.Tx, r api.CronMutationRequest) error {
	params := sqlc.UpdateReviewedCronParams{AppID: r.AppID, CronID: r.CronID}
	if r.Schedule != nil {
		params.Schedule = pgtype.Text{String: *r.Schedule, Valid: true}
	}
	if r.Path != nil {
		params.Path = pgtype.Text{String: *r.Path, Valid: true}
	}
	if r.Timezone != nil {
		params.Timezone = pgtype.Text{String: *r.Timezone, Valid: true}
	}
	if r.Enabled != nil {
		params.Enabled = pgtype.Bool{Bool: *r.Enabled, Valid: true}
	}
	n, err := sqlc.New().UpdateReviewedCron(ctx, tx, params)
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

func advanceCronDeletionTx(ctx context.Context, tx pgx.Tx, r *cronMutationRecord) error {
	q, receipt := sqlc.New(), &r.Receipt
	switch receipt.Phase {
	case "cancel_pending":
		cronID, appID, _ := pgUUIDPair(receipt.CronID, receipt.AppID)
		ids, err := q.ListPendingCronInvocationIDs(ctx, tx, sqlc.ListPendingCronInvocationIDsParams{CronID: cronID, AppID: appID, RowLimit: cronPendingCancelBatch})
		if err != nil {
			return err
		}
		for _, id := range ids {
			status, err := cancelPendingInvocationTx(ctx, tx, id)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			if status == InvocationCancelled {
				receipt.Cancelled++
			}
		}
		if len(ids) < cronPendingCancelBatch {
			receipt.Phase = "detach_invocations"
		}
	case "detach_invocations":
		n, err := q.DetachReviewedCronInvocations(ctx, tx, sqlc.DetachReviewedCronInvocationsParams{CronID: receipt.CronID, BatchLimit: cronDeleteBatch})
		if err != nil {
			return err
		}
		receipt.DetachedInvocations += n
		if n < cronDeleteBatch {
			receipt.Phase = "detach_tasks"
		}
	case "detach_tasks":
		n, err := q.DetachReviewedCronTasks(ctx, tx, sqlc.DetachReviewedCronTasksParams{CronID: receipt.CronID, BatchLimit: cronDeleteBatch})
		if err != nil {
			return err
		}
		receipt.DetachedTasks += n
		if n < cronDeleteBatch {
			receipt.Phase = "delete"
		}
	case "delete":
		remaining, err := q.ReviewedCronReferencesRemain(ctx, tx, receipt.CronID)
		if err != nil {
			return err
		}
		if remaining.Bool {
			receipt.Phase = "cancel_pending"
			break
		}
		if err := q.DeleteGuardedCron(ctx, tx, sqlc.DeleteGuardedCronParams{CronID: receipt.CronID, AppID: receipt.AppID}); err != nil {
			return err
		}
		receipt.Status, receipt.Phase = "completed", "complete"
		return nil
	default:
		return ErrInvalidArgument
	}
	receipt.Status = "partial"
	return nil
}
