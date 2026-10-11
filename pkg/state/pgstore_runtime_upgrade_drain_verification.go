package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// VerifyRuntimeUpgradeDrain is a fresh private forwarding observation. It
// incorporates current routing/health/membership, never historical success,
// and grants no scheduler, VM retirement, cleanup or whole-ingress authority.
func (s *PgStore) VerifyRuntimeUpgradeDrain(ctx context.Context, accountID, id string, sessions []string) (RuntimeUpgradeDrainVerification, error) {
	base, err := newRuntimeUpgradeVerification(id, sessions, time.Time{})
	if err != nil || !canonicalRuntimeUpgradeGatewayUUID(accountID) {
		return RuntimeUpgradeDrainVerification{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return RuntimeUpgradeDrainVerification{}, fmt.Errorf("begin forwarding drain verification: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	clock, err := sqlc.New().ReadRuntimeUpgradeDrainClock(ctx, tx)
	if err != nil {
		return RuntimeUpgradeDrainVerification{}, fmt.Errorf("read forwarding verification clock: %w", err)
	}
	base.CheckedAt = clock.Time
	base, err = runtimeUpgradeVerificationDB(ctx, tx, accountID, base)
	if err != nil {
		return RuntimeUpgradeDrainVerification{}, err
	}
	out := RuntimeUpgradeDrainVerification{OperationID: id, Status: "pending", Reason: base.Reason, CheckedAt: base.CheckedAt, GatewayRosterRevision: base.GatewayRosterRevision}
	if base.Status != "verified" {
		return out, nil
	}
	return runtimeUpgradeDrainReceiptsDB(ctx, tx, base, out)
}

func runtimeUpgradeDrainReceiptsDB(ctx context.Context, tx pgx.Tx, base RuntimeUpgradeVerification, out RuntimeUpgradeDrainVerification) (RuntimeUpgradeDrainVerification, error) {
	q := sqlc.New()
	op, err := q.GetRuntimeUpgradeOperation(ctx, tx, mustPgUUID(out.OperationID))
	if err != nil {
		return out, fmt.Errorf("read forwarding drain operation: %w", err)
	}
	snapshot, err := runtimeUpgradeDrainSnapshotDB(ctx, tx, pgUUIDString(op.AppID))
	if err != nil {
		return out, err
	}
	if snapshot.Plan == nil || snapshot.Plan.OperationID != out.OperationID || snapshot.Plan.GatewayRosterRevision != base.GatewayRosterRevision {
		out.Reason = "forwarding_activation_changed"
		return out, nil
	}
	rows, err := q.ReadRuntimeUpgradeGatewayDrains(ctx, tx, sqlc.ReadRuntimeUpgradeGatewayDrainsParams{AppID: op.AppID, Limit: api.RuntimeUpgradeGatewaySessionLimit + 1})
	if err != nil {
		return out, fmt.Errorf("read forwarding drain receipts: %w", err)
	}
	roster, err := q.ReadRuntimeUpgradeGatewayRoster(ctx, tx)
	if err != nil {
		return out, fmt.Errorf("read forwarding drain roster: %w", err)
	}
	clock, err := q.ReadRuntimeUpgradeDrainClock(ctx, tx)
	if err != nil {
		return out, fmt.Errorf("read forwarding drain clock: %w", err)
	}
	now := clock.Time
	out.CheckedAt = now
	out.RoutingRevision = snapshot.RoutingRevision
	out.Reason = "gateway_drain_pending"
	until := base.CheckedAt.Add(time.Duration(base.ValidForSeconds) * time.Second)
	for _, member := range runtimeUpgradeGatewayRosterFromRow(roster).Members {
		for _, r := range rows {
			if pgUUIDString(r.GatewaySessionID) != member.SessionID || pgUUIDString(r.SlotID) != member.SlotID {
				continue
			}
			if r.RoutingRevision != snapshot.RoutingRevision || pgUUIDString(r.OperationID) != out.OperationID || pgUUIDString(r.DeploymentID) != snapshot.Plan.DeploymentID ||
				pgUUIDString(r.ServingDeploymentID) != snapshot.Plan.ServingDeploymentID || pgUUIDString(r.GatewayRosterRevision) != base.GatewayRosterRevision ||
				!r.CutoverAt.Time.Equal(snapshot.Plan.CutoverAt) || r.ObservedAt.Time.Before(r.CutoverAt.Time) || r.ObservedAt.Time.After(now) || !r.ExpiresAt.Time.After(now) ||
				now.Sub(r.ObservedAt.Time) > api.RuntimeUpgradeDrainReceiptMaxAge || r.ActiveForwards != 0 {
				break
			}
			out.ConfirmedGateways++
			if r.ExpiresAt.Time.Before(until) {
				until = r.ExpiresAt.Time
			}
			break
		}
	}
	if out.ConfirmedGateways != len(base.GatewaySessions) {
		return out, nil
	}
	out.ValidForSeconds = int(until.Sub(now) / time.Second)
	if out.ValidForSeconds < 1 {
		out.ValidForSeconds = 0
		out.Reason = "gateway_drain_expired"
		return out, nil
	}
	out.Status = "forwarding_drained"
	out.Reason = ""
	return out, nil
}
