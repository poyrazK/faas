package state

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeUpgradeIngressBindingStore = (*PgStore)(nil)

func (s *PgStore) AuthorizeRuntimeUpgradeGatewayIngress(ctx context.Context, slotID, sessionID string) (RuntimeUpgradeIngressBinding, error) {
	if !canonicalRuntimeUpgradeGatewayUUID(slotID) || !canonicalRuntimeUpgradeGatewayUUID(sessionID) {
		return RuntimeUpgradeIngressBinding{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return RuntimeUpgradeIngressBinding{}, fmt.Errorf("begin ingress binding read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	roster, err := q.ReadRuntimeUpgradeGatewayRoster(ctx, tx)
	if err != nil {
		return RuntimeUpgradeIngressBinding{}, fmt.Errorf("read ingress membership: %w", mapErr(err))
	}
	rows, err := q.ReadRuntimeUpgradeGatewayHeartbeats(ctx, tx, roster.Revision)
	if err != nil {
		return RuntimeUpgradeIngressBinding{}, fmt.Errorf("read ingress liveness: %w", err)
	}
	clock, err := q.ReadRuntimeUpgradeDrainClock(ctx, tx)
	if err != nil {
		return RuntimeUpgradeIngressBinding{}, fmt.Errorf("read ingress binding clock: %w", err)
	}
	beats := make([]runtimeUpgradeGatewayHeartbeat, len(rows))
	for i, h := range rows {
		beats[i] = runtimeUpgradeGatewayHeartbeat{Revision: pgUUIDString(h.RosterRevision), SlotID: pgUUIDString(h.SlotID), SessionID: pgUUIDString(h.GatewaySessionID), SeenAt: h.SeenAt.Time, ExpiresAt: h.ExpiresAt.Time}
	}
	return runtimeUpgradeIngressBinding(runtimeUpgradeGatewayRosterFromRow(roster), beats, slotID, sessionID, clock.Time)
}
