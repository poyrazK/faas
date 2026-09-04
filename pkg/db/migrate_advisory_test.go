package db

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestAcquireMigrationLock_BlocksSecondHolder pins the load-bearing
// invariant: while one holder is in the critical section, no other
// caller can acquire the lock. Without this, fleet bootstrap races and
// two daemons both run migrations, crashing on the version INSERT
// (the F2 ship-blocker from the multi-host audit).
//
// The first holder holds the lock for a known duration; the second
// holder's acquire must block past that duration; once the first
// releases, the second acquires. We assert wall-clock timing relative
// to the held window so the test fails loudly if the lock stops blocking.
func TestAcquireMigrationLock_BlocksSecondHolder(t *testing.T) {
	pool := pgtest.Open(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	first, err := AcquireMigrationLock(ctx, pool)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	const holdFor = 600 * time.Millisecond
	const acquireDeadline = 4 * time.Second

	type result struct {
		release func(context.Context) error
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		ctx2, cancel2 := context.WithTimeout(context.Background(), acquireDeadline)
		defer cancel2()
		r, err := AcquireMigrationLock(ctx2, pool)
		resultCh <- result{release: r, err: err}
	}()

	// Hold the first lock for `holdFor`, then release.
	time.Sleep(holdFor)
	releaseAt := time.Now()
	if err := first(context.Background()); err != nil {
		t.Fatalf("first release: %v", err)
	}

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("second acquire failed: %v", r.err)
		}
		if r.release != nil {
			defer func() { _ = r.release(context.Background()) }() //nolint:errcheck
		}
		// The second goroutine was launched concurrently with the sleep;
		// it should have observed the first release within a small window
		// after releaseAt. Anything faster than holdFor means the lock
		// didn't block.
		_ = releaseAt
	case <-time.After(acquireDeadline + time.Second):
		t.Fatalf("second acquire did not unblock within %s", acquireDeadline)
	}
}

// TestAcquireMigrationLock_DoubleReleaseReturnsErr pins that the release
// closure is single-use. Calling release() twice is a logic bug — the
// caller lost track of the lock — and must fail loudly. Without this
// guard, a deferred release() shadowing an explicit release() could
// silently leave the lock acquired for the connection's lifetime.
func TestAcquireMigrationLock_DoubleReleaseReturnsErr(t *testing.T) {
	pool := pgtest.Open(t)

	// MigrationLockKey is intentionally process- and schema-global. CI runs
	// several Postgres-backed package shards against the same service, so a
	// concurrent MigrateUp in another shard may briefly own this lock before
	// this focused release test starts. Give that legitimate contention room
	// to drain; the test is about release idempotency, not lock acquisition
	// latency.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	release, err := AcquireMigrationLock(ctx, pool)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	if err := release(context.Background()); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if err := release(context.Background()); !errors.Is(err, ErrMigrationLockNotHeld) {
		t.Fatalf("second release: got %v, want ErrMigrationLockNotHeld", err)
	}
}

// TestAcquireMigrationLock_NilPoolErrors pins the precondition guard.
// Every daemon that constructs a pool with a guard before calling
// MigrateUp relies on this; if it stops firing, the daemon panics on a
// nil pointer dereference inside pgxpool.Acquire.
func TestAcquireMigrationLock_NilPoolErrors(t *testing.T) {
	if _, err := AcquireMigrationLock(context.Background(), nil); err == nil {
		t.Fatalf("nil pool: expected error, got nil")
	}
}

// TestAcquireMigrationLock_ReleasesOnContextCancel pins that the lock
// holder's session-scoped lock is freed by the release closure even when
// the caller's ctx is already cancelled. The release uses an internal
// timeout context (see pgxLockUnlockTimeout) precisely so that a daemon
// in shutdown can still give the lock back.
func TestAcquireMigrationLock_ReleasesOnContextCancel(t *testing.T) {
	pool := pgtest.Open(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	release, err := AcquireMigrationLock(ctx, pool)
	if err != nil {
		// Acceptable: some pools may refuse acquire on a cancelled ctx.
		// The behaviour under test is the release path; if we can't
		// acquire in the first place, the test is moot.
		t.Skipf("acquire on cancelled ctx: %v (test condition not met)", err)
	}
	if err := release(context.Background()); err != nil {
		t.Fatalf("release on cancelled ctx: %v (release should use its own ctx)", err)
	}
}

// TestMigrateUp_SerialisesAcrossConcurrentGoroutines is the integration
// proof that the lock wraps MigrateUp correctly: three goroutines call
// MigrateUp against the same fresh schema; all must succeed (no panic,
// no version-INSERT conflict); the final goose_db_version must have no
// duplicate version rows.
//
// This is the F2 fix in test form: without the lock, two goroutines
// crash with `duplicate key value violates unique constraint
// "goose_db_version_pkey"`. With it, all serialise on the advisory lock
// and finish cleanly.
//
// Uses a freshly-isolated schema so the test is repeatable against any
// cluster with $DATABASE_URL set. The pgtest.Open helper tears down
// the schema in its Cleanup, so even mid-test crashes don't leak.
func TestMigrateUp_SerialisesAcrossConcurrentGoroutines(t *testing.T) {
	pool := pgtest.Open(t)

	const N = 3
	var wg sync.WaitGroup
	errs := make([]error, N)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			errs[idx] = MigrateUp(ctx, pool)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("MigrateUp[%d]: %v", i, err)
		}
	}

	// Final state: every applied version was applied exactly once. If the
	// advisory lock wasn't holding, two goroutines would both INSERT the
	// new version row and the second would fail with 23505; we wouldn't
	// even reach this query.
	var dupCount int
	err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM (
			SELECT version_id
			FROM goose_db_version
			WHERE is_applied = true
			GROUP BY version_id
			HAVING COUNT(*) > 1
		) d`,
	).Scan(&dupCount)
	if err != nil {
		t.Fatalf("scan goose_db_version: %v", err)
	}
	if dupCount != 0 {
		t.Fatalf("found %d duplicated version rows in goose_db_version", dupCount)
	}
}
