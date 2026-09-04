// Package sched — local lease primitive for issue #1184 Workstream A.
//
// ADR-099 Decision 6 requires "lease ownership and idempotency" for
// dispatched job tasks: every job_task a schedd dispatches carries a
// lease_token (UUID v7) + lease_expires_at. The token is the
// idempotency key for the post-exit DGRAM (HandleJobExit) and the
// signal for any other schedd that "the lease is mine; don't touch
// this task until I either release it or it expires". The reaper
// (M6) scans for tasks where lease_expires_at < now() and treats them
// as fair game for SIGKILL + MarkTaskTerminal(timeout).
//
// This file defines the local primitive. It does NOT depend on
// pkg/dispatch because ADR-134 is staged but not yet shipped; when
// ADR-134 lands, the implementation in lease_pg.go gets swapped for
// pkg/dispatch.Job-leaser without changing the Leaser[T] API surface
// here. M5 (Engine.WakeJob) consumes Leaser[T] only.
//
// Why a generic Leaser[T]: a future Workstream C
// (execution_mode='job' inside cron) and Workstream E (cron
// trigger_records DLQ replay) want the SAME lease primitive with
// different backing stores. The generic interface lets the future
// pkg/dispatch.Leaser[T] drop in without an Engine.WakeJob edit.
package sched

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"
)

// LeaseToken is the opaque, comparable identity of an active lease.
// Persisted as a uuid column (job_tasks.lease_token, 00574). The
// underlying type is string to match the codebase convention (Cron.ID,
// App.ID) — uuid.UUID is reserved for the few places where the v7
// generation logic actually matters.
type LeaseToken string

// LeasePolicy describes the lifetime knobs the caller hands to
// Acquire. The sched engine only ever sets TTL; MaxAttempts and
// MaxDuration are surfaced for the future cron-DLQ replay path
// (issue #1184 Workstream E) which loops a lease across retries.
type LeasePolicy struct {
	// TTL is the lease duration. The caller MUST set TTL > 0; Acquire
	// rejects a zero value with ErrInvalidLeasePolicy.
	TTL time.Duration

	// MaxAttempts is the upper bound on Renew calls before the lease
	// is forcibly expired. 0 means "no upper bound". The dispatch
	// tick (M5) does not use MaxAttempts; the cron-DLQ path will.
	MaxAttempts int

	// MaxDuration is the wall-clock cap from the initial Acquire
	// timestamp. 0 means "no upper bound".
	MaxDuration time.Duration
}

// Validate rejects zero-TTL + obviously-broken inputs. Called by
// Acquire so callers can't construct a lease that immediately expires.
func (p LeasePolicy) Validate() error {
	if p.TTL <= 0 {
		return fmt.Errorf("%w: TTL must be > 0", ErrInvalidLeasePolicy)
	}
	if p.MaxAttempts < 0 {
		return fmt.Errorf("%w: MaxAttempts must be >= 0", ErrInvalidLeasePolicy)
	}
	if p.MaxDuration < 0 {
		return fmt.Errorf("%w: MaxDuration must be >= 0", ErrInvalidLeasePolicy)
	}
	if p.MaxDuration > 0 && p.MaxDuration < p.TTL {
		return fmt.Errorf("%w: MaxDuration (%s) < TTL (%s)", ErrInvalidLeasePolicy, p.MaxDuration, p.TTL)
	}
	return nil
}

// ErrInvalidLeasePolicy marks a LeasePolicy that Acquire refuses.
var ErrInvalidLeasePolicy = errors.New("sched: invalid lease policy")

// ErrLeaseNotFound marks a Lookup / Renew / Release on a token
// that does not exist (or already expired and was reaped).
var ErrLeaseNotFound = errors.New("sched: lease not found")

// ErrLeaseExpired marks a Lookup / Renew on a token whose
// lease_expires_at < now(). The reaper has likely already taken over
// the task; the caller should NOT retry — give up + audit.
var ErrLeaseExpired = errors.New("sched: lease expired")

// ErrLeaseHeldByOther marks a Renew / Release on a token whose
// stored owner_id does not match the caller. Distinct from
// ErrLeaseNotFound so the caller can tell "the lease was stolen"
// from "the lease was never mine" in the audit log.
var ErrLeaseHeldByOther = errors.New("sched: lease held by another owner")

// Leaser is the generic lease surface M5 (Engine.WakeJob /
// HandleJobExit) consumes. The type parameter T is the
// implementation-specific handle — the in-memory store uses
// *memLeaseRecord, the Postgres store uses a row pointer. Callers
// never construct T directly; they receive it back from Acquire.
//
// Acquire returns a fresh LeaseToken + the T handle. Renew extends
// the lease in place. Release clears the lease (idempotent: a second
// Release on the same token returns ErrLeaseNotFound, NOT a panic).
// Lookup is a non-mutating read for the audit / idempotency check
// path; HandleJobExit calls Lookup(token) before honouring the
// incoming DGRAM.
//
// Implementations MUST be safe for concurrent use. The dispatch tick
// (1s ticker, M5) and the DGRAM listener run on different goroutines.
type Leaser[T any] interface {
	Acquire(ctx context.Context, key string, policy LeasePolicy, ownerID string) (LeaseToken, T, error)
	Renew(ctx context.Context, token LeaseToken, ownerID string, ttl time.Duration) error
	Release(ctx context.Context, token LeaseToken, ownerID string) error
	Lookup(ctx context.Context, token LeaseToken) (key string, expiresAt time.Time, ownerID string, ok bool, err error)
}

// JobLeaser is the type-erased lease surface used by Engine.WakeJob. The
// generic Leaser retains implementation-specific handles for callers that
// need them, but the job lifecycle only needs the token. Keeping this adapter
// explicit avoids the false Leaser[any] assertion: Leaser[*memLeaseRecord]
// and Leaser[pgLeaseRecord] are not assignable to Leaser[any] in Go.
type JobLeaser interface {
	Acquire(ctx context.Context, key string, policy LeasePolicy, ownerID string) (LeaseToken, error)
	Release(ctx context.Context, token LeaseToken, ownerID string) error
}

type jobLeaserAdapter[T any] struct {
	inner Leaser[T]
}

func (a jobLeaserAdapter[T]) Acquire(ctx context.Context, key string, policy LeasePolicy, ownerID string) (LeaseToken, error) {
	tok, _, err := a.inner.Acquire(ctx, key, policy, ownerID)
	return tok, err
}

func (a jobLeaserAdapter[T]) Release(ctx context.Context, token LeaseToken, ownerID string) error {
	return a.inner.Release(ctx, token, ownerID)
}

// AdaptJobLeaser exposes a concrete generic lease implementation through the
// token-only surface Engine.WakeJob consumes.
func AdaptJobLeaser[T any](l Leaser[T]) JobLeaser {
	if l == nil {
		return nil
	}
	return jobLeaserAdapter[T]{inner: l}
}

// ----------------------------------------------------------------------------
// In-memory implementation (tests + schedd-local references)
// ----------------------------------------------------------------------------

// MemLeaser is the reference implementation. The Postgres-backed
// Leaser[pgLeaseRecord] in lease_pg.go is the production path; this
// one exists so pkg/sched/jobs_test.go can exercise the Leaser[T]
// surface without a DB round-trip and so pkg/sched/reaper_jobs_test.go
// can simulate expired leases deterministically.
//
// Concurrency: a single sync.Mutex guards the records map. The map
// is small (≤ a few thousand live leases) so the lock-hold time is
// microseconds; the production Postgres path trades the lock for a
// SQL round-trip.
type MemLeaser struct {
	mu      sync.Mutex
	records map[LeaseToken]*memLeaseRecord
	now     func() time.Time // injectable clock for tests
}

// memLeaseRecord is the in-memory lease row.
type memLeaseRecord struct {
	token       LeaseToken
	key         string
	ownerID     string
	acquiredAt  time.Time
	expiresAt   time.Time
	attempts    int
	maxAttempts int
	maxDuration time.Duration
}

// NewMemLeaser returns an empty in-memory Leaser[*memLeaseRecord].
// now defaults to time.Now; tests pass a deterministic clock.
func NewMemLeaser(now func() time.Time) *MemLeaser {
	if now == nil {
		now = time.Now
	}
	return &MemLeaser{
		records: make(map[LeaseToken]*memLeaseRecord),
		now:     now,
	}
}

// Acquire mints a fresh LeaseToken (UUID v7-shaped string) and stores
// the record under it. Renew attempts on this token must come from
// the same ownerID; Release likewise.
//
// Failure modes:
//   - ErrInvalidLeasePolicy: zero TTL / negative MaxAttempts / etc.
//   - (no error) on a (key, ownerID) re-acquire: returns the existing
//     lease refreshed to TTL. This is the "renew-after-expiry" path
//     for a schedd that lost its connection mid-task and is
//     re-dispatching. A re-acquire from a DIFFERENT ownerID is a
//     bug — Acquire refuses with ErrLeaseHeldByOther.
func (l *MemLeaser) Acquire(ctx context.Context, key string, policy LeasePolicy, ownerID string) (LeaseToken, *memLeaseRecord, error) {
	if err := policy.Validate(); err != nil {
		return "", nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	// Re-acquire path: scan for an existing lease on (key, ownerID)
	// and refresh it. This is the idempotent re-dispatch case (M5
	// schedd restart picks up a task whose lease_expires_at is still
	// in the future because vmmd is still alive).
	for tok, rec := range l.records {
		if rec.key == key && rec.ownerID == ownerID {
			rec.attempts++
			rec.expiresAt = l.now().Add(policy.TTL)
			if policy.MaxAttempts > 0 && rec.attempts > policy.MaxAttempts {
				delete(l.records, tok)
				return "", nil, fmt.Errorf("%w: attempts=%d > MaxAttempts=%d", ErrInvalidLeasePolicy, rec.attempts, policy.MaxAttempts)
			}
			return tok, rec, nil
		}
		// Different owner on the same key: refuse. Two schedds must
		// not own the same task.
		if rec.key == key && rec.ownerID != ownerID {
			return "", nil, fmt.Errorf("%w: key=%s owner=%s", ErrLeaseHeldByOther, key, rec.ownerID)
		}
	}

	tok := LeaseToken(newUUIDv7Like())
	rec := &memLeaseRecord{
		token:       tok,
		key:         key,
		ownerID:     ownerID,
		acquiredAt:  l.now(),
		expiresAt:   l.now().Add(policy.TTL),
		attempts:    1,
		maxAttempts: policy.MaxAttempts,
		maxDuration: policy.MaxDuration,
	}
	l.records[tok] = rec
	return tok, rec, nil
}

// Renew extends an existing lease's expires_at by ttl. The ownerID
// check is load-bearing: a leaked token must NOT be renewable by a
// different schedd. Returns ErrLeaseNotFound if the token is gone,
// ErrLeaseExpired if the lease already lapsed, ErrLeaseHeldByOther
// if the ownerID doesn't match.
func (l *MemLeaser) Renew(ctx context.Context, token LeaseToken, ownerID string, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("%w: renew TTL must be > 0", ErrInvalidLeasePolicy)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.records[token]
	if !ok {
		return ErrLeaseNotFound
	}
	if rec.ownerID != ownerID {
		return fmt.Errorf("%w: owner=%s", ErrLeaseHeldByOther, rec.ownerID)
	}
	if !l.now().Before(rec.expiresAt) {
		delete(l.records, token)
		return ErrLeaseExpired
	}
	rec.attempts++
	if rec.maxAttempts > 0 && rec.attempts > rec.maxAttempts {
		delete(l.records, token)
		return fmt.Errorf("%w: attempts=%d > MaxAttempts=%d", ErrInvalidLeasePolicy, rec.attempts, rec.maxAttempts)
	}
	rec.expiresAt = l.now().Add(ttl)
	if rec.maxDuration > 0 {
		cap := rec.acquiredAt.Add(rec.maxDuration)
		if rec.expiresAt.After(cap) {
			rec.expiresAt = cap
		}
	}
	return nil
}

// Release clears a lease. Idempotent: a second Release returns
// ErrLeaseNotFound, NOT a panic. The ownerID check is load-bearing
// for the same reason as Renew.
func (l *MemLeaser) Release(ctx context.Context, token LeaseToken, ownerID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.records[token]
	if !ok {
		return ErrLeaseNotFound
	}
	if rec.ownerID != ownerID {
		return fmt.Errorf("%w: owner=%s", ErrLeaseHeldByOther, rec.ownerID)
	}
	delete(l.records, token)
	return nil
}

// Lookup is the read-only token resolver. Returns ok=false on a
// missing OR expired token; the caller treats both the same way
// (the lease is no longer mine — audit + give up).
func (l *MemLeaser) Lookup(ctx context.Context, token LeaseToken) (string, time.Time, string, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.records[token]
	if !ok {
		return "", time.Time{}, "", false, nil
	}
	if !l.now().Before(rec.expiresAt) {
		delete(l.records, token)
		return "", time.Time{}, "", false, nil
	}
	return rec.key, rec.expiresAt, rec.ownerID, true, nil
}

// ReapExpired returns the tokens that have already lapsed. The
// caller (reaper_jobs.go, M6) uses this list to drive
// MarkTaskTerminal(timeout) without holding the MemLeaser mutex
// across the SQL round-trip.
//
// Returns a copy of the tokens; safe to iterate without l.mu.
func (l *MemLeaser) ReapExpired() []LeaseToken {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []LeaseToken
	now := l.now()
	for tok, rec := range l.records {
		if !now.Before(rec.expiresAt) {
			out = append(out, tok)
		}
	}
	return out
}

// Size returns the live-lease count. Used by tests + the
// jobs_concurrent gauge's lease-pool denominator.
func (l *MemLeaser) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.records)
}

// newUUIDv7Like returns a UUID-shaped string. We don't need
// cryptographic uniqueness — the Postgres path uses
// gen_random_uuid() + a job_tasks_lease_uniq partial index for
// collision safety; the in-memory store is per-process. uuid.NewV7
// would be ideal but the codebase pins google/uuid or
// github.com/google/uuid elsewhere; the in-memory store avoids that
// dep by minting crypto/rand hex. v7-shape (time-ordered) keeps
// debug logs readable.
func newUUIDv7Like() string {
	var b [16]byte
	// high 4 bytes: unix-ish millis so logs sort by acquisition time.
	ms := uint64(time.Now().UnixNano() / int64(time.Millisecond))
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	// version 7 in the high nibble of byte 6
	b[6] = (b[6] & 0x0f) | 0x70
	// variant 10 in the high nibble of byte 8
	b[8] = (b[8] & 0x3f) | 0x80
	// fill the rest from crypto/rand
	crandRead(b[4:6])
	crandRead(b[6:8])
	crandRead(b[8:10])
	crandRead(b[10:])
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3]),
		uint16(b[4])<<8|uint16(b[5]),
		uint16(b[6])<<8|uint16(b[7]),
		uint16(b[8])<<8|uint16(b[9]),
		uint64(b[10])<<32|uint64(b[11])<<24|uint64(b[12])<<16|uint64(b[13])<<8|uint64(b[14]))
}

// crandRead wraps crypto/rand.Read; failures (which should never
// happen on Linux) are non-recoverable so we panic with the
// underlying error. Lease tokens MUST be unique across a schedd
// process lifetime; falling back to math/rand would silently
// allow collisions in the MemLeaser.
func crandRead(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("sched: crypto/rand.Read failed: %v", err))
	}
}
