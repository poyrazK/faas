package db

import (
	"context"
	"errors"
	"fmt"
	"time"

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
//   - After the lock is acquired: the caller must invoke the release closure.
//     If the unlock fails, that session is closed before returning its pool
//     slot, so cancellation cannot strand an advisory lock on an idle session.
//
// Errors:
//   - nil pool  → "db: AcquireMigrationLock: nil pool"
//   - pool.Acquire failure → wrapped
//   - advisory-lock query failure → wrapped after closing the pinned session
//
// The helper does NOT acquire the goose shim or call MigrateUp — the
// caller composes them. The lock and the migration are separate concerns
// on separate connections (the lock sits on a pgxpool connection; goose
// uses a separate *sql.DB derived from the pool's ConnConfig). Both
// paths in the same daemon must call this helper or skip it explicitly
// (cmd/migrate -status calls db.Status instead of MigrateUp; that path
// is the only deliberate skip and is documented at the docstring on
// MigrateUp itself).
func AcquireMigrationLock(ctx context.Context, pool *pgxpool.Pool) (release func(context.Context) error, err error) {
	if pool == nil {
		return nil, errors.New("db: AcquireMigrationLock: nil pool")
	}

	// Session-scoped: pg_advisory_lock (not _xact_) is held across
	// statements on this pinned connection, so it must not be pooled.
	conn, err := DirectPool(pool).Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: acquire pool conn for migration lock: %w", err)
	}

	// Blocking pg_advisory_lock keeps a statement snapshot while waiting.
	// CREATE INDEX CONCURRENTLY can then wait for that snapshot while its
	// migrator owns this lock. Complete each failed try before waiting so
	// competing daemons do not prevent the current migrator from progressing.
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		var acquired bool
		if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", MigrationLockKey).Scan(&acquired); err != nil {
			discardMigrationLockConn(ctx, conn)
			return nil, fmt.Errorf("db: pg_try_advisory_lock(%d): %w", MigrationLockKey, err)
		}
		if acquired {
			break
		}
		select {
		case <-ctx.Done():
			conn.Release() // the last completed try did not acquire the lock
			return nil, fmt.Errorf("db: wait for migration lock: %w", ctx.Err())
		case <-tick.C:
		}
	}

	var released bool
	release = func(releaseCtx context.Context) error {
		if released {
			return ErrMigrationLockNotHeld
		}
		released = true

		// Respect the caller's release deadline. A failed unlock closes the
		// session; returning a healthy but still-locked connection to the pool
		// would otherwise strand the migration lock after cancellation.
		unlockCtx, cancel := context.WithTimeout(releaseCtx, pgxLockUnlockTimeout)
		defer cancel()

		_, err := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", MigrationLockKey)
		if err != nil {
			discardMigrationLockConn(releaseCtx, conn)
			return fmt.Errorf("db: pg_advisory_unlock(%d): %w", MigrationLockKey, err)
		}
		conn.Release()
		return nil
	}
	return release, nil
}

// A failed query may have acquired the lock before the client lost its reply.
// End the session rather than returning an uncertain lock owner to the pool.
func discardMigrationLockConn(parent context.Context, conn *pgxpool.Conn) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), pgxLockUnlockTimeout)
	defer cancel()
	_ = conn.Conn().Close(ctx)
	conn.Release()
}

// Bound both the unlock round-trip and closing an uncertain lock owner.
const pgxLockUnlockTimeout = 5 * time.Second
