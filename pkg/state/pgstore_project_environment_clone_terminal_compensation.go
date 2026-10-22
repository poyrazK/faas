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

var _ ProjectEnvironmentCloneTerminalCompensationStore = (*PgStore)(nil)

// Complete verifies closure and records completion under the same clock, owner,
// app and borrower fences. It deliberately never rewrites the original resources.
func (s *PgStore) CompleteProjectEnvironmentCloneCheckpointCompensation(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCompensationReceipt, error) {
	var out ProjectEnvironmentCloneCompensationReceipt
	if !validCloneLeaseIdentity(l) || l.Operation.Status != CloneOperationCompensating {
		return out, ErrInvalidArgument
	}
	// A committed retry may outlive its lease. Only the saved completing token and
	// revision can replay it; expired leases never authorize a new terminal write.
	if l.ExpiresAt.After(time.Now()) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, l.ExpiresAt)
		defer cancel()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if _, err = q.LockCloneCheckpointUnusedReservationClock(ctx, tx); err != nil {
		return out, mapErr(err)
	}
	op, err := lockCloneWorkloadOperationTx(ctx, tx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
	if err != nil {
		return out, err
	}
	saved, err := q.ReadCloneTerminalCompensationReceipt(ctx, tx, mustPgUUID(op.ID))
	if err == nil {
		out, err = replayCloneCompensationReceipt(op, l, saved)
		if err != nil {
			return ProjectEnvironmentCloneCompensationReceipt{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return ProjectEnvironmentCloneCompensationReceipt{}, mapErr(err)
		}
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, mapErr(err)
	}
	if op.Status != CloneOperationCompensating || !l.ExpiresAt.After(time.Now()) {
		return out, ErrConflict
	}
	if err = authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
		return out, err
	}
	if _, err = q.LockProjectEnvironmentCloneApps(ctx, tx, sqlc.LockProjectEnvironmentCloneAppsParams{AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID)}); err != nil {
		return out, mapErr(err)
	}
	report, err := reconcileCheckpointCompensationTx(ctx, tx, op)
	if err != nil {
		return out, err
	}
	if !completeCloneCompensationReport(report) {
		return out, ErrConflict
	}
	at, err := q.ReadProjectEnvironmentCloneDatabaseReservationTime(ctx, tx)
	if err != nil {
		return out, mapErr(err)
	}
	out = ProjectEnvironmentCloneCompensationReceipt{Report: report, CompletedAt: at.Time.UTC()}
	tokenSHA := cloneCheckpointSHA(l.Token)
	out.SHA256 = cloneCompensationReceiptSHA(op, tokenSHA, out)
	raw, err := json.Marshal(report)
	if err != nil || len(raw) > api.CloneCompensationReceiptMaxBytes {
		return ProjectEnvironmentCloneCompensationReceipt{}, ErrConflict
	}
	if err = q.InsertCloneTerminalCompensationReceipt(ctx, tx, sqlc.InsertCloneTerminalCompensationReceiptParams{OperationID: mustPgUUID(op.ID), WorkerTokenSha256: tokenSHA, Report: raw, ReceiptSha256: out.SHA256, CompletedAt: pgtype.Timestamptz{Time: out.CompletedAt, Valid: true}}); err != nil {
		return ProjectEnvironmentCloneCompensationReceipt{}, mapErr(err)
	}
	if err = authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
		return ProjectEnvironmentCloneCompensationReceipt{}, err
	}
	n, err := q.CompleteCloneTerminalCompensation(ctx, tx, sqlc.CompleteCloneTerminalCompensationParams{OperationID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
	if err != nil {
		return ProjectEnvironmentCloneCompensationReceipt{}, mapErr(err)
	}
	if n != 1 {
		return ProjectEnvironmentCloneCompensationReceipt{}, ErrConflict
	}
	if err = tx.Commit(ctx); err != nil {
		return ProjectEnvironmentCloneCompensationReceipt{}, mapErr(err)
	}
	return out, nil
}

func replayCloneCompensationReceipt(op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, saved sqlc.ProjectEnvironmentCloneCompensationReceipt) (ProjectEnvironmentCloneCompensationReceipt, error) {
	var out ProjectEnvironmentCloneCompensationReceipt
	if len(saved.Report) > api.CloneCompensationReceiptMaxBytes || json.Unmarshal(saved.Report, &out.Report) != nil {
		return ProjectEnvironmentCloneCompensationReceipt{}, ErrConflict
	}
	out.CompletedAt, out.SHA256 = saved.CompletedAt.Time.UTC(), saved.ReceiptSha256
	r := out.Report
	if op.Status != CloneOperationCompensated || op.TargetReleaseSetID != "" || !saved.CompletedAt.Valid || out.CompletedAt.IsZero() || out.CompletedAt.After(time.Now()) ||
		!completeCloneCompensationReport(r) || r.OperationID != op.ID || r.Revision != l.Operation.Revision || op.Revision != r.Revision+1 ||
		r.ResourcesSHA256 != cloneCheckpointSHA(op.Resources) || r.SourceRevisionHash != op.SourceRevisionHash || saved.WorkerTokenSha256 != cloneCheckpointSHA(l.Token) ||
		out.SHA256 != cloneCompensationReceiptSHA(op, saved.WorkerTokenSha256, out) {
		return ProjectEnvironmentCloneCompensationReceipt{}, ErrConflict
	}
	return out, nil
}
