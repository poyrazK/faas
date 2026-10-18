# ADR-1583: Default-off same-repository PR execution automation

- Status: implemented behind existing default-off CI gates; native qualification pending
- Date: 2026-10-17
- Extends: ADR-1656, ADR-202610180816, ADR-1581 and ADR-1582

## Decision

Extend saved automation policies with explicit `pull_request_branches` filters
for PR base branches. Either push or PR filters must be nonempty; each list is
bounded by the existing 16-filter limit and must be a subset of the corresponding
binding filters. Omitted PR filters remain empty. Identical saves preserve the
policy generation; changed filters revoke pending claims through the existing
generation and profile/binding checks. Setup creates no execution, native run,
transfer or PR observation row. Push-only policy behavior remains unchanged.

Only authenticated App inbox deliveries capture source evidence. Support open,
non-draft, same-repository PR `opened`, `reopened`, `synchronize` and
`ready_for_review` events, plus `edited` when the base branch changed. Title/body
edits create no source. Pin the PR's exact head SHA and PR number; never run a
merge ref or fetch pipeline configuration from the PR. Repository IDs and names
must match both sides of the webhook. Existing logical-source deduplication is
unchanged: binding, kind, SHA, base branch and PR number identify the source.
Existing blocked receipts cannot become execution work after opt-in.

## Fresh PR authority

Before every PR admission, freshly verify account/installation/repository access
and call githubd's account-scoped `GetCIPullRequest` on the local control-plane
socket. This requires the GitHub App's `Pull requests:read` permission and
`pull_request` webhook subscription. The recovery service decorator forwards
this optional capability; older or unconfigured daemons fail closed. The remote
imaged listener continues to permit branch-head verification only. No CI
credential leaves githubd.

The typed result contains PR number, state, explicit draft flag, current head
SHA, base branch and both repository IDs/names. The HTTP reader bounds the
response to 256 KiB and JSON nesting to 32, rejecting ambiguous/case-alias keys,
trailing JSON, missing identity and missing/null draft state. All limits are in
`pkg/api/limits.go`. HTTP 404 and provider failures are availability errors;
they do not establish a closed PR. Raw responses, URLs and credentials never
enter execution snapshots, event errors or worker logs.

Closed/draft PRs and changed repository/target identity settle only the candidate
as `pr_closed`, `pr_draft` or `pr_changed`. A stale head settles as `head_changed`
with the observed replacement SHA and retained source ID when available. These
decisions cannot cancel other executions. Provider or incomplete-identity errors
retain the existing bounded admission retries and explicit recovery path.
Recovery preserves the source and checks current PR state again in the worker.

## Ordering and one execution per PR

PRs always require current-head verification, independently of the optional
push `latest_only` setting. Add an operational observation journal keyed by
account, binding and PR number. The 256 retained sources/account also bound this
journal; create a row only for a valid claimed source. Under the account lock,
advance its fence before the provider call. Hold no SQL transaction during that
call. New captured PR sources, including metadata-only capture, invalidate older
observations. An authenticated retarget to an unlisted base also invalidates an
existing observation without creating a source or journal row. Duplicate retained
sources and other PRs/pushes do not invalidate a current PR observation.

Admission rechecks the observation fence, claim, current policy generation,
binding, saved profile and materialized images under the account lock. A stale
observation defers without consuming the admission failure budget. GitHub and
PostgreSQL cannot commit atomically: the read establishes observed current state,
not a guarantee that GitHub cannot change immediately afterward. Closed/draft
webhooks remain ignored; this change introduces no background PR polling or
automatic cancellation solely because an already-admitted PR later closes.

At most one automatic execution holds a PR slot. This slot is independent of
the target branch, so a retarget cannot evade it. PRs, pushes, bindings and manual
executions retain separate scopes under normal account/global quotas. Add an
explicit push-kind predicate to existing branch queries to prevent PR sources
on the same base branch from sharing push concurrency or supersession.

A verified current candidate supersedes unadmitted sources for its PR and policy
generation. It requests normal cancellation of admitted automatic executions
from that generation, using existing execution-version and runtime capability
fences. Manual and prior-generation executions are never cancelled by this
policy. Prior-generation executions still hold the slot until they finish or
an operator cancels them explicitly. Cancellation commits before replacement
admission. Wait for terminal CI state, stopped native instances, first-attempt
Jobs receipts, collected output and no live runtime transfers. Retained artifacts
do not hold the slot. No scheduler/VM owner, task retry or command replay is added.

Supersession is atomic with cancellation and queue updates. Existing permanent
deduplication and retry-generation limits remain in force, including after
archival. Reopening or force-pushing an already settled exact logical source
does not revive it; a deliberate fresh manual submission remains available.

## API, CLI and rollout

Existing guarded automation PUT accepts `pull_request_branches`.
`gregalectl ci automation put --binding UUID --profile UUID --pr-branches main`
records PR-only intent; both branch flags may be supplied. Event history/status
includes `kind` and `pr_number`; execution/archive automation provenance retains
the PR identity. These diagnostics add no provider calls or public routes.
Existing GitHub Checks, JUnit collection, cancellation, archival and health
accounting are reused. Check conclusions remain determined by native results.

All production flags and generated defaults remain off; release stays paused.
Deploy migration, githubd and apid readers before opting into PR filters. Keep
admission off during binary rollback: older readers cannot interpret PR policy,
source or fencing evidence. Rollback refuses nonempty PR configuration, source
or observation journals. Clone inventory classifies filters as configuration
and PR observation authority as operational state that cannot be replayed into
a cloned environment.

Portable tests use real migrated PostgreSQL and fake provider/native/storage
boundaries. Cover signed ingress, source deduplication, policy ownership,
current-state checks, reordered captures, stale observations, retargeting,
independent slots, cancellation and native/transfer drain, recovery, restart,
RPC/decorator wiring, API/CLI, HTTPS guards, migration replay and dormant defaults.
Native KVM and real GitHub/storage verification remain separate acceptance.
