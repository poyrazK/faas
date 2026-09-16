// node_keys_loader.go — production-side NodeKeyLoader for schedd.
//
// Why this lives in cmd/schedd, not pkg/sched. The pgNodeKeyLoader
// is a thin adapter from *pgxpool.Pool to sched.NodeKeyLoader —
// the only consumer is schedd's main wiring. Putting it inside
// the daemon keeps pkg/sched Postgres-agnostic (its NodeKeyLoader
// interface accepts any source; the unit test uses an in-memory
// stub). The same pattern keeps cmd/schedd config-aware without
// leaking Postgres-specific code into the engine package.
//
// Wire shape (cmd/schedd/main.go):
//
//	keys := sched.NewNodeKeyRegistry(pgNodeKeyLoader{pool: pool}, log)
//	engine.WithNodeKeyRegistry(keys)
//	if _, err := keys.Refresh(ctx); err != nil {
//	    log.Warn("schedd: initial node key registry refresh failed", "err", err)
//	}
//	go subscribeWithReconnect(ctx, "node keys", log,
//	    deps.subscribeNodeKeyChanges, pool, keys.Run)
//
// The reconnect loop calls keys.Refresh on every 'compute_node_changed'
// notify (one channel covers both compute_nodes and compute_node_keys
// per migration 00076; the trigger fires on either table).
//
// Pre-slice-3 schedd (the legacy path) does not construct this loader
// at all. The engine's NodeKeyRegistry() returns nil and the handler
// skips signature verification (additive per ADR-016). The fix here
// flips the conditional the other way: production schedd always
// wires the registry; the absence of compute_node_keys rows (no
// vmmd has registered yet) is a degenerate case that's safe —
// VerifyNodeSignature returns ErrUnknownNodeKey for every report,
// streams close, vmmd reconnects.

package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/sched"
)

// pgNodeKeyLoader is the production-side sched.NodeKeyLoader.
// Reads every row from compute_node_keys (migration 00076) and
// returns them in the engine-side sched.NodeKeyRow shape.
//
// Performance note: the table is small (one row per compute node,
// bounded by fleet size — handful of rows at the tier 1 scale).
// A full-table SELECT on every refresh is fine; the partial index
// compute_node_keys_node_idx is reserved for a future per-node
// refresh path that vmmd rotation would warrant.
type pgNodeKeyLoader struct {
	pool *pgxpool.Pool
}

const loadNodeKeysSQL = `
	select k.compute_node_id::text,
	       k.key_id,
	       k.public_key_pem
	  from compute_node_keys k
	  join compute_nodes n on n.id = k.compute_node_id
	 where n.active
	   and k.revoked_at is null
	   and (k.key_state = 'current'
	        or (k.key_state = 'overlap' and k.valid_until > now()))
`

// LoadNodeKeys reads every row from compute_node_keys. The query
// is the canonical column set (key_id, public_key_pem); schedd
// parses the PEM through the same parsePublicKeyPEM path the
// registry uses, so a malformed row fails at ReplaceAll with a
// Warn log (the registry keeps the last-known-good map; the
// offending row is skipped).
func (l pgNodeKeyLoader) LoadNodeKeys(ctx context.Context) ([]sched.NodeKeyRow, error) {
	rows, err := l.pool.Query(ctx, loadNodeKeysSQL)
	if err != nil {
		return nil, fmt.Errorf("schedd: query compute_node_keys: %w", err)
	}
	defer rows.Close()
	var out []sched.NodeKeyRow
	for rows.Next() {
		var r sched.NodeKeyRow
		if err := rows.Scan(&r.ComputeNodeID, &r.KeyID, &r.PublicKeyPEM); err != nil {
			return nil, fmt.Errorf("schedd: scan compute_node_keys row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("schedd: iterate compute_node_keys: %w", err)
	}
	return out, nil
}
