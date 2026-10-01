package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OrphanedAppCandidate is the small, bounded projection used by ADR-421.
// The transfer rechecks eligibility; this projection is never a claim.
type OrphanedAppCandidate struct {
	ID, NodeID string
	RAMMB      int
}

func ownershipRecoveryUUID(id string) (pgtype.UUID, error) {
	if id == "" {
		return pgtype.UUID{}, nil
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}, ErrInvalidArgument
	}
	return pgtype.UUID{Bytes: u, Valid: true}, nil
}

func (s *PgStore) ListOrphanedAppsPage(ctx context.Context, cooldownSeconds, limit int, afterAppID, deadNodeID string) ([]OrphanedAppCandidate, error) {
	if limit <= 0 {
		return nil, nil
	}
	after, err := ownershipRecoveryUUID(afterAppID)
	if err != nil {
		return nil, err
	}
	dead, err := ownershipRecoveryUUID(deadNodeID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListOrphanedAppsPage(ctx, s.pool, sqlc.ListOrphanedAppsPageParams{
		AfterAppID: after, DeadNodeID: dead, CooldownSeconds: int32(cooldownSeconds), BatchLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list orphaned app page: %w", err)
	}
	out := make([]OrphanedAppCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, OrphanedAppCandidate{ID: uuid.UUID(row.ID.Bytes).String(), NodeID: uuid.UUID(row.NodeID.Bytes).String(), RAMMB: int(row.RamMb)})
	}
	return out, nil
}

func (s *PgStore) ReassignOrphanedAppOwner(ctx context.Context, appID, fromNodeID, toNodeID string, cooldownSeconds int) error {
	if appID == "" || fromNodeID == "" || toNodeID == "" || fromNodeID == toNodeID {
		return ErrInvalidArgument
	}
	app, err := ownershipRecoveryUUID(appID)
	if err != nil {
		return err
	}
	from, err := ownershipRecoveryUUID(fromNodeID)
	if err != nil {
		return err
	}
	to, err := ownershipRecoveryUUID(toNodeID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: begin ownership transfer: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	nodes, err := q.LockOwnershipRecoveryNodes(ctx, tx, sqlc.LockOwnershipRecoveryNodesParams{FromNodeID: from, ToNodeID: to})
	if err != nil {
		return fmt.Errorf("state: lock ownership transfer nodes: %w", err)
	}
	// A deleted source is orphaned as well. The destination must exist and be
	// fully active; recovering nodes are not safe ownership destinations yet.
	destinationReady := false
	for _, node := range nodes {
		if node.ID == from && NodeLifecycle(node.Lifecycle).IsAdmitting() {
			return ErrConflict
		}
		if node.ID == to {
			destinationReady = NodeLifecycle(node.Lifecycle) == NodeLifecycleActive
		}
	}
	if !destinationReady {
		return ErrConflict
	}
	n, err := q.ReassignOrphanedAppOwner(ctx, tx, sqlc.ReassignOrphanedAppOwnerParams{
		AppID: app, FromNodeID: from, ToNodeID: to, CooldownSeconds: int32(cooldownSeconds),
	})
	if err != nil {
		return fmt.Errorf("state: transfer orphaned app: %w", err)
	}
	if n == 0 {
		return ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: commit ownership transfer: %w", err)
	}
	return nil
}

func (m *MemStore) ListOrphanedAppsPage(_ context.Context, cooldownSeconds, limit int, afterAppID, deadNodeID string) ([]OrphanedAppCandidate, error) {
	if limit <= 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var out []OrphanedAppCandidate
	for _, app := range m.apps {
		if app.ID <= afterAppID || app.NodeID == "" || (deadNodeID != "" && app.NodeID != deadNodeID) ||
			(app.Status != AppActive && app.Status != AppEvictedCold) {
			continue
		}
		if node, ok := m.computeNodes[app.NodeID]; ok && node.Active {
			continue
		}
		if cooldownSeconds >= 0 && app.ReassignedAt != nil && now.Sub(*app.ReassignedAt) <= time.Duration(cooldownSeconds)*time.Second {
			continue
		}
		out = append(out, OrphanedAppCandidate{ID: app.ID, NodeID: app.NodeID, RAMMB: app.RAMMB})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ReassignOrphanedAppOwner(_ context.Context, appID, fromNodeID, toNodeID string, cooldownSeconds int) error {
	if appID == "" || fromNodeID == "" || toNodeID == "" || fromNodeID == toNodeID {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.NodeID != fromNodeID || (app.Status != AppActive && app.Status != AppEvictedCold) {
		return ErrConflict
	}
	if source, ok := m.computeNodes[fromNodeID]; ok && source.Active {
		return ErrConflict
	}
	destination, ok := m.computeNodes[toNodeID]
	if !ok || destination.Lifecycle != NodeLifecycleActive {
		return ErrConflict
	}
	now := time.Now()
	if cooldownSeconds >= 0 && app.ReassignedAt != nil && now.Sub(*app.ReassignedAt) <= time.Duration(cooldownSeconds)*time.Second {
		return ErrConflict
	}
	app.NodeID, app.ReassignedAt = toNodeID, &now
	app.ScalingPolicyRevision++
	m.apps[appID] = app
	return nil
}
