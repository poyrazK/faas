# ADR-1522: Recoverable operator restart execution

Date: 2026-10-17
Status: Accepted; native lifecycle qualification pending

## Context

An operator restart invalidates snapshots, destroys a VM and writes STOPPED
before its command is completed. A scheduler can die between any two steps.
Reclaiming a five-minute-old command does not establish whether its VM effect
ran. The previous direct restart path also swallowed errors writing STOPPED.

## Decision

Apply this protocol to `force_restart` operator intents:

1. Every claim gets a new UUID. Reclaim clears it. Restart preparation,
   vmmd dispatch and completion check the current running claim. Legacy Mark
   succeeded/failed setters reject restarts.
2. Schedd records an immutable execution plan: instance, app, deployment, node,
   wake ID, migration history/lease identity, vmmd physical generation, and the
   exact warm/init snapshots selected.
   Snapshot invalidation and plan insertion commit together. A later attempt
   uses the original plan and never selects newer snapshots.
3. Vmmd validates its local node identity, the claim and the original placement,
   then commits `started` with an effect token before teardown. Only that
   prepared-to-started transition authorizes execution. Claim changes cannot
   authorize a second destroy for the same intent.
4. Manager reserves teardown against the exact live incarnation. It refuses
   replacement and unknown instances. The native generation comes from the
   existing native launch identity; the fallback generation lasts for that
   Manager incarnation. No ordinary Destroy fallback is used.
5. Only confirmed process/resource teardown authorizes vmmd's durable receipt.
   Vmmd retries receipt persistence without invoking teardown again. The effect
   token remains valid across scheduler claim changes so late physical evidence
   can still be recorded.
6. Schedd reads durable evidence after the RPC, including failed/lost replies.
   A receipt permits one transaction to commit STOPPED with `terminal_at`, the
   operator outcome, exact snapshot IDs, timestamp, host-port lease release,
   completion receipt and audit outbox handoff. Completion retries return the original handoff. Admission is
   released after this transaction succeeds. State write failures propagate.
7. Started work without a receipt becomes `outcome_unknown`; it is never
   redispatched automatically. A late receipt makes it eligible for database-only
   reconciliation if the original instance/node/wake still match and the row is
   RUNNING or STOPPED. A changed/missing target stays unresolved. A stopped VM by
   itself never establishes that this operator action succeeded.

The completion receipt is the original outbox ID retained on the intent; it has
no foreign key to the prunable delivery queue. Outcomes deduplicate per intent
and status, allowing an uncertain outcome and its later success to remain in
audit history. Replaying a terminal completion cannot recreate a pruned handoff.

GET `/v1/admin/operator-intents/{id}` exposes execution phase, original node/wake,
physical generation, selected snapshots and teardown acknowledgement time. It
does not expose claim, destroy or migration lease tokens. Migration credentials
are also redacted from the durable audit payload.

## Recovery and rollout

See the [operator restart recovery guide](../ops/operator-restart-recovery.md).
Apply the migration with operator dispatch quiesced, upgrade vmmd first, then
replace all schedulers together. Pending legacy intents can be claimed; running
legacy restarts are quarantined as `outcome_unknown` without fabricated receipts.
Old schedulers bypass the protocol, so mixed scheduler writers are unsupported.
Downgrade refuses to discard execution evidence or unresolved legacy actions.

The journal intentionally preserves uncertainty if vmmd dies after starting an
effect and before acknowledging it. This provides at most one authorized
teardown dispatch per intent, not a distributed exactly-once VM execution claim.
It favors an unresolved action over repeating a destructive effect. Operators
must inspect native recovery/resource evidence before issuing a new action.

This decision covers the asynchronous restart intent path. Force-park, cold-boot
policy changes, node actions and the legacy unary schedd ForceRestart RPC do not
gain this protocol. The unary restart does gain terminal-write error propagation.
It must not be used as a retry/recovery interface.

## Validation

Portable checks cover memory/PostgreSQL journal replay and concurrency, stale
claims, lost acknowledgements, replacement targets, late receipts, atomic failure
rollback and migration quarantine. Scheduler tests use the actual dispatcher;
bufconn tests exercise the vmmd protocol across a fresh server instance. Manager
tests bind teardown to a physical generation. Native acceptance additionally
requires `TestMetalOperatorRestartGeneration`, the existing teardown/native
recovery suites, `make test-metal` and `make leakcheck` on supported native KVM.
