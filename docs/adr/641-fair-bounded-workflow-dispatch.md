# ADR-641: Fair bounded workflow dispatch

- **Status:** accepted
- **Date:** 2026-10-07
- **Extends:** ADR-081, ADR-191 and ADR-345
- **Decision:** Retain four workflow execution slots per scheduler. Each tick
  offers work to every slot; a busy slot coalesces its own ticks. A slot drains
  at most eight runs before returning. Claim only after reserving a slot, and
  execute each claimed run before claiming another.
- **Fairness:** Persist the most recent claim for each application and each
  tenant scope within that application. Choose eligible applications least
  recently served, then tenant scopes least recently served, then the oldest
  due run. Unserved scopes sort first. Ordinary runs have their own unscoped
  cursor so tenant traffic cannot starve them. Record both cursors atomically
  with a successful claim. Cursor rows survive run-history pruning and cascade
  with application/tenant deletion.
- **Capacity:** At most two unexpired running automation claims per application
  and one per tenant within an application, across scheduler replicas. Parked
  waits and pending retries release dispatch capacity. Strict app caps reserve
  capacity for other apps even when only one app has queued work. Existing
  definition `max_concurrent_runs` and action limits still apply independently: waits can
  continue to consume the definition's concurrency budget. Stale leases can be
  recovered without counting their abandoned workers as live claims.
- **Coordination:** A transaction advisory lock serializes only claim selection,
  lease reservation and cursor writes. No handler executes under that lock or
  with its database connection held. Keep the earlier definition concurrency
  lock and recheck capacity after acquiring it for rolling-upgrade safety.
  A five-second claim deadline bounds lock contention and database work.
  Native Customer Operations claims retain their separate coordinator custody.
- **Recovery:** Preserve existing step-timeout lease extensions, recovery
  behavior, durable generation fences and deployment pins. Cancellation stops
  batch admission before the next claim. Overflow leaves runs in the durable
  queue for a later tick; existing bounded work-pool metrics report saturation,
  coalescing and worker duration without tenant labels.
- **Limits:** This is fair dispatch among eligible scopes, not handler
  preemption, account-wide fairness or a completion-latency guarantee. Four
  different slow applications can occupy all four slots until they complete,
  park or time out. Foreach parallelism remains bounded separately. During a
  rolling update, earlier workers do not enforce the new application/tenant
  budgets or record service order; full fairness requires all workers upgraded.
- **Rollout:** Apply the cursor/index migration before upgrading schedulers.
  No customer schema or SDK changes are required. Do not drop the cursor table
  until upgraded schedulers have stopped using it.
- **Tradeoff:** Serial claim transactions favor predictable fairness and
  resource bounds on the current control plane. This lock can become a claim
  throughput bottleneck at larger fleet sizes; sharded claim ownership is a
  separate decision and must preserve the same capacity invariants.

All dispatcher ceilings live in `pkg/api/limits.go`. This changes control-plane
admission and queue service only; it introduces no VM lifecycle transitions.
