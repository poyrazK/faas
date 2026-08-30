package dispatch

// Job is the read-only contract every drain input satisfies. The
// two schedd drains (pkg/sched/drain.go for invocations,
// pkg/sched/dispatch_triggers.go for trigger_records) read these
// fields once per row to make every retry / deadline / lease
// decision through the pkg/dispatch types, so per-row JSONB
// overrides added in PR-B (retry_policy, deadline_at,
// result_retention_until on invocations) and PR-C (same three on
// trigger_records) flow through the drain without forking the
// producer code.
//
// Implementations:
//
//   - *state.Invocation     — added in PR-B; today a synthetic
//     adapter (pkg/sched/drain_compile_test.go)
//     proves the interface compiles cleanly.
//   - *state.TriggerRecord  — added in PR-C; same pattern.
//
// All methods must be safe to call concurrently with the row's
// own producer writers (apid for invocations, the trigger
// dispatcher for trigger_records); they return the durable row
// state at call time, not a snapshot at claim time. Drains that
// need a stable snapshot must serialize the accessor's view with
// their claim's CAS predicate.
type Job interface {
	// Kind reports whether the row is an invocation or a
	// trigger_record. The dispatch path keys on this to choose
	// the right FSM (pending|dispatching|completed|... vs
	// pending|claimed|succeeded|retry|dead_letter).
	Kind() JobKind

	// ID is the row's primary key as a string (uuid today; the
	// generic T in Leaser is for future typed-id migrations).
	ID() string

	// AppID is the row's parent app. Drains use this to look up
	// the cap configuration (api.MustLimitsFor(plan)) and to
	// stamp the instance_id on successful claims.
	AppID() string

	// AccountID is the row's owning account. Per-account
	// concurrency caps (added in PR-B's account_async_quota
	// table) key on this.
	AccountID() string

	// Origin returns the producer-specific source name —
	// async_invoke / queue / delayed_task / cron / replay / esm
	// for invocations; kafka / nats / redis_streams / sqs_compat
	// / queue / cron for trigger_records. Drains log this on
	// every transition; it is not part of the FSM.
	//
	// Named Origin rather than Source because *state.Invocation
	// has a Source field of type InvocationSource (the typed
	// enum) and Go does not allow a method and field of the
	// same name on the same type.
	Origin() string

	// RetryPolicy returns the row's effective retry policy. Per
	// ADR-134 §6.7 the row's retry_policy JSONB column (added in
	// PR-B / PR-C) wins over the producer default; the zero
	// value signals "use the producer default". Drains call
	// RetryPolicy().Zero() to detect the fallback path.
	RetryPolicy() RetryPolicy

	// Deadline returns the row's deadline + per-attempt timeout.
	// Zero() means no constraint; the drain proceeds as today.
	Deadline() DeadlinePolicy

	// CurrentAttempts returns the row's attempts counter at the
	// time of the call. Drains compare this against
	// RetryPolicy().MaxAttempts to decide between retry and
	// dead-letter.
	CurrentAttempts() int

	// ErrorText returns the row's last_error text — the
	// producer-facing message that explains why the row is in
	// its current state. Empty on a fresh row.
	//
	// Named ErrorText rather than LastError because
	// *state.Invocation has a LastError string field and Go
	// forbids a same-named method on the same type.
	ErrorText() string

	// Snapshot returns a JSON-serialised copy of the row's
	// durable fields at call time. The drain writes this into
	// the row's payload snapshot column on terminal transitions
	// (PR-B's result-retention path) so a later replay can
	// reconstruct the row without re-reading the table.
	//
	// Returns nil when the producer has not yet wired
	// snapshotting (today: both producers).
	Snapshot() []byte
}
