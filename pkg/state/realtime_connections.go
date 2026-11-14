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

// RealtimeEndpointConnections is one capped endpoint's open realtime
// connections. A node reports every endpoint it serves that has its own
// max_connections, with zero when it holds none, so it learns the other
// nodes' counts before its first connection.
type RealtimeEndpointConnections struct {
	EndpointID  string `json:"endpoint_id"`
	Connections int64  `json:"connections"`
}

// RealtimeEndpointConnectionSyncStore exchanges per-endpoint connection
// counts. It runs after SyncRealtimeNodeConnections in the same sync, which
// records the node.
type RealtimeEndpointConnectionSyncStore interface {
	SyncRealtimeNodeEndpointConnections(ctx context.Context, node string, counts []RealtimeEndpointConnections) ([]RealtimeEndpointConnections, error)
}

// ValidateRealtimeEndpointConnectionSync checks one node's endpoint snapshot:
// every endpoint at most once, counts never negative.
func ValidateRealtimeEndpointConnectionSync(node string, counts []RealtimeEndpointConnections) error {
	if len(node) == 0 || len(node) > 253 || len(counts) > api.RealtimeConnectionSyncMaxEndpoints {
		return ErrRealtimeConnectionSyncInvalid
	}
	seen := make(map[uuid.UUID]bool, len(counts))
	for _, c := range counts {
		id, err := uuid.Parse(c.EndpointID)
		if err != nil || seen[id] || c.Connections < 0 || c.Connections > math.MaxInt32 {
			return ErrRealtimeConnectionSyncInvalid
		}
		seen[id] = true
	}
	return nil
}

// SyncRealtimeNodeEndpointConnections replaces node's per-endpoint counts
// with the nonzero ones in counts and returns the other nodes' fresh counts
// for every endpoint in counts. The node must already be recorded by
// SyncRealtimeNodeConnections.
func (s *PgStore) SyncRealtimeNodeEndpointConnections(ctx context.Context, node string, counts []RealtimeEndpointConnections) ([]RealtimeEndpointConnections, error) {
	if err := ValidateRealtimeEndpointConnectionSync(node, counts); err != nil {
		return nil, err
	}
	all := make([]pgtype.UUID, 0, len(counts))
	open := make([]pgtype.UUID, 0, len(counts))
	values := make([]int32, 0, len(counts))
	for _, c := range counts {
		id := realtimeUsageUUID(c.EndpointID)
		all = append(all, id)
		if c.Connections > 0 {
			open = append(open, id)
			values = append(values, int32(c.Connections))
		}
	}
	q := connsqlc.New()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("state: sync realtime endpoint connections: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := q.ClearNodeEndpointConnectionsExcept(ctx, tx, connsqlc.ClearNodeEndpointConnectionsExceptParams{NodeName: node, EndpointIds: open}); err != nil {
		return nil, fmt.Errorf("state: sync realtime endpoint connections: clear: %w", err)
	}
	if err := q.UpsertNodeEndpointConnections(ctx, tx, connsqlc.UpsertNodeEndpointConnectionsParams{NodeName: node, EndpointIds: open, Counts: values}); err != nil {
		return nil, fmt.Errorf("state: sync realtime endpoint connections: upsert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("state: sync realtime endpoint connections: %w", err)
	}
	if len(all) == 0 {
		return nil, nil
	}
	rows, err := q.ReadRemoteNodeEndpointConnections(ctx, s.pool, connsqlc.ReadRemoteNodeEndpointConnectionsParams{
		NodeName: node, EndpointIds: all, StaleSeconds: api.RealtimeConnectionSyncStaleAfter.Seconds(),
	})
	if err != nil {
		return nil, fmt.Errorf("state: sync realtime endpoint connections: read remote: %w", err)
	}
	remote := make([]RealtimeEndpointConnections, 0, len(rows))
	for _, row := range rows {
		remote = append(remote, RealtimeEndpointConnections{EndpointID: row.EndpointID, Connections: row.Connections})
	}
	return remote, nil
}
