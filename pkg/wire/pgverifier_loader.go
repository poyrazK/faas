// Production loader for PGNodeVerifier (ADR-056).
//
// The loader is intentionally tiny: a single SQL query against an existing
// *pgxpool.Pool. Splitting the loader from the verifier keeps
// pgverifier.go Postgres-agnostic — the verifier is DB-shape-free and
// the loader is the only file that imports pgx.
//
// The shape mirrors the cmd/schedd/node_keys_loader.go pattern:
// daemon-side adapters wrap pgx, the verifier stays DB-shape-free.

package wire

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pgNodeLoader is the production-side NodeLoader.
type pgNodeLoader struct {
	pool *pgxpool.Pool
}

// NewPGNodeLoader returns a NodeLoader backed by an existing
// pgxpool.Pool. The pool is the daemon's normal pool — no separate
// connection needed for this read.
func NewPGNodeLoader(pool *pgxpool.Pool) NodeLoader {
	return pgNodeLoader{pool: pool}
}

// LoadNodes returns (name, id, cert_fingerprint) tuples for every compute node
// that may still participate in an internal mTLS exchange. The CN lookup key is `name` (the
// operator-assigned friendly label, e.g. "vmmd" or "schedd"), per
// ADR-056 §Locked decisions — leaf-CN binds to compute_nodes.name,
// not compute_nodes.id.
//
// Draining nodes cannot receive new placements, but they remain authenticated
// while their live instances are handed to peers. Removing their CN at the
// start of a drain makes the destination reject the source's migration RPC.
// Maintenance, unavailable, and retired nodes are excluded because they have
// no live handoff work left.
//
// PR-3 widening: cert_fingerprint (added in migration 00271 by
// PR-3a) is loaded alongside name+id. Empty values (NULL
// fingerprint on pre-PR-X boxes) are returned as "" —
// CertFingerprintByCN surfaces this as ErrCertFingerprintNotRegistered
// so the doctor (PR-4) can flag it.
func (l pgNodeLoader) LoadNodes(ctx context.Context) ([]NodeRow, error) {
	if l.pool == nil {
		return nil, fmt.Errorf("wire: pgNodeLoader has nil pool")
	}
	const q = `
		select name, id::text, cert_fingerprint
		  from compute_nodes
		 where lifecycle in ('active', 'recovering', 'draining', 'force_draining')
	`
	rows, err := l.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("wire: query compute_nodes: %w", err)
	}
	defer rows.Close()

	var out []NodeRow
	for rows.Next() {
		var r NodeRow
		var fingerprint *string
		if err := rows.Scan(&r.CN, &r.ID, &fingerprint); err != nil {
			return nil, fmt.Errorf("wire: scan compute_nodes row: %w", err)
		}
		if fingerprint != nil {
			r.CertFingerprint = *fingerprint
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("wire: iterate compute_nodes rows: %w", err)
	}
	return out, nil
}
