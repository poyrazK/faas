// Package leaser is the local lease contract (issue #1184 Workstream A,
// ADR-099 Decision 6): a lease token, its policy and the generic Leaser
// surface. schedd's job dispatch consumes it and vmmd's live-migration
// lease implements it.
//
// It used to live in pkg/sched, which made vmmd link the scheduler engine
// to implement four methods. Keep this package a leaf: it must not import
// the scheduler or the store. pkg/sched re-exports it under its old names
// (LeaseToken, LeasePolicy, Leaser, ErrLease*), and keeps the in-memory
// and Postgres implementations.
//
// Error messages keep their "sched:" prefix so log lines read as they did
// before the move.
package leaser

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Token is the opaque, comparable identity of an active lease. Persisted as
// a uuid column (job_tasks.lease_token, 00574). The underlying type is
// string to match the codebase convention (Cron.ID, App.ID).
type Token string

// Policy describes the lifetime knobs the caller hands to Acquire. The
// scheduler only ever sets TTL; MaxAttempts and MaxDuration are surfaced
// for the cron-DLQ replay path (issue #1184 Workstream E), which loops a
// lease across retries.
type Policy struct {
	// TTL is the lease duration. The caller MUST set TTL > 0; Acquire
	// rejects a zero value with ErrInvalidPolicy.
	TTL time.Duration

	// MaxAttempts is the upper bound on Renew calls before the lease is
	// forcibly expired. 0 means "no upper bound".
	MaxAttempts int

	// MaxDuration is the wall-clock cap from the initial Acquire
	// timestamp. 0 means "no upper bound".
	MaxDuration time.Duration
}

// Validate rejects zero-TTL and obviously broken inputs. Called by Acquire
// so callers can't construct a lease that immediately expires.
func (p Policy) Validate() error {
	if p.TTL <= 0 {
		return fmt.Errorf("%w: TTL must be > 0", ErrInvalidPolicy)
	}
	if p.MaxAttempts < 0 {
		return fmt.Errorf("%w: MaxAttempts must be >= 0", ErrInvalidPolicy)
	}
	if p.MaxDuration < 0 {
		return fmt.Errorf("%w: MaxDuration must be >= 0", ErrInvalidPolicy)
	}
	if p.MaxDuration > 0 && p.MaxDuration < p.TTL {
		return fmt.Errorf("%w: MaxDuration (%s) < TTL (%s)", ErrInvalidPolicy, p.MaxDuration, p.TTL)
	}
	return nil
}

// ErrInvalidPolicy marks a Policy that Acquire refuses.
var ErrInvalidPolicy = errors.New("sched: invalid lease policy")

// ErrNotFound marks a Lookup / Renew / Release on a token that does not
// exist (or already expired and was reaped).
var ErrNotFound = errors.New("sched: lease not found")

// ErrExpired marks a Lookup / Renew on a token whose lease_expires_at <
// now(). The reaper has likely already taken over the task; the caller
// should NOT retry — give up and audit.
var ErrExpired = errors.New("sched: lease expired")

// ErrHeldByOther marks a Renew / Release on a token whose stored owner_id
// does not match the caller. Distinct from ErrNotFound so the caller can
// tell "the lease was stolen" from "the lease was never mine" in the audit
// log.
var ErrHeldByOther = errors.New("sched: lease held by another owner")

// Leaser is the generic lease surface. The type parameter T is the
// implementation-specific handle — the in-memory store uses a record
// pointer, the Postgres store a row pointer. Callers never construct T
// directly; they receive it back from Acquire.
//
// Acquire returns a fresh Token and the T handle. Renew extends the lease
// in place. Release clears the lease (idempotent: a second Release on the
// same token returns ErrNotFound, NOT a panic). Lookup is a non-mutating
// read for the audit / idempotency check path.
//
// Implementations MUST be safe for concurrent use.
type Leaser[T any] interface {
	Acquire(ctx context.Context, key string, policy Policy, ownerID string) (Token, T, error)
	Renew(ctx context.Context, token Token, ownerID string, ttl time.Duration) error
	Release(ctx context.Context, token Token, ownerID string) error
	Lookup(ctx context.Context, token Token) (key string, expiresAt time.Time, ownerID string, ok bool, err error)
}
