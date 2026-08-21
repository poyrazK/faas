package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MigrationLockKey is the constant pg_advisory_lock key that guards the
// schema-migration critical section across every Gregale daemon in the
// fleet. Bigint — exact match is the contract.
//
// Why session-scoped (pg_advisory_lock) and not xact-scoped
// (pg_advisory_xact_lock): goose runs multiple internal transactions
// during UpContext; an xact-scoped lock would release at the first COMMIT,
// letting a second daemon enter and race the version INSERT. The one
// existing advisory-lock precedent in this codebase is xact-scoped
// (pkg/state/pgstore_ratelimit.go:35 on the rate-limit counter) because
// there the entire workload is a single statement. Schema migration is
// many statements, so we need the session-scoped variant.
//
// Why a constant key (not hashtext(svc)): the key is a global lock on
// "any schema-migrating connection in this database". Hashing a service
// name would create one lock per service and reintroduce the very race
// we're closing (N daemons all hashing to distinct keys → N concurrent
// migrations). A single bigint key means at most one daemon in the world
// can be applying migrations at any instant.
//
// ADR-124 / Multi-host safety cluster PR-1.
const MigrationLockKey int64 = 0xfa4_5a5d_0e1a_0001

// ErrMigrationLockNotHeld is returned by the release closure if the caller
// invokes it without ever having observed a successful AcquireMigrationLock.
// The closure is the only object that knows whether the lock is held, so
// double-release is a logic bug and should fail loudly.
var ErrMigrationLockNotHeld = errors.New("db: migration lock not held")

// AcquireMigrationLock pins a connection from the pool and acquires the
// session-scoped pg_advisory_lock(MigrationLockKey) on it. The returned
// release closure must be invoked when the migration work is complete:
// it runs pg_advisory_unlock(MigrationLockKey) on the same pinned
// connection and returns it to the pool.
//
// The pinned connection stays out of the pool's general circulation until
// release runs, which means every other MigrateUp call in the fleet —
// from every other daemon on every other box — blocks on its own
// AcquireMigrationLock until this holder finishes. That is the load-bearing
// invariant: the schema is mutated by exactly one daemon at a time, even
// when the operator launches every daemon in parallel.
//
// Context cancellation:
//   - Before the lock is acquired: returns ctx.Err() immediately. No
//     connection is held.
//   - After the lock is acquired: the caller is expected to invoke the
//     release closure (which still runs the unlock on the live pinned
//     connection — pg_advisory_unlock does not require a live ctx, only
//     a live connection). If the caller forgets, the connection returns
//     to the pool when its reference is GC'd, and the session ends, so
//     PG auto-releases the lock at connection close. We log a warning
//     in the test harness to catch the "forgot to release" bug.
//
// Errors:
//   - nil pool  → "db: AcquireMigrationLock: nil pool"
//   - pool.Acquire failure → wrapped
//   - pg_advisory_lock failure → wrapped (after rolling back the pinned conn)
//
// The helper does NOT acquire the goose shim or call MigrateUp — the
// caller composes them. The lock and the migration are separate concerns
// on separate connections (the lock sits on a pgxpool connection; goose
// uses a separate *sql.DB derived from the pool's ConnConfig). Both
// paths in the same daemon must call this helper or skip it explicitly
// (cmd/migrate -status calls db.Status instead of MigrateUp; that path
// is the only deliberate skip and is documented at the docstring on
// MigrateUp itself).
func AcquireMigrationLock(ctx context.Context, pool *pgxpool.Pool) (release func() error, err error) {
	if pool == nil {
		return nil, errors.New("db: AcquireMigrationLock: nil pool")
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: acquire pool conn for migration lock: %w", err)
	}

	// Use the session-scoped variant: returns at session end OR explicit
	// unlock. We use ExecContext (not QueryRowContext.Scan) because the
	// variant returns void — the boolean "acquired" parameter is irrelevant
	// when we use the blocking form (the try-form is pg_try_advisory_lock,
	// which we deliberately do NOT use here).
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", MigrationLockKey); err != nil {
		conn.Release()
		return nil, fmt.Errorf("db: pg_advisory_lock(%d): %w", MigrationLockKey, err)
	}

	var released bool
	release = func() error {
		if released {
			return ErrMigrationLockNotHeld
		}
		released = true

		// Run the unlock on a fresh context — the caller's ctx may already
		// be cancelled (their goroutine panicked, the deadline expired,
		// etc.) and we still want to release the lock before returning the
		// connection to the pool. Using context.Background() is safe here
		// because pg_advisory_unlock is a one-shot round trip; we bound it
		// with a short timeout so a hung connection doesn't deadlock the
		// shutdown path.
		unlockCtx, cancel := context.WithTimeout(context.Background(), pgxLockUnlockTimeout)
		defer cancel()

		_, err := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", MigrationLockKey)
		// Always release the connection — even if the unlock Exec errored,
		// the connection must go back to the pool. PG auto-releases the
		// lock at session end if the unlock call raced a network error,
		// so leaking the conn is the only way the lock would survive.
		conn.Release()
		if err != nil {
			return fmt.Errorf("db: pg_advisory_unlock(%d): %w", MigrationLockKey, err)
		}
		return nil
	}
	return release, nil
}

// pgxLockUnlockTimeout bounds the unlock round-trip during release. The
// unlock is a single Exec on a pinned connection we already own; under
// healthy Postgres this is sub-millisecond. Five seconds is the safety
// margin: a hung connection returns to the pool after this window and
// PG's session-end auto-release frees the lock. Anything longer means
// the network or Postgres itself is gone, in which case the daemon is
// about to die anyway and the caller will log the timeout.
const pgxLockUnlockTimeout = 5 * 1_000_000_000 // 5s in ns; matches pgxpool default
