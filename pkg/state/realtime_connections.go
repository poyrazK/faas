package state

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	connsqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
)

// ADR-1323 fair use: realtimed nodes share per-account connection counts so
// admission can cap an account across the fleet.

// realtimeConnectionNodeForgetAfter removes a node that stopped syncing, with
// its counts. Its counts already stop counting after
// api.RealtimeConnectionSyncStaleAfter.
const realtimeConnectionNodeForgetAfter = time.Hour

// ErrRealtimeConnectionSyncInvalid rejects a malformed connection sync.
var ErrRealtimeConnectionSyncInvalid = errors.New("state: invalid realtime connection sync")

// RealtimeAccountConnections is one account's open realtime connections.
type RealtimeAccountConnections struct {
	AccountID   string `json:"account_id"`
	Connections int64  `json:"connections"`
}

// RealtimeConnectionSyncStore exchanges per-account connection counts.
type RealtimeConnectionSyncStore interface {
	SyncRealtimeNodeConnections(ctx context.Context, node string, counts []RealtimeAccountConnections) ([]RealtimeAccountConnections, error)
}

// ValidateRealtimeConnectionSync checks one node's snapshot: every account at
// most once, each with at least one connection.
func ValidateRealtimeConnectionSync(node string, counts []RealtimeAccountConnections) error {
	if len(node) == 0 || len(node) > 253 || len(counts) > api.RealtimeConnectionSyncMaxAccounts {
		return ErrRealtimeConnectionSyncInvalid
	}
	seen := make(map[uuid.UUID]bool, len(counts))
	for _, c := range counts {
		id, err := uuid.Parse(c.AccountID)
		if err != nil || seen[id] || c.Connections < 1 || c.Connections > math.MaxInt32 {
			return ErrRealtimeConnectionSyncInvalid
		}
		seen[id] = true
	}
	return nil
}

// SyncRealtimeNodeConnections replaces node's per-account counts with counts
// and returns the other nodes' fresh counts, largest first, capped at
// api.RealtimeConnectionSyncMaxAccounts accounts.
func (s *PgStore) SyncRealtimeNodeConnections(ctx context.Context, node string, counts []RealtimeAccountConnections) ([]RealtimeAccountConnections, error) {
	if err := ValidateRealtimeConnectionSync(node, counts); err != nil {
		return nil, err
	}
	ids := make([]pgtype.UUID, len(counts))
	values := make([]int32, len(counts))
	for i, c := range counts {
		ids[i] = realtimeUsageUUID(c.AccountID)
		values[i] = int32(c.Connections)
	}
	q := connsqlc.New()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("state: sync realtime connections: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := q.MarkNodeConnectionSync(ctx, tx, node); err != nil {
		return nil, fmt.Errorf("state: sync realtime connections: mark node: %w", err)
	}
	if err := q.ForgetSilentConnectionNodes(ctx, tx, realtimeConnectionNodeForgetAfter.Seconds()); err != nil {
		return nil, fmt.Errorf("state: sync realtime connections: forget silent nodes: %w", err)
	}
	if err := q.ClearNodeConnectionsExcept(ctx, tx, connsqlc.ClearNodeConnectionsExceptParams{NodeName: node, AccountIds: ids}); err != nil {
		return nil, fmt.Errorf("state: sync realtime connections: clear: %w", err)
	}
	if err := q.UpsertNodeConnections(ctx, tx, connsqlc.UpsertNodeConnectionsParams{NodeName: node, AccountIds: ids, Counts: values}); err != nil {
		return nil, fmt.Errorf("state: sync realtime connections: upsert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("state: sync realtime connections: %w", err)
	}
	rows, err := q.ReadRemoteNodeConnections(ctx, s.pool, connsqlc.ReadRemoteNodeConnectionsParams{
		NodeName: node, StaleSeconds: api.RealtimeConnectionSyncStaleAfter.Seconds(), RowLimit: api.RealtimeConnectionSyncMaxAccounts,
	})
	if err != nil {
		return nil, fmt.Errorf("state: sync realtime connections: read remote: %w", err)
	}
	remote := make([]RealtimeAccountConnections, 0, len(rows))
	for _, row := range rows {
		remote = append(remote, RealtimeAccountConnections{AccountID: row.AccountID, Connections: row.Connections})
	}
	return remote, nil
}
