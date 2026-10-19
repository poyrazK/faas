// Package livequery is the Phase 0 prototype of ADR-20261016151947 live queries.
//
// A live query is a read-only SELECT whose result the engine keeps current as
// transactions commit. Changes come from logical decoding (pgoutput). The
// engine maintains results in rounds, each aligned to one PostgreSQL snapshot,
// so a subscriber always holds exactly the query result at some committed
// snapshot. PostgreSQL evaluates every expression; the engine only decides
// which rows to re-read, using primary-key lineage and group keys.
//
// Phase 0 exists to prove correctness: the differential oracle tests compare
// the engine's maintained result with the query executed at the same snapshot
// after every round. Nothing here is wired into a daemon yet.
package livequery
