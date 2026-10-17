# ADR-1481: Opt-in directional tracker sync and automatic reopenings

Date: 2026-10-15

Status: Accepted

## Context

Explicit inbound pulls now protect owner review with an atomic app intent fence.
Unattended inbound changes also need a baseline that survives claims and polling,
records edits away and back, and does not invalidate unrelated issues. Provider
closures still lack the fixing-deployment evidence required for local resolution.

## Decision

Add per-link `direction` values `outbound`, `inbound`, and `both`. Omission retains
outbound behavior for old clients and existing rows. Enabling any direction still
requires aligned native/provider and source lifecycle snapshots. Inbound/both also
capture a source intent revision before provider verification and compare it in
the configuration transaction. Pending writes and live claims block establishing
an inbound baseline. Preserve verified Linear open/completed workflow mappings.
Expose direction and last `observed_state` in API/SDK metadata; support the same
selection through the dashboard and CLI `auto-sync --enable --direction ...`.
The CLI refuses inbound/both before delegation when an older server omits direction
metadata, and requires the update response to confirm the selected direction.

Separate the app-wide explicit-review fence from unattended source intent. Keep
its existing revision behavior, but add a global connection revision and per-issue
source guard rows backed by a shared monotonic PostgreSQL sequence. Source revision
is the greatest committed connection revision and guard revision among the current
canonical group's original members. Lifecycle/assignment/regression changes bump
issue guards. Group-link changes bump old/new roots and members, so regrouping away
and back remains visible. Split-rule/reference edits bump their original sources.
App ownership/status/deletion/provider binding, installation identity, Linear connection
replacement and blocked OAuth status advance the app connection revision. Worker
policy writes, ordinary traffic counters and normal token refresh do not change
source intent. Unrelated groups do not invalidate a baseline. Sequence gaps after
rollback are harmless; guard rows and actions commit together.

Inbound policies observe a fresh scoped provider read under the existing lease,
connection and group safeguards. Any local intent divergence pauses for review.
Both-direction policies may push an isolated local lifecycle change when the
provider still exactly matches its baseline. A local edit without a lifecycle
change or changes on both sides pause rather than silently accepting new authority.
Require the exact selected Linear workflow for inbound changes. Same-lifecycle
workflow edits require review.

Apply only provider reopenings automatically. Lock app, policy, then source fence;
verify owner, canonical/reference membership, baseline, source revision, direction,
claim identity and unexpired lease. Acquire the fence first and read the effective
source revision in a separate statement afterward; a lock wait must not preserve
an older MVCC snapshot of member guard rows. Reopen through the existing issue action with
tracker audit metadata, then store the resulting source revision and provider
baseline and complete the claim in the same transaction. Check wall-clock lease
expiry again at completion. The worker skips its ordinary finish after this atomic
receipt. Unknown commit outcomes recover through the committed baseline/claim; stale
finishes and duplicate workers cannot repeat or overwrite the reopening.

Persist the dispatch source revision before an outbound write in both mode.
Confirm an unknown write by exact observation without redispatch. A later local
edit cannot be acknowledged by that old dispatch: preserve its revision and pause
on recovery. Provider callbacks remain authenticated scheduling hints and cannot
supply lifecycle authority.

Hold provider resolutions as `blocked` with `fixing_deployment_required`, retaining
the old baseline and the observed resolved state. They do not resolve Gregale.
The existing explicit dashboard pull can recover this hold with owner-selected,
transactionally scoped deployment evidence, then pauses the policy. Conflicts and
holds never resume automatically; review alignment to re-enable or disable without
provider access. Existing outbound-only behavior remains unchanged.

## Consequences

GitHub and Linear gain opt-in automatic reopenings and durable directional recovery.
No automatic resolution or automatic deployment selection is introduced. Read-only
page loads still avoid provider I/O. The provider and local database cannot share
a transaction; a provider edit after the final read may need another review.

The append-only migration preserves source/direction history and does not support
rollback. Explicit dashboard review remains deliberately app-wide, while unattended
source tracking is restricted to the issue group and shared connection identity.
