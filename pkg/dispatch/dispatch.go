package dispatch

import (
	"database/sql"
	"time"
)

// JobKind discriminates the two drain families that share this
// contract. Today schedd owns exactly two: the unified invocations
// drain (async / queue / delayed-task / cron / replay) and the
// trigger_records drain. New kinds (e.g. a future cron_records split)
// should be added here rather than carving a third drain ad-hoc.
type JobKind string

const (
	// JobKindInvocation is the row kind for invocations-table rows
	// (pkg/state/types.go:2479). All async_invoke / queue / delayed_task
	// / cron / replay / esm sources share this kind — they all flow
	// through pkg/sched/drain.go.
	JobKindInvocation JobKind = "invocation"

	// JobKindTriggerRecord is the row kind for trigger_records-table
	// rows (migrations/00297_triggers.sql:65). All kafka / nats /
	// redis_streams / sqs_compat / in-platform queue trigger sources
	// share this kind — they flow through pkg/sched/dispatch_triggers.go.
	JobKindTriggerRecord JobKind = "trigger_record"
)

// RetryPolicy is the durable retry contract. MaxAttempts caps total
// attempts across the row's lifetime (queue retry budget uses the
// same shape — see api.Limits.MaxQueueAttempts for the plan-wide
// default). BaseSeconds is the starting delay, doubled per attempt
// up to MaxSeconds. JitterSeconds is the symmetric jitter window
// expressed as a *fraction* of base (0.2 = ±20%, the value every
// Gregale producer uses today per the inline curve at
// pkg/sched/dispatch_triggers.go:1009).
//
// The zero value (MaxAttempts==0 && BaseSeconds==0) means "no
// policy — use the producer's default". Backoff on a zero-valued
// RetryPolicy returns 0, signalling the dispatcher to retry as soon
// as it can. Callers that require an explicit budget should refuse
// the zero value (see PR-B's apid handler).
type RetryPolicy struct {
	MaxAttempts   int     // total attempts incl. the first (1 = no retry)
	BaseSeconds   float64 // delay before attempt 1 (typical: 1.0)
	MaxSeconds    float64 // cap on the doubled base (typical: 300.0)
	JitterSeconds float64 // symmetric ± fraction of base (0.2 = ±20%)
}

// Zero reports whether the policy is unset. Drains fall back to
// producer defaults (MaxQueueAttempts for queue rows, the trigger's
// max_attempts column for trigger records, etc.) when Zero() is true.
func (p RetryPolicy) Zero() bool {
	return p.MaxAttempts == 0 && p.BaseSeconds == 0 && p.MaxSeconds == 0 && p.JitterSeconds == 0
}

// DeadlinePolicy is the absolute-deadline + start-to-close-timeout
// pair. Either field can be unset; the drain treats the unset field
// as "no constraint".
//
// DeadlineAt is the wall-clock time after which the row is considered
// expired — the drain skips it and (per producer) routes it to the
// dead-letter path. StartToCloseTimeout bounds a single wake→invoke
// round-trip; a blown StartToCloseTimeout maps to outcome='timeout'
// (issue #791, migrations/00166). Both fields flow from the same
// async-job deadline knob in PR-B (per-invocation DeadlineAt +
// per-job task_timeout_s).
type DeadlinePolicy struct {
	// DeadlineAt is the absolute wall-clock expiration. sql.NullTime
	// (not *time.Time) so the JSON round-trip through the
	// retry_policy JSONB column lands as a real NULL on absent.
	DeadlineAt sql.NullTime

	// StartToCloseTimeout bounds one wake→invoke round-trip. Zero
	// means "no per-attempt timeout"; the drain uses the wake lease
	// (Drain.wakeLeaseSeconds) as the effective floor.
	StartToCloseTimeout time.Duration
}

// Zero reports whether the policy is unset.
func (p DeadlinePolicy) Zero() bool {
	return !p.DeadlineAt.Valid && p.StartToCloseTimeout == 0
}

// Lease is the CAS-with-token handle a claim produces. The same
// shape lives inline on pkg/state/pgstore.go:2656 for the
// live-migration path today; pkg/lease (PR-D) produces these via a
// generic Manager. ExpiresAt is the wall-clock instant after which
// the lease is considered abandoned by the holder and any other
// claimer may take over.
type Lease struct {
	Token     string
	ExpiresAt time.Time
}

// Expired reports whether the lease's wall-clock TTL has elapsed.
// Drains use this to decide between "renew the lease and continue"
// and "the holder is gone — requeue the row".
func (l Lease) Expired(now time.Time) bool {
	if l.Token == "" {
		return true
	}
	return !l.ExpiresAt.IsZero() && !now.Before(l.ExpiresAt)
}
