# Operator build admission and durable-worker observation

Status: accepted

## Context

Queue depth/age and running-build recovery are now observable, but operators
still need logs to explain why queued work is not starting. A slot denial can
return `ErrNoSlot` even when its requeue write failed, leaving a running claim.
A poll iteration executes the whole build, so missing two-second poll ticks
does not establish a stalled worker. An unsuccessful claim can mean affinity
deferral or another claimant, not just an empty queue. Spec section 12 supports
these diagnostics; section 17 and Appendix D require no admission, execution or
rollout change for this observation.

## Decision

Read reserved slots from `Builderd.activeSlots` under the allocator's existing
lock, and export the actual `maxBuilderSlots` limit. Both are process-local VM
admission state, not database running counts or resident-VM inventory. Cache
hits and pre-slot preparation remain outside this gauge. Slot decisions use
the closed outcomes `granted`, `limit`, and `policy`. Saturation alone raises
no new alert and cannot enable an opportunistic second slot.

Count completed claim calls at both actual claim boundaries with source
`poll`/`notification` and outcome `claimed`/`not_claimed`/`error`/`canceled`.
Preserve claim selection, affinity, fairness and return semantics. Describe
`ErrNotFound` as no eligible claim rather than proof of empty fleet state.

Instrument the existing claim-fenced requeue helper with fixed reasons
`no_slot`, `source_unavailable`, `context_canceled`, and `lookup_failure`.
Outcomes are `accepted`, `not_requeued`, `error`, and `canceled`. A stale/missing
claim is `not_requeued`; a caller-cancellation error is canceled only if it
matches the caller's canceled context. A store deadline while the caller is
live remains an error. Successful calls do not prove a row remains queued after
a concurrent claimant. Retry counters count individual store calls, not unique
builds. Never label metrics with build/tenant IDs, error text or denial strings.

Observe the durable polling loop separately from notification work. Publish a
coherent memory snapshot of initialization, last iteration start/completion,
in-flight state, polling expectation, interval and effective busy allowance.
Count fixed iteration response outcomes. Completion includes idle and failed
iterations; it means progress of `ProcessNext`, not a successful build or VM
start. Scrapes read RAM only and cannot renew timestamps. The existing loop's
scheduling, error handling and exit behavior remain unchanged.

Parent cancellation or intentional drain withdraws polling expectation. An
unexpected `context.Canceled` result with a live parent still exits as before,
but retains expectation so the otherwise-live daemon cannot silently lose its
durable poller. A worker that never starts uses initialization as its reference.

Add three recordings for complete queue telemetry, progress age and progress
allowance. Validate phase/timestamp coherence, nonfuture times, positive budgets,
slot usage within its limit, and the initialized closed counter vocabularies.
Join only the same job/instance; another builder cannot repair missing evidence.
Missing or invalid evidence produces an independent internal telemetry warning.

Internal progress warning: idle age above two effective poll intervals, or
in-flight age above the effective running-build recovery threshold plus two
intervals, held for five minutes. The threshold already includes configured
build execution and host teardown margin. It is an observation allowance, not
a new enforced timeout. It can indicate a blocked store/driver or exited loop
without proving a VM remains alive. Intentional shutdown suppresses the warning.

Internal operational failure warnings: more than 10% errors and at least three
errors within each rolling five-minute window, continuously for five minutes.
Claims are evaluated separately per source, requeues separately per reason.
Canceled calls are excluded; protective requeue fences are also excluded from
the requeue ratio. Ordinary no-claim responses are healthy claim attempts.
These thresholds warn on sustained failures without treating one transient
burst, ordinary saturation, or customer build errors as a worker outage.

## Validation

Race-test actual claim calls, failed no-slot requeue with preserved legacy
return, requeue recovery/fencing, authoritative slot gauges and idempotent
concurrent release. Extend existing success/cache tests to verify slot ownership
and cache bypass. Use named-field statetest fakes with explicit dispatch, never
new store-embedding fakes. Exercise blocked poll calls, scrape isolation,
intentional shutdown, unexpected exit and fixed response outcomes.

Promtool fixtures cover exact idle/busy allowance and hold boundaries, healthy
long builds, saturation, missing/invalid/incomplete telemetry, source/instance
isolation, persistent failures, protective fences, cancellation, isolated bursts,
recovery and absent installations. Refresh both catalogs, retain zero metric
emission exceptions, and run builder startup, dispatch, policy and lint checks.

Adversarial review focuses on falsely calling no-claim empty, masking requeue
errors behind slot denial, alerting during a healthy long build, cross-instance
evidence mixing, and changing claim/slot/recovery behavior through instrumentation.
