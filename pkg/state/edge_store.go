package state

import "github.com/jackc/pgx/v5/pgxpool"

// EdgeStore is the store of the edge daemons (gatewayd-public): the
// queries they run and nothing else.
//
// A daemon that reaches *PgStore through an interface value carries the
// names and types of all of its methods, because the daemon links
// reflection; adding any store method then changes the daemon's code and
// redeploys the edge lane. EdgeStore shares PgStore's pool and its queries'
// implementations (methods of *pgCore) without being a PgStore, so the edge
// daemons change only when a query they run does
// (docs/adr/20261021092138-edge-store-blast-radius.md).
// scripts/ci/check_edge_store.sh fails a change that brings *PgStore or
// Store back into an edge daemon.
type EdgeStore struct {
	pgCore
}

// NewEdgeStore wraps a pool, which the caller owns, like NewPgStore.
func NewEdgeStore(pool *pgxpool.Pool) *EdgeStore {
	return &EdgeStore{pgCore: newPgCore(pool)}
}
