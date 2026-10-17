# Post-capture publication and activation observation

Status: accepted

## Context

The scheduler observer distinguishes publication_pending and snapshot_recorded,
but its eligible-prime warning intentionally excludes both. Publication outcome
counters and completed hosting/stage histograms cannot describe outstanding
work. The readiness stage advances after promotion, so its timestamp cannot
measure the preceding activation wait. Spec section 12 permits private operator
evidence; section 17 and Appendix D do not authorize changing execution gates.

## Decision

Add a read-only optional store observation with eight bounded classes:
publication_pending, activation_pending, dependency_held, compatibility_held,
hosting_verifying, hosting_retry, gate_overdue and unknown. Return count,
unknown-age subset and oldest known boundary per class, through one SQLC
aggregate or one MemStore lock. There is no deployment batch cap. This is a
shared fleet view, matching imaged's unscoped snapshot_written consumer, not
image-preparation's node-local recovery scope. Never sum replicas.

Observe nondeleted apps' snapshotting deployments, excluding readiness and
intentional workload holds. Scoped ephemeral-secret flows are excluded because
they intentionally activate without a retained snapshot. Worker/job instance
modes cannot supply parked-capture evidence. Current non-stale init publication
must be newer than snapshot_prepare; warm publication cannot prove init
activation. With no current init, a post-stage parked capture with fresh
wake/scope/runtime evidence establishes publication-pending. Reuse the existing
receipt freshness/requirement predicates and latest-64-instances-per-app
lookback, including the older UUIDv7 wake boundary. No unbounded identifiers,
configuration, commands, payloads, error text or secrets become metric labels.

Publication timing starts at the parked capture timestamp, not stage/deployment
creation. Missing parked history retains unknown age. Activation timing starts
at committed init publication. Retained compatibility/dependency/hosting
boundaries may move it forward, never backward. Valid old gate/receipt history
before the current stage is ignored on rollback. A hold re-observed after
rollback retains its classification but has unknown age when its first-held
time belongs to the old attempt. Missing, malformed, future or
inconsistent qualifying evidence belongs to unknown. No migration or invented
historical time is needed. These times do not measure continuous class residence.

Compatibility holds precede dependency/hosting classification. Dependency waits
reflect the last durable gate evaluation, not a fresh dependency eligibility
check. A failed dependency gate or an expired waiting/hosting-retry deadline
remaining nonterminal is gate_overdue. Within-budget dependency waits and
compatibility holds remain separate from generic activation delay. Hosting retry
uses the closed verification recovery codes; it does not blame an app or the
platform for an unauthenticated response. Completed verification does not prove
that receipt persistence or promotion succeeded: the row remains activation_pending.

Attach an owned collector through Handler.WithOpsMetrics, preserving repeated,
zero-sink, detach and reattach behavior. Run independently before Loop.Run's
startup drains, immediately and every 30 seconds, with a three-second read
budget. Freshness expires 120 seconds after read start. Scrapes use RAM only
and release locks before emission. Read failures omit counts and freeze the last
success timestamp. Stop withdraws expectation without claiming work drained.

Count actual activation-handler returns by input (snapshot/ready), last reached
phase and success/error/deferred/held/canceled. A success at evidence may be a
warm publication or redelivery, not completed activation. Nil returns may also
acknowledge discarded captures or terminal verdicts. Error includes app and
policy verdicts, so it is not an infrastructure failure numerator. Preserve all
return values, lifecycle calls, gate order, ownership/token fences and replay.
Reuse existing publication write_error/policy_error outcomes for the focused
publication-error warning; exclude stale captures and RAM/ephemeral rejections.

Add three recordings and six internal warnings: unavailable telemetry held five
minutes; publication or activation boundary older than five minutes held five
minutes; failed/expired gate retained five minutes; unknown evidence 15 minutes;
publication errors above 10% with at least three per rolling five minutes, held
five minutes. Budgets are initial triage thresholds, not measured SLOs. No
frontend, rollout, deployment, public-status or automatic recovery change.

## Validation and adversarial review

Compare memory and real PostgreSQL over empty and >256-row fleets, fresh parked
captures, exact parked/publication boundaries, init versus warm, runtime
invalidation, hosting begin/retry/complete, dependency wait/failure, compatibility
holds, rollback with old receipts, ephemeral exclusions, cancellation and
malformed stored evidence. Test actual handler replay/retry outcomes, concurrent
scrapes during blocked reads, late cancellation, stale/future data and wiring.

Promtool fixtures cover thresholds, holds, no new arrivals, mixed/unknown ages,
missing/inconsistent telemetry, initialization, deadline warnings, publication
error ratios/volume/rejections, shutdown and replica isolation. Preserve existing
rule/catalog definitions, regenerate SQLC/catalogs, run affected race, policy and
lint checks. No VM lifecycle behavior changes, so native metal/leak acceptance
is outside this instrumentation slice.

Adversarial review: parking does not prove artifact acceptance; publication does
not prove live traffic; readiness-stage timing misses activation; old rollback
captures cannot establish new progress; retry counters cannot replace durable
backlog; observed holds are not fresh authorization; error returns can be app
verdicts; incomplete evidence must not become zero; and another replica's valid
evidence cannot repair an invalid target's observation.
