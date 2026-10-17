# ADR-1584: Explicit internal CI execution reruns

- Status: implemented behind existing default-off CI gates; native qualification pending
- Date: 2026-10-17
- Extends: ADR-1573, ADR-1339, ADR-1340, ADR-1578 and ADR-1583

## Decision

Add an explicit whole-pipeline rerun operation for retained Jobs executions.
Each accepted request creates a fresh manual execution, pinned to the parent's
commit, immutable revision, complete plan, profile when present, binding
generation and materialized template identities. All jobs start waiting with
no attempt or result. The normal dispatcher creates fresh attempt, source,
output, artifact and JUnit identities. Each Jobs task still has one attempt and
zero retries. Controller recovery continues the existing execution; it does
not invoke rerun or repeat user commands.

Store `rerun_parent_id` and `rerun_root_id` in the existing execution ledger,
with matching `rerun.parent_execution_id` and `rerun.root_execution_id` in its
snapshot. The first child points to its parent as root; subsequent reruns keep
that root and point to their immediate parent. Account-scoped foreign keys,
non-self checks and snapshot checks prevent foreign or incomplete lineage and
make older writers fail closed when they would discard it. Original execution
results are unchanged. No additional queue, native owner or retry worker is
introduced.

## Fresh admission and cleanup

Fresh reruns require the existing internal allowlist, GitHub and dispatch gates.
The HTTP route additionally requires configured storage/HTTPS readiness, worker
and transport enablement, account authentication, admin scope and MFA. Production
defaults remain off. Native and real-provider qualification remain separate.

Read the owned parent's frozen configuration before fresh repository access
verification, holding no SQL transaction during the provider call. Resolve its
active immutable profile when present. Under the normal account lock, recheck
account eligibility, current binding, profile, revision and template images.
Lock and reread the parent before insertion; a concurrent archive, disable,
binding change or rematerialization cannot change the source of a rerun.
Provider failures use stable availability/access errors and expose no raw URLs,
responses or credentials.

The parent must be terminal with a finish receipt and no execution lease.
Reuse the branch/PR admission proof for stopped native instances, first-attempt
task receipts, collected native output and no live runtime transfers. The
account/execution lock order also fences runtime grants and late starts.
Payload retirement and remote Check settlement are not required: retained
artifacts and pending publication belong to the original execution and remain
independent of the child. New execution, lineage and optional Check intent
commit atomically under normal global/account/detailed-history quotas.

Reruns are explicit manual work, including when the parent was automatic. They
do not copy automation admission authority or create/promote webhook sources.
Branch/PR automation does not cancel manual reruns or share its automatic slot
with them. A rerun pins the original commit even if a PR head has moved; current
repository access and frozen configuration must still be valid. Check capture
uses the current publication gates and the new execution's remote identity.

## Idempotency and archival

Use the existing account-scoped execution request-key namespace. A repeated key
for the same parent returns its existing execution without provider calls,
additional admission, audit emission or native work. A different parent or an
ordinary submission cannot reuse the key. The account lock serializes races;
lost responses and restarts preserve the same identity. Different explicit keys
admit distinct executions within normal quotas; there is no automatic rerun
policy or replenished attempt budget.

Receipt replay precedes fresh configuration checks and capacity accounting.
It remains available when the parent was archived or its profile changed, while
the route's admission gates and authentication still apply. An archived child
returns the existing `ci_execution_archived` response; its request key remains
reserved. Fresh reruns of archived parents also return that response because
their private configuration was deliberately compacted. Operators can use the
existing fresh profile submission workflow for a deliberate new execution.

Detailed status, live history and archive receipts retain the parent/root IDs.
Archive compaction keeps relational lineage and its minimal snapshot projection,
without restoring commands, output or private image keys. Existing bounds of
32 detailed Jobs executions and 128 archives/account also bound retained rerun
identities. Lineage stays operational clone inventory and cannot grant execution
authority in another environment. Rollback refuses retained rerun evidence,
including archived lineage. Deploy all readers before use and keep admission
off during binary rollback.

## API, CLI and verification

`POST /v1/internal/ci/executions/{id}/rerun` accepts an empty JSON object and
requires `Idempotency-Key`. Configuration, job and attempt overrides are refused.
It returns 202 with the new execution or `Idempotency-Replayed: true` for an
existing receipt. `ci_rerun_not_ready` (409) identifies unfinished execution or
cleanup; other existing ownership, authority, conflict and quota errors apply.

`gregalectl ci execution rerun --id EXECUTION_UUID --key STABLE_RERUN_KEY`
uses that guarded route. Existing status/list/archive commands expose lineage;
separate logs, artifacts and reports use the child's new execution ID.

Portable tests use real migrated PostgreSQL and synthetic provider, native and
storage boundaries. Cover complete pipeline reset, result/JUnit/Check isolation,
concurrent keys, restart, archival and replay, account isolation, failed atomic
insertion, profile/binding/image changes during lookup, provider redaction,
native/output/transfer cleanup, quotas, manual PR reruns, API/CLI and HTTPS
guards. No native VM or real provider action is performed by these tests.
