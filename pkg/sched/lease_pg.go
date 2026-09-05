// Package sched — Postgres-backed Leaser[pgLeaseRecord] implementation.
//
// The production dispatch path (M5's Engine.WakeJob / HandleJobExit)
// does NOT consume Leaser[T] directly; it uses JobTaskMarkClaimed /
// MarkTerminal / Retry which write the lease_token + lease_expires_at
// columns as a side effect of the state transition. This file exists
// so the Leaser[T] abstraction has a Postgres implementation that
// matches the in-memory one in lease.go — useful for tests that want
// to exercise the lease surface through real SQL, and as the canonical
// shape for the future pkg/dispatch.Leaser[T] swap (ADR-134).
//
// The pgLeaseRecord handle returned by Acquire is the (run_id,
// task_index) tuple as a string, plus the issued lease_token. The
// caller never touches the struct fields directly; the dispatch
// path passes the LeaseToken to HandleJobExit and the struct fields
// are diagnostic-only.

package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgLeaseRecord is the per-lease row returned by PgLeaser.Acquire.
// RunID + TaskIndex uniquely identifies the job_tasks row; Token is
// the freshly minted UUID v7; ExpiresAt is the wall-clock TTL bound.
// LastRenewedAt is stamped by Renew for observability.
type pgLeaseRecord struct {
	RunID         string
	TaskIndex     int
	Token         LeaseToken
	ExpiresAt     time.Time
	LastRenewedAt time.Time
}

// PgLeaser is the Postgres-backed Leaser[pgLeaseRecord].
// It depends on a minimal subset of the JobStore surface
// (MarkClaimed-style writes + a Lease-by-token reader) so it can be
// exercised without dragging the full Store dependency into a test.
//
// The pool is the pgxpool.Pool from the production wiring; the test
// path passes a pgtest pool.
type PgLeaser struct {
	// pool is the pgxpool.Pool (or *pgxpool.Pool) the leaser writes
	// through. Held as an interface so pkg/sched doesn't import
	// pgx directly; the production wiring casts.
	//
	// QueryExecer is the minimum surface Acquire / Renew / Release
	// need. We type-assert in each call so a misconfigured pool
	// (one without QueryExecer) fails fast.
	pool poolExecutor

	// nodeID is stamped on last_lease_node (job_tasks.last_lease_node,
	// 00574) so an audit log can name the schedd that owns the lease.
	// Production wiring passes the schedd node identity from
	// pkg/sched.NodeIdentity.
	nodeID string

	// now is the injectable clock (tests pass a deterministic one).
	now func() time.Time
}

// poolExecutor is the minimal pgx-shaped surface PgLeaser needs.
// Implemented by *pgxpool.Pool (and by pgtest's pool).
type poolExecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgxCommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgxRows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgxRow
}

// pgxCommandTag / pgxRows / pgxRow are the return-type interfaces
// the poolExecutor methods hand back. The pgxpool.Pool satisfies
// them directly; the test pool satisfies them via pgx v5.
//
// We avoid importing pgx here so a unit test that does NOT need a
// DB can still construct a MemLeaser without a transitive dep.
type pgxCommandTag interface {
	RowsAffected() int64
}

type pgxRows interface {
	Close()
	Next() bool
	Scan(dest ...any) error
	Err() error
}

type pgxRow interface {
	Scan(dest ...any) error
}

// NewPgLeaser wires a PgLeaser against an arbitrary poolExecutor.
// The production wiring casts *pgxpool.Pool; tests pass a pgtest
// pool or a stub that records calls without hitting a DB.
func NewPgLeaser(pool poolExecutor, nodeID string, now func() time.Time) *PgLeaser {
	if now == nil {
		now = time.Now
	}
	return &PgLeaser{
		pool:   pool,
		nodeID: nodeID,
		now:    now,
	}
}

// NewPgLeaserFromPool adapts the concrete pgxpool return types to the small
// interfaces used by PgLeaser. Keeping the adapter here removes the unsafe
// production type assertion that previously left jobs without a leaser.
func NewPgLeaserFromPool(pool *pgxpool.Pool, nodeID string, now func() time.Time) *PgLeaser {
	if pool == nil {
		return nil
	}
	return NewPgLeaser(pgxPoolExecutor{pool: pool}, nodeID, now)
}

type pgxPoolExecutor struct {
	pool *pgxpool.Pool
}

func (p pgxPoolExecutor) Exec(ctx context.Context, sql string, args ...any) (pgxCommandTag, error) {
	return p.pool.Exec(ctx, sql, args...)
}

func (p pgxPoolExecutor) Query(ctx context.Context, sql string, args ...any) (pgxRows, error) {
	return p.pool.Query(ctx, sql, args...)
}

func (p pgxPoolExecutor) QueryRow(ctx context.Context, sql string, args ...any) pgxRow {
	return p.pool.QueryRow(ctx, sql, args...)
}

// Acquire issues a fresh lease against the (runID, taskIndex) tuple.
// The "key" parameter is the (runID|"\x00"|taskIndex) string the
// Leaser[T] interface expects; pgLeaser.Acquire ignores it after
// asserting the format — the canonical identifier for the lease is
// the job_tasks row, not the key string.
//
// Implementation: a single UPDATE on job_tasks guarded by
// `lease_token IS NULL OR lease_expires_at < now()` so a live
// lease held by another schedd is not stolen. RowsAffected()==0
// returns ErrLeaseHeldByOther (the row exists but is leased to
// someone else OR not yet ready for re-claim). The previous shape
// ignored RowsAffected and matched on `status IN ('queued','claimed')`
// only, so two schedd nodes both observed success on the same row
// and double-billed tenant RAM (CR-D / code-review #2 round-4).
//
// In production, Acquire is rarely called directly: M5's
// JobTaskClaimBatch + JobTaskMarkClaimed writes the same columns
// without going through this Leaser. This method exists so the
// Leaser[T] surface has a complete Postgres implementation.
func (l *PgLeaser) Acquire(ctx context.Context, key string, policy LeasePolicy, ownerID string) (LeaseToken, *pgLeaseRecord, error) {
	if err := policy.Validate(); err != nil {
		return "", nil, err
	}
	runID, taskIndex, err := parseLeaseKey(key)
	if err != nil {
		return "", nil, err
	}
	tok := LeaseToken(newUUIDv7Like())
	expires := l.now().Add(policy.TTL)
	// job_tasks.lease_token is the unique identifier for the lease;
	// last_lease_node + lease_expires_at complete the row. The
	// caller is responsible for setting status='claimed' on the same
	// row before Acquire returns (otherwise the partial unique index
	// includes NULL tokens which don't trip the constraint).
	//
	// The lease_token-IS-NULL OR lease_expires_at-is-past guard
	// is the load-bearing concurrency primitive: without it, two
	// schedds both see RowsAffected=1 on the same row (last writer
	// wins) and both boot a Firecracker VM for the same task. With
	// it, the second Acquire's WHERE matches 0 rows and we return
	// ErrLeaseHeldByOther so the dispatch tick re-queues via
	// JobTaskRequeue (which does NOT increment attempt — see CR-7).
	tag, err := l.pool.Exec(ctx, `
		UPDATE job_tasks
		   SET lease_token      = $1,
		       lease_expires_at = $2,
		       last_lease_node  = $3
		 WHERE run_id     = $4
		   AND task_index = $5
		   AND status IN ('queued','claimed')
		   AND (lease_token IS NULL OR lease_expires_at < now())
	`, string(tok), expires, l.nodeID, runID, taskIndex)
	if err != nil {
		return "", nil, fmt.Errorf("sched: pg lease acquire: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", nil, ErrLeaseHeldByOther
	}
	rec := &pgLeaseRecord{
		RunID:     runID,
		TaskIndex: taskIndex,
		Token:     tok,
		ExpiresAt: expires,
	}
	return tok, rec, nil
}

// Renew extends an existing lease's expires_at. Mirrors MemLeaser:
// ErrLeaseNotFound when the token is gone, ErrLeaseExpired when
// the lease already lapsed, ErrLeaseHeldByOther when the ownerID
// doesn't match. The ownerID check is enforced via the
// (lease_token, last_lease_node) pair — the renew must come from
// the same node.
func (l *PgLeaser) Renew(ctx context.Context, token LeaseToken, ownerID string, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("%w: renew TTL must be > 0", ErrInvalidLeasePolicy)
	}
	expires := l.now().Add(ttl)
	tag, err := l.pool.Exec(ctx, `
		UPDATE job_tasks
		   SET lease_expires_at = $1
		 WHERE lease_token      = $2
		   AND last_lease_node  = $3
		   AND status IN ('queued','claimed')
		   AND lease_expires_at > now()
	`, expires, string(token), ownerID)
	if err != nil {
		return fmt.Errorf("sched: pg lease renew: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return l.classifyRenewMiss(ctx, token, ownerID)
	}
	return nil
}

// classifyRenewMiss inspects why an UPDATE returned 0 rows.
// Distinct from classifyLookupMiss because Renew cares about
// ownership (last_lease_node) first, freshness second.
func (l *PgLeaser) classifyRenewMiss(ctx context.Context, token LeaseToken, ownerID string) error {
	var node string
	var expires time.Time
	err := l.pool.QueryRow(ctx, `
		SELECT last_lease_node, lease_expires_at
		  FROM job_tasks
		 WHERE lease_token = $1
	`, string(token)).Scan(&node, &expires)
	if errors.Is(err, classifyNoRows) {
		return ErrLeaseNotFound
	}
	if err != nil {
		return fmt.Errorf("sched: pg lease renew classify: %w", err)
	}
	if node != ownerID {
		return fmt.Errorf("%w: owner=%s", ErrLeaseHeldByOther, node)
	}
	return ErrLeaseExpired
}

// Release clears the lease. Idempotent: a second Release on the
// same token returns ErrLeaseNotFound (no row matches the WHERE).
// In production, the terminal-state UPDATE (JobTaskMarkTerminal)
// clears the lease columns as a side effect, so Release is rarely
// called directly outside tests.
func (l *PgLeaser) Release(ctx context.Context, token LeaseToken, ownerID string) error {
	tag, err := l.pool.Exec(ctx, `
		UPDATE job_tasks
		   SET lease_token      = NULL,
		       lease_expires_at = NULL
		 WHERE lease_token      = $1
		   AND last_lease_node  = $2
	`, string(token), ownerID)
	if err != nil {
		return fmt.Errorf("sched: pg lease release: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseNotFound
	}
	return nil
}

// Lookup reads the (key, expires_at, owner) tuple by token. Returns
// ok=false on a missing OR expired token; the caller treats both
// the same way (the lease is no longer mine — audit + give up).
func (l *PgLeaser) Lookup(ctx context.Context, token LeaseToken) (string, time.Time, string, bool, error) {
	var runID string
	var taskIndex int
	var expires time.Time
	var owner string
	err := l.pool.QueryRow(ctx, `
		SELECT run_id, task_index, lease_expires_at, last_lease_node
		  FROM job_tasks
		 WHERE lease_token = $1
	`, string(token)).Scan(&runID, &taskIndex, &expires, &owner)
	if errors.Is(err, classifyNoRows) {
		return "", time.Time{}, "", false, nil
	}
	if err != nil {
		return "", time.Time{}, "", false, fmt.Errorf("sched: pg lease lookup: %w", err)
	}
	if !l.now().Before(expires) {
		return "", time.Time{}, "", false, nil
	}
	return formatLeaseKey(runID, taskIndex), expires, owner, true, nil
}

// parseLeaseKey is the canonical key encoder/decoder pair. The "\x00"
// separator is illegal in a UUID string + can't collide with the
// task_index ASCII digits.
func parseLeaseKey(key string) (string, int, error) {
	for i := 0; i < len(key); i++ {
		if key[i] == 0x00 {
			runID := key[:i]
			var taskIndex int
			for j := i + 1; j < len(key); j++ {
				c := key[j]
				if c < '0' || c > '9' {
					return "", 0, fmt.Errorf("%w: malformed task index", ErrInvalidLeasePolicy)
				}
				taskIndex = taskIndex*10 + int(c-'0')
			}
			return runID, taskIndex, nil
		}
	}
	return "", 0, fmt.Errorf("%w: missing separator", ErrInvalidLeasePolicy)
}

func formatLeaseKey(runID string, taskIndex int) string {
	// int → ASCII decimal, no padding.
	if taskIndex == 0 {
		return runID + "\x000"
	}
	var buf [20]byte
	i := len(buf)
	for n := taskIndex; n > 0; n /= 10 {
		i--
		buf[i] = byte('0' + n%10)
	}
	return runID + "\x00" + string(buf[i:])
}

// classifyNoRows is the sentinel check used by classifyAcquireMiss /
// classifyRenewMiss / classifyLookupMiss. We compare against the
// production pgx.ErrNoRows (re-exported via the pgx import added in
// CR-3) instead of a fresh errors.New() — a local sentinel never
// matches the real pgx sentinel, so error classification silently
// fell through to fmt.Errorf("sched: pg lease … classify: %w", err)
// for every "no rows" case (CR-3 / code-review #3). The package
// owns a test-friendly alias rather than re-declaring the literal —
// every existing call site (lines 201, 248) just references the alias.
var classifyNoRows = pgx.ErrNoRows
