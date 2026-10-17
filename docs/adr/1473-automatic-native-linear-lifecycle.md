# ADR-1473: Automatic native Linear lifecycle sync

Date: 2026-10-14

Status: Accepted

## Context

Explicit native Linear pushes have connection/group guards and verified workflow
selection. Automatic GitHub delegation already has durable claims and outcomes.
Linear delegation additionally needs explicit workflow destinations and recovery
that recognizes an exact state UUID rather than only its mapped lifecycle.

## Decision

Extend the existing GET/PUT tracker `auto-sync` policy with optional
`expected_linear_status`, `linear_open_state_id` and `linear_resolved_state_id`.
Enabling Linear requires the native snapshot, policy revision and aligned Gregale
lifecycle. Reject ambiguous GitHub/Linear snapshots. Verify both distinct nonzero
workflow UUIDs are active, belong to the connected team and map to open/resolved,
without changing the provider. Keep existing owner, entitlement, deploy-write,
session MFA and policy revision checks.

Persist mappings alongside the baseline in the existing claim table. Pin Linear's
connection revision as well as its owner/workspace/team/viewer and group scope.
Credential replacement requires explicit re-enabling, even in the same workspace.
Preserve existing GitHub policy fingerprints and native creation receipt hashes.

Dispatch claimed Linear policies through the scoped native Linear host and push
service. Persist pending lifecycle before a provider write. Derive the pending
workflow UUID from the immutable mapping for that policy revision. A later read
matching the exact pending identity/state UUID confirms the write without another
mutation. If the provider still matches the original baseline, retain the pending
intent and keep observing without redispatching. Any other provider edit pauses
as conflict, including a different workflow with the same mapped lifecycle.

Reuse policy inspection, offline disable, claim fencing and bounded polling.
Conflicts/blocked outcomes require an aligned explicit opt-in to resume. Ignored
states, revoked credentials, owner/entitlement loss and grouping changes cannot
silently push. Extend the CLI with `--provider linear`, `--linear-open-state` and
`--linear-resolved-state`; no local provider key is needed.

## Limits

This is outbound delegation only: no ticket creation or automatic inbound
resolution. Pending outcomes may remain unconfirmed indefinitely; operators must
inspect, explicitly align and re-enable if needed. Provider operations and Gregale
do not share an atomic transaction. An already-dispatched mutation cannot be
recalled by disable or disconnect, and another writer may race the provider's
final snapshot check. Polling is bounded by the existing shared worker limits.

## Validation

Mocked domain race tests cover selected destinations, exact uncertain-write
recovery, no redispatch on unconfirmed outcomes, manual workflow edits, revision
and group changes, ignored states and disable during credential access.
PostgreSQL API acceptance covers persisted mappings, stale-policy rejection,
reporter denial, failed workflow verification, uncertain-write recovery,
connection replacement and disabling after disconnect. CLI and SDK contracts
cover native scope, mapping fields and policy revisions. No live Linear writes
are performed by these tests.
