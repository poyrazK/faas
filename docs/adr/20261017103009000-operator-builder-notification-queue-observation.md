# Operator builder notification queue observation

Status: accepted

## Context

Durable build wait, claims, admission and recovery are observable. The bounded
LISTEN/NOTIFY work queues still collapse duplicate IDs, full channels and stopping
into a false enqueue result and one debug message. Existing DB notify hub metrics
measure upstream transport, and build-cancel outcome counters measure actual
driver calls. Neither describes delay or drops between those boundaries.
Spec section 12 supports separate operator diagnostics. Section 17 and Appendix D
require no execution, ownership, frontend or rollout change for this observation.

## Decision

Observe the existing two queues with the closed label `queue=build|cancel`.
Keep channel capacity, worker count, nonblocking enqueue, duplicate coalescing,
handler calls, context handling and durable recovery unchanged. Store the accepted
time in the existing pending-ID set. Replace it with zero immediately before
handler dispatch, and delete the entry after the handler returns as before.
Duplicate IDs never renew a waiting timestamp.

Export waiting IDs, oldest waiting enqueue timestamp (zero when empty), active
handlers, actual buffered capacity, configured workers and enabled expectation.
Waiting includes the bounded dispatch handoff between channel receive and handler
entry. A worker can receive before obtaining the pending-set lock, so total
waiting plus active is bounded by capacity plus workers; waiting can briefly
exceed buffered capacity. Active handlers have no waiting age. These gauges do
not establish durable build count, resident VMs, or successful VM cancellation.

Initialize all five enqueue outcomes at zero: `accepted`, `coalesced`, `full`,
`stopping`, and `invalid`. Coalescing includes queued and active IDs. Full means
the hint was dropped, leaving durable polling or persisted cleanup as recovery.
Shutdown and invalid inputs remain separate from loss. A nil queue has no
instance and emits no attempt. Do not label metrics with IDs or error strings.

Scrapes copy bounded RAM under the existing queue mutex and release it before
emitting samples. They perform no database, driver or handler calls. Derive age
from the enqueue timestamp in Prometheus so age grows without new arrivals or
timestamp renewal. Canceling the parent withdraws expectation without resetting
pending/active evidence or claiming drain completed. Preserve WaitContext behavior.

Validate each queue's complete state, timestamp/count coherence, positive limits,
active and total occupancy bounds, binary expectation and all five counter
outcomes. Keep job, instance and queue identity for joins. Generate both expected
queue identities from a successful builder scrape so an entirely missing queue
cannot disappear into a healthy sibling. Missing or invalid evidence suppresses
derived age and pressure warnings and raises a separate telemetry warning after
five minutes. A failed scrape remains the existing target-health responsibility.

Add internal warnings only:

- Waiting age above 300 seconds for builds or 60 seconds for cancellation, held
  for five minutes. These measure fast-path delay, including legitimate backlog
  behind busy handlers; they are not stalled-worker diagnoses or enforced
  timeouts. A healthy long-running active build alone has zero waiting age.
- Full drops above 10% of accepted/full attempts and at least three drops in
  each rolling five-minute window, continuously for five minutes. Exclude
  coalescing, shutdown and invalid inputs from the denominator. Evaluate each
  queue and builder independently so unrelated throughput cannot hide a failure.
- Separate missing/inconsistent telemetry warning per expected queue.

Both operational warnings require complete telemetry and enabled expectation.
Leave existing durable queue/worker/reaper rules and cancellation RPC counters
independent. No queue resizing, scheduling changes, automatic remediation or
deployment is introduced.

## Validation and adversarial review

Race-test real enqueue/dispatch/completion boundaries for both queue labels,
queued and active duplicates, full rejection, no timestamp renewal, bounded
scrapes under blocked handlers, reuse after completion, drain and canceled
contexts. Verify a driver that ignores cancellation remains active in telemetry
after a timed-out shutdown wait. Exercise normalized constructor limits.

Promtool fixtures cover exact age and hold boundaries, growing age without new
arrivals, sustained drops, benign coalescing, isolated bursts, minimum drop
volume, ratio thresholds, counter resets, shutdown, recovery, healthy active
builds, missing queue/counter/state, malformed timestamps and occupancy,
instance/queue isolation, failed scrapes and absent installations. Regenerate
both catalogs, retain zero metric-emission exceptions, and run affected package,
startup, rule-policy and lint checks.

Adversarial review: waiting excludes active work; duplicates retain original age;
capacity refers to the existing buffer and includes a bounded handoff allowance
in validation; shutdown withdraws expectation without fabricating drain; success
of enqueue or handler return cannot be promoted to successful build/cancellation;
durable recovery and actual cancellation outcomes remain independent evidence.
