# ADR-626: Durable regional serving resume intents

Date: 2026-10-05
Status: Journal and warm scheduler integration implemented; regional activation pending.

## Context

ADR-624 requires an explicit fresh serving seat when an existing resident resumes.
A scheduler can lose the issuance response or the native resume response. A second
scheduler can select the same paused resident. Minting another authorization on
either retry loses the identity needed to recover an uncertain native operation.

## Decision

Retain an immutable resume intent in the regional PostgreSQL journal. Its ID
identifies one serving authorization; its seat ID is derived from the original
physical permit ID and the intent ID. Mutable claims do not enter that derivation.
Keep the full original physical permit, including host epoch, run, deployment,
purpose and grant. Only issued, held, unfenced warm or serving permits qualify.

Atomically reserve and issue the fresh seat and record its intent under the same
read committed app quota lock used by grant retirement, physical permits and seat
fences. No issuance failure leaves a new seat or changes the physical receipt.
Competing proposals with the same physical claims and rollout classification
recover the existing held resume intent, including its original timestamps.
Callers dispatch the returned intent. The proposed ID is never a dispatch identity
until the transaction returns its retained receipt.

An exact old-ID retry checks that old authorization; it cannot substitute a later
one or reopen a fence. A new proposal cannot replace a held or fenced resume seat.
Only the existing authenticated stop receiver can release an issued seat. After
that release, a different intent may issue a fresh seat on the same still-held
physical permit. Release of serving capacity never releases physical capacity.
RPC errors, timeouts, scheduler restart and native acknowledgement do not close
an intent or release either hold. Read-only recovery retains records after app,
instance and node collection. SQL rejects record changes, deletion, truncation,
fixed-snapshot issuance and nonempty downgrade.

The Store interface requires resume persistence through decorators. The journal
is bookkeeping only. All accepted quota heads remain frozen. Warm-promotion acceptance,
migration rollback, signed compute admission, native pause/stop verification,
request forwarding, task attempts, billing, provisioning and ingress remain
necessary before activation. Mock stop assertions do not establish native proof.
Dedicated native x86_64 Linux KVM acceptance remains required by CLAUDE.md.

## Validation

Exercise the same contract on MemStore and PostgreSQL: concurrent proposals,
exact retries, lost-response recovery, immutable identities, capacity rollback,
held and fenced old seats, authenticated release and fresh intent, physical and
grant fences, cancellation, store restart, collection and SQL guards. Verify
retirement/fence lock waits with both commit and rollback. Regenerate schema and
SQLC, run focused race checks and repository policy gates.

Full runtimequota and state race suites passed, followed by 309 regional cases
with private PostgreSQL enabled and a byte-identical canonical schema check.
Linux race suites for runtimequota, state, builderd and imaged passed on the
shared internal GCE node. All 304 product packages built, and diff lint and
repository policy checks passed. The PR-only migration-version hygiene check
was skipped in local context. Source manifests and the initial local disk-space
build failure are retained with the validation evidence. These checks do not
provide dedicated native KVM lifecycle or recovery acceptance.
