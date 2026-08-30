// Package dispatch defines the shared durable-async-job contract that
// the two schedd drains — pkg/sched/drain.go (invocations: async /
// queue / delayed-task / cron / replay) and pkg/sched/dispatch_triggers.go
// (trigger_records: kafka / nats / redis_streams / sqs_compat / cron /
// in-platform queue) — both consume. ADR-134.
//
// The package is intentionally NOT a unified drain; the two drains
// keep their own FSMs and SQL, but every retry / deadline / lease
// decision they make goes through the types here so the per-producer
// tables can be migrated onto the same contract gradually.
//
// Type taxonomy:
//
//   - RetryPolicy   — backoff curve (exponential with ±20% jitter,
//     5-minute cap) and attempt budget. Mirrors the
//     curve that has lived inline in
//     pkg/sched/dispatch_triggers.go:1009
//     since Move 1, now lifted into a value type so
//     per-row JSON overrides can flow through it.
//   - DeadlinePolicy — absolute deadline + start-to-close timeout. The
//     "deadline" half of the §6.7 async-job contract.
//   - Lease         — CAS-with-token handle. The pattern has lived
//     inline on pkg/state/pgstore.go:2656 since the
//     live-migration work; pkg/lease (PR-D) wires the
//     generic Manager that produces these.
//   - Job           — read-only interface both drain inputs implement
//     so future per-row policy overrides can flow
//     without breaking call sites.
//
// Producers (cron, queue :send, async-invoke, trigger dispatcher)
// gradually migrate onto the contract per ADR-134 §6; the first
// migration is PR-C (trigger_records). Invocations follow in PR-B.
package dispatch
