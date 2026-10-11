# ADR-738: Serving and configuration baselines for runtime upgrades

Status: accepted · 2026-10-05

## Context

ADR-737 retains the exact runtime and source chosen before building. Preparing
an update also needs a stable serving predecessor and configuration evidence.
Otherwise a concurrent deployment, environment edit or secret rotation can
change what the update replaces or what its retained source will receive.

## Decision

Add an internal apid-owned baseline capture after target pinning and before
queue admission. Require a fresh pending candidate with explicit zero traffic,
the same source archive, build root, handler and runtime family as one known
serving artifact in the same environment. Refuse ambiguous traffic and active
canary rollouts. Named sidecars remain outside this contract.

Use the existing atomic runtime configuration and sealed-secret snapshots.
PostgreSQL reads both deployments in one repeatable-read transaction and locks
the app then candidate in queue-admission order. MemStore uses its mutex.
Retain the serving deployment, physical layer key, runtime release and
configuration/secret fingerprints, plus separate candidate input fingerprints.
The existing secret fingerprint covers customer and delivery versions, row
incarnation, ownership, managed credential generation and selection grants.
Environment values, settings and deployment overrides participate in the
configuration fingerprint. Only digests and identities are stored; secret
values and sealed envelopes are not copied into upgrade records.

Database constraints and an immutability trigger forbid replacing or deleting
a baseline while its candidate exists. Deleting the candidate cleans up its
baseline. The serving deployment is retained by a deferred foreign key; a
whole-app deletion can still remove both deployments. Ordinary failed-build
retries copy the original baseline and capture timestamp in the retry
transaction. Retrying must never recapture newer values as reviewed input.

Register runtime catalogues as platform configuration and artifact bindings,
upgrade targets/baselines and health observations as operational records in
the environment-clone schema inventory. This also accounts for the earlier
runtime provenance column. Clones must not inherit upgrade review state, and
new metadata tables must not make otherwise known application schemas unknown.

Builderd and imaged validate captured baselines before selecting or preparing
a runtime. Missing or changed inputs and database failures block preparation.
Ordinary deployments without a capture keep their existing behavior. A real
absence is distinct from a failure reading the inputs of an existing capture.

Candidate build outputs, physical layer identities, inferred image profiles
and discovered secret-reload signals do not invalidate retained source intent.
They still require target binding and fresh native readiness qualification.
Serving artifact identity and configuration remain fully fenced. Runtime
secret delivery observations are not configuration and do not invalidate a
baseline; rewriting, rotating, resealing or revoking a secret does.

Validation is point-in-time preparation evidence. It is not a lock spanning a
build, a qualification record or an activation receipt. The eventual cutover
must repeat baseline checks in its authoritative write transaction, together
with target qualification and fresh candidate cold-boot/readiness evidence.
No public apply route, scheduler mutation or traffic activation is added.
Read-only previews continue to report execution unavailable.

## Consequences

An update attempt retains the serving artifact and reviewed input identities
through failures and retries. Changes require a fresh reviewed attempt. These
records do not freeze customer configuration, retain historical secret values,
or authorize restoring revoked credentials. Rollback must use the retained
artifact and respect the current secret lifecycle and guarded rollout policy.

The baseline uses existing 64-character revision digests and the existing
1,024-byte runtime artifact-key bound. No new quota is introduced.

## Validation

Memory and real PostgreSQL tests cover idempotent capture, queue races, retry
retention, legitimate image outputs, environment/resource edits, same-envelope
secret rewrites, revocation, changed serving artifacts and serving replacement.
SQL tests cover immutable baselines, changed candidate overrides, predecessor
retention and candidate cleanup. Builder/image tests refuse stale evidence and
database failures without fallback. The migrated-schema clone coverage gate
checks the complete registered inventory and its fail-closed drift behavior.
Native runtime qualification, test-metal
and leakcheck remain pending on the dedicated native KVM host; the configured
acceptance project is suspended.
