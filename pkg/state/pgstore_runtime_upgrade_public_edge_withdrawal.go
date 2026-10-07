package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeUpgradePublicEdgeWithdrawalStore = (*PgStore)(nil)
var _ RuntimeUpgradePublicEdgeCoverageVerifier = (*PgStore)(nil)

func (s *PgStore) RepairRuntimeUpgradePublicEdgeWithdrawal(ctx context.Context, member RuntimeUpgradePublicEdgeMember, install func(string) (RuntimeUpgradePublicEdgeWithdrawalSnapshot, error)) (bool, error) {
	if !validPublicEdgeMember(member) || install == nil {
		return false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin public edge withdrawal: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, _, err := lockPublicEdgeHeads(ctx, tx, false); err != nil {
		return false, err
	}
	q := sqlc.New()
	w, err := q.LockRuntimeUpgradePublicEdgeWithdrawal(ctx, tx, sqlc.LockRuntimeUpgradePublicEdgeWithdrawalParams{SlotID: mustPgUUID(member.SlotID), PublicSessionID: mustPgUUID(member.SessionID), ConfigSha256: member.ConfigSHA256})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	a, err := install(pgUUIDString(w.ID))
	if err != nil {
		return true, err
	}
	if a.ID != pgUUIDString(w.ID) || !canonicalRuntimeUpgradeGatewayUUID(a.FenceID) || a.Version < 1 || !a.Closed || a.Active < 0 || a.Active > api.RuntimeUpgradeActivityForwardLimit {
		return true, ErrInvalidArgument
	}
	if !a.Known || a.Active != 0 {
		return true, nil
	}
	if _, err := q.RecordRuntimeUpgradePublicEdgeWithdrawalReceipt(ctx, tx, sqlc.RecordRuntimeUpgradePublicEdgeWithdrawalReceiptParams{WithdrawalID: w.ID, FenceID: mustPgUUID(a.FenceID), ActivityVersion: a.Version}); err != nil {
		return true, fmt.Errorf("seal public edge withdrawal: %w", runtimeUpgradeBaselineError(err))
	}
	receipt, err := q.ReadRuntimeUpgradePublicEdgeWithdrawalReceipt(ctx, tx, w.ID)
	if err != nil {
		return true, err
	}
	if pgUUIDString(receipt.FenceID) != a.FenceID || receipt.ActivityVersion != a.Version {
		return true, ErrConflict
	}
	return true, mapErr(tx.Commit(ctx))
}

// Coverage combines current-generation activity with ALL unresolved historical
// withdrawals under the same head fences. It still grants no retirement authority.
func (s *PgStore) ObserveRuntimeUpgradePublicEdgeCoverage(ctx context.Context, expected string) (RuntimeUpgradePublicEdgeCoverageObservation, error) {
	out := RuntimeUpgradePublicEdgeCoverageObservation{}
	if !canonicalRuntimeUpgradeGatewayUUID(expected) {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, _, err := lockPublicEdgeHeads(ctx, tx, false); err != nil {
		return out, err
	}
	rows, err := sqlc.New().ReadPendingRuntimeUpgradePublicEdgeWithdrawals(ctx, tx, api.RuntimeUpgradePublicEdgeWithdrawalLimit+1)
	if err != nil {
		return out, err
	}
	// Read activity and its database clock AFTER the withdrawal read. A blocked
	// withdrawal-table read must not extend any current process's fact lease.
	out.RuntimeUpgradePublicEdgeActivityObservation, err = runtimeUpgradePublicEdgeActivityDB(ctx, tx, expected)
	if err != nil {
		return out, err
	}
	out.PendingWithdrawals = make([]RuntimeUpgradePublicEdgeWithdrawal, len(rows))
	for i, r := range rows {
		out.PendingWithdrawals[i] = RuntimeUpgradePublicEdgeWithdrawal{ID: pgUUIDString(r.ID), RosterRevision: pgUUIDString(r.RosterRevision), RuntimeUpgradePublicEdgeMember: RuntimeUpgradePublicEdgeMember{SlotID: pgUUIDString(r.SlotID), SessionID: pgUUIDString(r.PublicSessionID), ConfigSHA256: r.ConfigSha256}, CreatedAt: r.CreatedAt.Time.UTC()}
	}
	if len(rows) != 0 {
		if out.Status == "activity_observed" {
			out.Reason = "public_edge_withdrawal_pending"
		}
		out.Status, out.ValidForSeconds = "pending", 0
		if len(rows) > api.RuntimeUpgradePublicEdgeWithdrawalLimit {
			out.Reason = "public_edge_withdrawal_capacity_exceeded"
		}
		return out, nil
	}
	if out.Status == "activity_observed" {
		out.Status = "coverage_observed"
	}
	return out, nil
}
