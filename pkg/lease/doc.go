// Package lease implements the durable-async CAS-with-token lease
// primitive (ADR-134 §6.7). It lifts the pattern that has lived
// inline on pkg/state/pgstore.go:2656 since the live-migration
// work, exposing it as a generic Manager that any row type can
// opt into by adding two columns (a TEXT lease_token and a
// TIMESTAMPTZ lease_expires_at).
//
// Scope. The primitive is intentionally narrow:
//
//   - Acquire — CAS from (state=expectedState, lease empty or
//     expired) to (state=expectedState, lease=NEW_TOKEN,
//     lease_expires_at=NOW+ttl). Mints a fresh UUIDv4 token.
//     Returns the new Lease or ErrLeaseConflict.
//   - Renew — CAS on (lease_token=$token) extending
//     lease_expires_at to NOW+ttl. Strict on token match — a
//     stolen lease cannot be silently extended by a third party.
//   - Release — CAS on (lease_token=$token) clearing both columns.
//     Strict on token match — see Renew.
//
// State transitions are NOT the primitive's job. Producers that
// need a state transition AND a lease stamp in one UPDATE
// (pkg/state/pgstore.go:2656 MarkInstanceMigrating is the canonical
// example: state='running' → 'migrating' + lease_token=$new in one
// query) should stay bespoke — splitting the operation into two
// queries opens a TOCTOU race that the inline form avoids. The
// Manager is for code paths where the lease stamp is the entire
// mutation, e.g. PR-B's invocations-claim transition (state
// unchanged, lease_token + lease_expires_at written).
//
// Usage. The constructor takes the table + column names so a
// single Manager implementation serves every table that adopts the
// lease columns. Construct once at package init, pass around as a
// dependency:
//
//	mgr := lease.New(pool, "instances", "id", "state",
//	                 "lease_token", "lease_expires_at", time.Now)
//	l, err := mgr.Acquire(ctx, instanceID, "running", time.Minute)
//
// Thread-safety. Manager is stateless and safe for concurrent use
// across goroutines — pgx pools serialise the actual UPDATE.
package lease
