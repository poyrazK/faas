# ADR-1581: Internal CI branch serialization and verified-head supersession

- Status: implemented behind existing default-off CI gates; native qualification pending
- Date: 2026-10-17
- Extends: ADR-1341 and ADR-1580

## Decision

Serialize automatic executions per account, binding UUID and exact branch.
The limit is one in `pkg/api/limits.go`. Manual executions and independent
branches/bindings remain independent, subject to normal global/account quotas.
Waiting for an automatic branch slot does not consume the eight-attempt
provider/admission failure budget. It clears the claim and polls again after
the existing two-second interval. Previous genuine failures remain charged.

An explicit policy `latest_only` boolean defaults false. Opt-in advances the
policy generation; identical saves preserve it. New authenticated push capture
freezes this choice with the existing policy/profile/source snapshot. Policy
setup creates no execution, branch claim, transfer or native work.

## Authority and ordering

For latest-only admission, apid freshly verifies repository authority and uses
githubd's existing account/installation-scoped `GetBranchHead` RPC. It never
infers a newer commit from delivery order, receipt timestamps, commit timestamps
or SHA ordering. Missing/invalid/provider responses fail closed without cancelling
existing work. A successfully missing branch settles only its candidate as
ignored with `branch_deleted`; it does not cancel an existing execution.

A branch observation row is created only for a valid claimed retained source.
The existing 256 retained source events/account also bound these rows to 256.
Under the account lock, each observation advances its branch fence before the
RPC, with no SQL transaction held during the call. Capturing a distinct source
on that branch, including metadata capture, invalidates an existing fence in
the capture transaction; a
duplicate delivery does not. Admission rechecks the fence, source claim, policy
generation, binding, active profile and materialized images under the account
lock. A newer capture or observation makes an older response defer harmlessly.

GitHub head and PostgreSQL cannot share one transaction. The RPC establishes an
observed head, not a guarantee that the remote branch cannot move immediately
afterward. Later authenticated captures invalidate in-flight observations and
the following pass resolves again. Each execution still pins its exact SHA.

## Supersession and native stop

A non-head candidate becomes permanently ignored with the observed replacement
SHA/time and, when captured, its same-policy source-event UUID. A verified-head
candidate supersedes other unadmitted pending/claimed/dead events of the same
policy generation and branch. This fences stale claims and prevents explicit
admission recovery or webhook redelivery from reviving them. Existing logical
source deduplication remains permanent, including a force push back to an
already superseded SHA; a fresh explicit manual submission remains possible.

For admitted automatic work from the same generation, apid records supersession
and invokes the existing cancellation mutation, version invalidation and
source-capability revocation in the same transaction. Guest cancellation
polling and bounded output finalization remain available. Cancellation intent
commits before the replacement is considered on another pass. Manual runs,
other branches/bindings and admitted prior-generation work are never cancelled
by this policy. Prior-generation automatic executions still hold the branch
slot until they finish or an operator cancels them explicitly.

The replacement waits for terminal execution state, terminal first-attempt Jobs
receipts, confirmed stopped native instances, collected output where native work
existed, and no live runtime transfer. Retained artifacts do not hold the slot.
Archived executions already passed the stricter archival cleanup checks.
No task retry, VM launch/stop, instance mutation or new compute owner is added.
Existing Jobs runtime/start fences reject late source transfers after cancel.
Atomic rollback preserves the previous work if cancellation or audit updates
fail. Cancellation updates continue to dirty the existing execution Check
outbox and use its normal cancellation result publication.

## API, CLI and operations

Existing automation PUT accepts `latest_only`, and list responses report it.
`gregalectl ci automation put --latest-only` opts in. Omitting the flag selects
false; existing policies remain false after migration. Existing admin/MFA/HTTPS,
allowlist and GitHub setup checks apply. All production CI gates remain off.
Event status/history adds `superseded_by`, `superseded_sha`, `superseded_at` and
stable `branch_wait`, `head_changed`, `branch_deleted` classifications, with no
lease token, head fence, command or provider response exposed.

The additive replay-safe migration preserves the previous recovery journal.
Rollback refuses to remove active policy, branch fencing or supersession
evidence. Branch observations are operational clone inventory; policy choice
is configuration and captured event authority is operational.

Deploy the new admission readers together before opting in. Keep admission off
during binary rollback: older binaries do not enforce the new branch policy.

Portable PostgreSQL/race tests cover branch contention, retry budgets, manual
and branch isolation, reordered captures, stale observations, policy replacement,
provider failure, source recovery exclusion, atomic cancellation, native stop,
output drain, restart and deletion. HTTPS wiring checks actual branch-head RPC
use through a fake provider. Native KVM/provider qualification remains separate.
