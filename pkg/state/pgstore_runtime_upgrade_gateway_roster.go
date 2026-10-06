package state

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeUpgradeGatewayRosterStore = (*PgStore)(nil)
var _ RuntimeUpgradeGatewayHeartbeatStore = (*PgStore)(nil)

func (s *PgStore) ReviewRuntimeUpgradeGatewayRoster(ctx context.Context, expected string, members []RuntimeUpgradeGatewayMember) (RuntimeUpgradeGatewayRoster, error) {
	members, err := canonicalRuntimeUpgradeGatewayMembers(expected, members)
	if err != nil {
		return RuntimeUpgradeGatewayRoster{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RuntimeUpgradeGatewayRoster{}, fmt.Errorf("begin gateway roster review: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	head, err := q.LockRuntimeUpgradeGatewayRosterHead(ctx, tx)
	if err != nil {
		return RuntimeUpgradeGatewayRoster{}, fmt.Errorf("lock gateway roster review: %w", err)
	}
	if pgUUIDString(head) != expected {
		return RuntimeUpgradeGatewayRoster{}, ErrConflict
	}
	if head.Valid {
		old, readErr := q.ReadRuntimeUpgradeGatewayRoster(ctx, tx)
		if readErr != nil {
			return RuntimeUpgradeGatewayRoster{}, fmt.Errorf("read gateway roster review: %w", readErr)
		}
		roster := runtimeUpgradeGatewayRosterFromRow(old)
		if slices.Equal(roster.Members, members) {
			return roster, nil
		}
	}
	slots, sessions := make([]pgtype.UUID, len(members)), make([]pgtype.UUID, len(members))
	for i, member := range members {
		slots[i], sessions[i] = mustPgUUID(member.SlotID), mustPgUUID(member.SessionID)
	}
	r, err := q.InsertRuntimeUpgradeGatewayRoster(ctx, tx, sqlc.InsertRuntimeUpgradeGatewayRosterParams{Revision: mustPgUUID(uuid.NewString()), SlotIds: slots, GatewaySessions: sessions})
	if err != nil {
		return RuntimeUpgradeGatewayRoster{}, fmt.Errorf("save gateway roster review: %w", runtimeUpgradeBaselineError(err))
	}
	count, err := q.PublishRuntimeUpgradeGatewayRoster(ctx, tx, sqlc.PublishRuntimeUpgradeGatewayRosterParams{Revision: r.Revision, ExpectedRevision: head})
	if err != nil {
		return RuntimeUpgradeGatewayRoster{}, fmt.Errorf("publish gateway roster review: %w", err)
	}
	if count != 1 {
		return RuntimeUpgradeGatewayRoster{}, ErrConflict
	}
	if err := q.ResetRuntimeUpgradeGatewayHeartbeats(ctx, tx); err != nil {
		return RuntimeUpgradeGatewayRoster{}, fmt.Errorf("reset gateway roster liveness: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RuntimeUpgradeGatewayRoster{}, fmt.Errorf("commit gateway roster review: %w", err)
	}
	return runtimeUpgradeGatewayRosterFromRow(r), nil
}

func (s *PgStore) RuntimeUpgradeGatewayRoster(ctx context.Context) (RuntimeUpgradeGatewayRoster, error) {
	r, err := sqlc.New().ReadRuntimeUpgradeGatewayRoster(ctx, s.pool)
	if err != nil {
		return RuntimeUpgradeGatewayRoster{}, fmt.Errorf("read gateway roster: %w", mapErr(err))
	}
	return runtimeUpgradeGatewayRosterFromRow(r), nil
}

func (s *PgStore) HeartbeatRuntimeUpgradeGateway(ctx context.Context, slot, session string) error {
	if !canonicalRuntimeUpgradeGatewayUUID(slot) || !canonicalRuntimeUpgradeGatewayUUID(session) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin gateway heartbeat: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.ShareRuntimeUpgradeGatewayRosterHead(ctx, tx); err != nil {
		return fmt.Errorf("fence gateway heartbeat: %w", err)
	}
	count, err := q.HeartbeatRuntimeUpgradeGateway(ctx, tx, sqlc.HeartbeatRuntimeUpgradeGatewayParams{SlotID: mustPgUUID(slot), GatewaySessionID: mustPgUUID(session), LeaseSeconds: int32(api.RuntimeUpgradeGatewayHeartbeatMaxAge / time.Second)})
	if err != nil {
		return fmt.Errorf("record gateway heartbeat: %w", err)
	}
	if count != 1 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}

func runtimeUpgradeGatewayRosterFromRow(r sqlc.RuntimeUpgradeGatewayRoster) RuntimeUpgradeGatewayRoster {
	roster := RuntimeUpgradeGatewayRoster{Revision: pgUUIDString(r.Revision), CreatedAt: r.CreatedAt.Time.UTC(), Members: make([]RuntimeUpgradeGatewayMember, len(r.SlotIds))}
	for i, slot := range r.SlotIds {
		roster.Members[i] = RuntimeUpgradeGatewayMember{SlotID: pgUUIDString(slot), SessionID: pgUUIDString(r.GatewaySessions[i])}
	}
	return roster
}

func runtimeUpgradeGatewayMembershipDB(ctx context.Context, tx pgx.Tx, accountID string, out RuntimeUpgradeVerification) (RuntimeUpgradeVerification, error) {
	q := sqlc.New()
	frozen, err := q.GetRuntimeUpgradeVerification(ctx, tx, sqlc.GetRuntimeUpgradeVerificationParams{OperationID: mustPgUUID(out.OperationID), AccountID: mustPgUUID(accountID)})
	enrolled := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return RuntimeUpgradeVerification{}, fmt.Errorf("read frozen gateway roster: %w", err)
	}
	r, err := q.ReadRuntimeUpgradeGatewayRoster(ctx, tx)
	if errors.Is(err, pgx.ErrNoRows) {
		out.Reason = "gateway_membership_unreviewed"
		return out, nil
	}
	if err != nil {
		return RuntimeUpgradeVerification{}, fmt.Errorf("read verification gateway roster: %w", err)
	}
	rows, err := q.ReadRuntimeUpgradeGatewayHeartbeats(ctx, tx, r.Revision)
	if err != nil {
		return RuntimeUpgradeVerification{}, fmt.Errorf("read verification gateway liveness: %w", err)
	}
	heartbeats := make([]runtimeUpgradeGatewayHeartbeat, len(rows))
	for i, h := range rows {
		heartbeats[i] = runtimeUpgradeGatewayHeartbeat{Revision: pgUUIDString(h.RosterRevision), SlotID: pgUUIDString(h.SlotID), SessionID: pgUUIDString(h.GatewaySessionID), SeenAt: h.SeenAt.Time, ExpiresAt: h.ExpiresAt.Time}
	}
	out.CheckedAt = time.Now().UTC()
	return evaluateRuntimeUpgradeGatewayMembership(out, runtimeUpgradeGatewayRosterFromRow(r), pgUUIDString(frozen.GatewayRosterRevision), enrolled, heartbeats), nil
}
