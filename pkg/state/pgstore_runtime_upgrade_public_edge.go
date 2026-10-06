package state

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeUpgradePublicEdgeRosterStore = (*PgStore)(nil)
var _ RuntimeUpgradePublicEdgeGuardStore = (*PgStore)(nil)

func lockPublicEdgeHeads(ctx context.Context, tx pgx.Tx, review bool) (pgtype.UUID, pgtype.UUID, error) {
	q := sqlc.New()
	gateway, err := q.ShareRuntimeUpgradeGatewayRosterHead(ctx, tx)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("fence internal roster: %w", err)
	}
	var edge pgtype.UUID
	if review {
		edge, err = q.LockRuntimeUpgradePublicEdgeRosterHead(ctx, tx)
	} else {
		edge, err = q.ShareRuntimeUpgradePublicEdgeRosterHead(ctx, tx)
	}
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("fence public edge roster: %w", err)
	}
	return gateway, edge, nil
}

func (s *PgStore) ReviewRuntimeUpgradePublicEdgeRoster(ctx context.Context, expected, gateway, topology string, members []RuntimeUpgradePublicEdgeMember) (RuntimeUpgradePublicEdgeRoster, error) {
	members, err := canonicalPublicEdgeMembers(expected, gateway, topology, members)
	if err != nil {
		return RuntimeUpgradePublicEdgeRoster{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RuntimeUpgradePublicEdgeRoster{}, fmt.Errorf("begin public edge review: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	currentGateway, head, err := lockPublicEdgeHeads(ctx, tx, true)
	if err != nil {
		return RuntimeUpgradePublicEdgeRoster{}, err
	}
	if pgUUIDString(currentGateway) != gateway || pgUUIDString(head) != expected {
		return RuntimeUpgradePublicEdgeRoster{}, ErrConflict
	}
	q := sqlc.New()
	if head.Valid {
		old, err := q.ReadRuntimeUpgradePublicEdgeRoster(ctx, tx)
		if err != nil {
			return RuntimeUpgradePublicEdgeRoster{}, fmt.Errorf("read public edge review: %w", err)
		}
		roster := publicEdgeRosterFromRow(old)
		if roster.GatewayRosterRevision == gateway && roster.TopologySHA256 == topology && slices.Equal(roster.Members, members) {
			return roster, nil
		}
	}
	slots, sessions, configs := make([]pgtype.UUID, len(members)), make([]pgtype.UUID, len(members)), make([]string, len(members))
	for i, m := range members {
		slots[i], sessions[i], configs[i] = mustPgUUID(m.SlotID), mustPgUUID(m.SessionID), m.ConfigSHA256
	}
	r, err := q.InsertRuntimeUpgradePublicEdgeRoster(ctx, tx, sqlc.InsertRuntimeUpgradePublicEdgeRosterParams{
		Revision: mustPgUUID(uuid.NewString()), GatewayRosterRevision: currentGateway, TopologySha256: topology, SlotIds: slots, PublicSessions: sessions, ConfigSha256s: configs,
	})
	if err != nil {
		return RuntimeUpgradePublicEdgeRoster{}, fmt.Errorf("save public edge review: %w", runtimeUpgradeBaselineError(err))
	}
	count, err := q.PublishRuntimeUpgradePublicEdgeRoster(ctx, tx, sqlc.PublishRuntimeUpgradePublicEdgeRosterParams{Revision: r.Revision, ExpectedRevision: head})
	if err != nil {
		return RuntimeUpgradePublicEdgeRoster{}, fmt.Errorf("publish public edge review: %w", err)
	}
	if count != 1 {
		return RuntimeUpgradePublicEdgeRoster{}, ErrConflict
	}
	if err := q.ResetRuntimeUpgradePublicEdgeGuards(ctx, tx); err != nil {
		return RuntimeUpgradePublicEdgeRoster{}, fmt.Errorf("reset public guard observations: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RuntimeUpgradePublicEdgeRoster{}, fmt.Errorf("commit public edge review: %w", err)
	}
	return publicEdgeRosterFromRow(r), nil
}

func (s *PgStore) RuntimeUpgradePublicEdgeRoster(ctx context.Context) (RuntimeUpgradePublicEdgeRoster, error) {
	r, err := sqlc.New().ReadRuntimeUpgradePublicEdgeRoster(ctx, s.pool)
	if err != nil {
		return RuntimeUpgradePublicEdgeRoster{}, fmt.Errorf("read public edge roster: %w", mapErr(err))
	}
	return publicEdgeRosterFromRow(r), nil
}

func publicEdgeRosterFromRow(r sqlc.RuntimeUpgradePublicEdgeRoster) RuntimeUpgradePublicEdgeRoster {
	out := RuntimeUpgradePublicEdgeRoster{Revision: pgUUIDString(r.Revision), GatewayRosterRevision: pgUUIDString(r.GatewayRosterRevision), TopologySHA256: r.TopologySha256, CreatedAt: r.CreatedAt.Time.UTC(), Members: make([]RuntimeUpgradePublicEdgeMember, len(r.SlotIds))}
	for i, slot := range r.SlotIds {
		out.Members[i] = RuntimeUpgradePublicEdgeMember{SlotID: pgUUIDString(slot), SessionID: pgUUIDString(r.PublicSessions[i]), ConfigSHA256: r.ConfigSha256s[i]}
	}
	return out
}

func (s *PgStore) RecordRuntimeUpgradePublicEdgeGuard(ctx context.Context, member RuntimeUpgradePublicEdgeMember) error {
	if !validPublicEdgeMember(member) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin public guard observation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, _, err := lockPublicEdgeHeads(ctx, tx, false); err != nil {
		return err
	}
	count, err := sqlc.New().RecordRuntimeUpgradePublicEdgeGuard(ctx, tx, sqlc.RecordRuntimeUpgradePublicEdgeGuardParams{SlotID: mustPgUUID(member.SlotID), PublicSessionID: mustPgUUID(member.SessionID), ConfigSha256: member.ConfigSHA256, LeaseSeconds: int32(api.RuntimeUpgradeGatewayHeartbeatMaxAge / time.Second)})
	if err != nil {
		return fmt.Errorf("record public guard observation: %w", err)
	}
	if count != 1 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}

// Observe reads all declared members under both current-head fences. It is a
// fresh installation/liveness observation, never an in-flight retirement lease.
func (s *PgStore) ObserveRuntimeUpgradePublicEdges(ctx context.Context, expected string) (RuntimeUpgradePublicEdgeObservation, error) {
	out := RuntimeUpgradePublicEdgeObservation{Status: "pending", Reason: "public_edge_membership_unreviewed"}
	if !canonicalRuntimeUpgradeGatewayUUID(expected) {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, fmt.Errorf("begin public edge observation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	gateway, head, err := lockPublicEdgeHeads(ctx, tx, false)
	if err != nil {
		return out, err
	}
	if !head.Valid {
		return out, nil
	}
	q := sqlc.New()
	r, err := q.ReadRuntimeUpgradePublicEdgeRoster(ctx, tx)
	if err != nil {
		return out, fmt.Errorf("read observed public edge roster: %w", err)
	}
	roster := publicEdgeRosterFromRow(r)
	out.Revision, out.GatewayRosterRevision, out.TopologySHA256, out.ExpectedEdges = roster.Revision, roster.GatewayRosterRevision, roster.TopologySHA256, len(roster.Members)
	if roster.Revision != expected {
		out.Reason = "public_edge_membership_changed"
		return out, nil
	}
	if roster.GatewayRosterRevision != pgUUIDString(gateway) {
		out.Reason = "gateway_membership_changed"
		return out, nil
	}
	rows, err := q.ReadRuntimeUpgradePublicEdgeGuards(ctx, tx, sqlc.ReadRuntimeUpgradePublicEdgeGuardsParams{PublicRosterRevision: head, Limit: api.RuntimeUpgradePublicEdgeLimit + 1})
	if err != nil {
		return out, fmt.Errorf("read public guard facts: %w", err)
	}
	clock, err := q.ReadRuntimeUpgradeDrainClock(ctx, tx)
	if err != nil {
		return out, fmt.Errorf("read public guard clock: %w", err)
	}
	out.CheckedAt, out.Reason = clock.Time, "public_edge_guards_pending"
	if len(roster.Members) < 1 || len(roster.Members) > api.RuntimeUpgradePublicEdgeLimit || len(rows) > api.RuntimeUpgradePublicEdgeLimit {
		return out, nil
	}
	remaining := int(api.RuntimeUpgradeGatewayHeartbeatMaxAge / time.Second)
	for _, member := range roster.Members {
		for _, row := range rows {
			if pgUUIDString(row.SlotID) != member.SlotID || pgUUIDString(row.PublicSessionID) != member.SessionID || row.ConfigSha256 != member.ConfigSHA256 || !row.GuardEnabled || row.ObservedAt.Time.After(out.CheckedAt) || !row.ExpiresAt.Time.Equal(row.ObservedAt.Time.Add(api.RuntimeUpgradeGatewayHeartbeatMaxAge)) {
				continue
			}
			valid := int(row.ExpiresAt.Time.Sub(out.CheckedAt) / time.Second)
			if valid < 1 {
				continue
			}
			remaining = min(remaining, valid)
			out.ConfirmedEdges++
			break
		}
	}
	if out.ConfirmedEdges != out.ExpectedEdges {
		return out, nil
	}
	out.Status, out.Reason, out.ValidForSeconds = "guards_observed", "", remaining
	return out, nil
}
