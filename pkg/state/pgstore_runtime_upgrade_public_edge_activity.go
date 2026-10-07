package state

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeUpgradePublicEdgeActivityStore = (*PgStore)(nil)
var _ RuntimeUpgradePublicEdgeActivityVerifier = (*PgStore)(nil)

func currentPublicEdgeGeneration(ctx context.Context, tx pgx.Tx, member RuntimeUpgradePublicEdgeMember) (RuntimeUpgradeIngressGeneration, error) {
	gateway, head, err := lockPublicEdgeHeads(ctx, tx, false)
	if err != nil {
		return RuntimeUpgradeIngressGeneration{}, err
	}
	if !head.Valid || !gateway.Valid {
		return RuntimeUpgradeIngressGeneration{}, ErrConflict
	}
	r, err := sqlc.New().ReadRuntimeUpgradePublicEdgeRoster(ctx, tx)
	if err != nil {
		return RuntimeUpgradeIngressGeneration{}, fmt.Errorf("read public ingress review: %w", err)
	}
	roster := publicEdgeRosterFromRow(r)
	if roster.GatewayRosterRevision != pgUUIDString(gateway) || !slices.Contains(roster.Members, member) {
		return RuntimeUpgradeIngressGeneration{}, ErrConflict
	}
	withdrawn, err := sqlc.New().RuntimeUpgradePublicEdgeSessionWithdrawn(ctx, tx, mustPgUUID(member.SessionID))
	if err != nil {
		return RuntimeUpgradeIngressGeneration{}, err
	}
	if withdrawn {
		return RuntimeUpgradeIngressGeneration{}, ErrConflict
	}
	return RuntimeUpgradeIngressGeneration{PublicRevision: roster.Revision, GatewayRevision: roster.GatewayRosterRevision}, nil
}

// Bind the exact receiving internal process and THIS public startup identity to
// one pair of current reviews. Locks last only for this bounded database read,
// never for a long-lived HTTP stream or upgraded connection.
func (s *PgStore) AuthorizeRuntimeUpgradePublicEdgeIngress(ctx context.Context, member RuntimeUpgradePublicEdgeMember, slot, session string) (RuntimeUpgradePublicIngressBinding, error) {
	out := RuntimeUpgradePublicIngressBinding{}
	if !validPublicEdgeMember(member) || !canonicalRuntimeUpgradeGatewayUUID(slot) || !canonicalRuntimeUpgradeGatewayUUID(session) {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, fmt.Errorf("begin public ingress generation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out.RuntimeUpgradeIngressGeneration, err = currentPublicEdgeGeneration(ctx, tx, member)
	if err != nil {
		return out, err
	}
	q := sqlc.New()
	r, err := q.ReadRuntimeUpgradeGatewayRoster(ctx, tx)
	if err != nil {
		return out, err
	}
	rows, err := q.ReadRuntimeUpgradeGatewayHeartbeats(ctx, tx, r.Revision)
	if err != nil {
		return out, err
	}
	clock, err := q.ReadRuntimeUpgradeDrainClock(ctx, tx)
	if err != nil {
		return out, err
	}
	beats := make([]runtimeUpgradeGatewayHeartbeat, len(rows))
	for i, h := range rows {
		beats[i] = runtimeUpgradeGatewayHeartbeat{Revision: pgUUIDString(h.RosterRevision), SlotID: pgUUIDString(h.SlotID), SessionID: pgUUIDString(h.GatewaySessionID), SeenAt: h.SeenAt.Time, ExpiresAt: h.ExpiresAt.Time}
	}
	out.RuntimeUpgradeIngressBinding, err = runtimeUpgradeIngressBinding(runtimeUpgradeGatewayRosterFromRow(r), beats, slot, session, clock.Time)
	return out, err
}

func (s *PgStore) RecordRuntimeUpgradePublicEdgeActivity(ctx context.Context, member RuntimeUpgradePublicEdgeMember, snapshot func(RuntimeUpgradeIngressGeneration) RuntimeUpgradePublicEdgeActivity) error {
	if !validPublicEdgeMember(member) || snapshot == nil {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin public ingress activity: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	generation, err := currentPublicEdgeGeneration(ctx, tx, member)
	if err != nil {
		return err
	}
	q := sqlc.New()
	if err := q.LockRuntimeUpgradePublicEdgeActivityTable(ctx, tx); err != nil {
		return err
	}
	if _, err := q.LockRuntimeUpgradePublicEdgeActivityRow(ctx, tx, mustPgUUID(member.SlotID)); err != nil {
		return err
	}
	a := snapshot(generation)
	if !a.valid() {
		return ErrInvalidArgument
	}
	n, err := q.RecordRuntimeUpgradePublicEdgeActivity(ctx, tx, sqlc.RecordRuntimeUpgradePublicEdgeActivityParams{
		SlotID: mustPgUUID(member.SlotID), PublicSessionID: mustPgUUID(member.SessionID), PublicRosterRevision: mustPgUUID(generation.PublicRevision), ConfigSha256: member.ConfigSHA256,
		ActivityVersion: a.Version, CoverageKnown: a.Known, PendingForwards: int32(a.Pending), CurrentForwards: int32(a.Current), PreviousForwards: int32(a.Previous), LeaseSeconds: int32(api.RuntimeUpgradeGatewayHeartbeatMaxAge / time.Second),
	})
	if err != nil {
		return fmt.Errorf("record public ingress activity: %w", runtimeUpgradeBaselineError(err))
	}
	if n != 1 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}

// Activity is an observation of current declared processes, not proof of a
// withdrawn process's death, closed future admission or safe retirement.
func (s *PgStore) ObserveRuntimeUpgradePublicEdgeActivity(ctx context.Context, expected string) (RuntimeUpgradePublicEdgeActivityObservation, error) {
	out := RuntimeUpgradePublicEdgeActivityObservation{RuntimeUpgradePublicEdgeObservation: RuntimeUpgradePublicEdgeObservation{Status: "pending", Reason: "public_edge_membership_unreviewed"}}
	if !canonicalRuntimeUpgradeGatewayUUID(expected) {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return runtimeUpgradePublicEdgeActivityDB(ctx, tx, expected)
}

func runtimeUpgradePublicEdgeActivityDB(ctx context.Context, tx pgx.Tx, expected string) (RuntimeUpgradePublicEdgeActivityObservation, error) {
	out := RuntimeUpgradePublicEdgeActivityObservation{RuntimeUpgradePublicEdgeObservation: RuntimeUpgradePublicEdgeObservation{Status: "pending", Reason: "public_edge_membership_unreviewed"}}
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
		return out, err
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
	rows, err := q.ReadRuntimeUpgradePublicEdgeActivity(ctx, tx, sqlc.ReadRuntimeUpgradePublicEdgeActivityParams{PublicRosterRevision: head, Limit: api.RuntimeUpgradePublicEdgeLimit + 1})
	if err != nil {
		return out, err
	}
	clock, err := q.ReadRuntimeUpgradeDrainClock(ctx, tx)
	if err != nil {
		return out, err
	}
	out.CheckedAt, out.Reason = clock.Time, "public_edge_activity_pending"
	if len(roster.Members) < 1 || len(roster.Members) > api.RuntimeUpgradePublicEdgeLimit || len(rows) > api.RuntimeUpgradePublicEdgeLimit {
		return out, nil
	}
	remaining := int(api.RuntimeUpgradeGatewayHeartbeatMaxAge / time.Second)
	for _, m := range roster.Members {
		for _, row := range rows {
			if pgUUIDString(row.SlotID) != m.SlotID || pgUUIDString(row.PublicSessionID) != m.SessionID || row.ConfigSha256 != m.ConfigSHA256 || !row.GuardEnabled || row.ObservedAt.Time.After(out.CheckedAt) || !row.ExpiresAt.Time.Equal(row.ObservedAt.Time.Add(api.RuntimeUpgradeGatewayHeartbeatMaxAge)) {
				continue
			}
			valid := int(row.ExpiresAt.Time.Sub(out.CheckedAt) / time.Second)
			if valid < 1 {
				continue
			}
			out.Pending += int(row.PendingForwards)
			out.Current += int(row.CurrentForwards)
			out.Previous += int(row.PreviousForwards)
			if !row.CoverageKnown || row.PendingForwards != 0 || row.PreviousForwards != 0 {
				break
			}
			remaining = min(remaining, valid)
			out.ConfirmedEdges++
			break
		}
	}
	if out.ConfirmedEdges == out.ExpectedEdges {
		out.Status, out.Reason, out.ValidForSeconds = "activity_observed", "", remaining
	}
	return out, nil
}
