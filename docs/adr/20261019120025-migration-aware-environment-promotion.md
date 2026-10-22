# ADR-20261019120025: Migration-aware environment promotion

## Status

Implemented behind default-off `FAAS_PROMOTION_MIGRATIONS_ENABLED=1` admission.
Native x86_64 KVM qualification remains required before enabling it. Existing
admitted operations continue when the admission switch is disabled.

## Context

Snapshot and service graph promotions reuse artifacts and destination runtime
configuration, but bypass imaged's build-to-release-task handoff. Promoting a
release with a database expansion therefore requires an explicit destination
migration step before destination HTTP health checks and cutover.

## Decision

Require `--sync-config --verify --migrate`, complete pinned HTTP graphs, and
exactly one immutable source `release_command`. The opt-in asserts backward
compatibility with the destination baseline. Bind workload identity, command
hash and compatibility to the captured release configuration, preview, approval
and promotion identity. Credentials remain destination-owned; public policy
contains only identities and hashes.

Add a durable `migrating` health phase before preflight. Reserve the project
migration lane atomically with promotion admission. After dark candidate
preparation, record one task identity and a destination runtime fingerprint in
`project_environment_promotion_migrations`, then insert its release task in the
same transaction. The journal follows the parent lifetime and cannot replace
the task or expected runtime inputs. Serialize competing release tasks,
expansions and pending contract tasks under the project admission lock.

The scheduler retains ownership of execution. Bind the exact runtime fingerprint
to the current restoring lease and require it again at the existing running
fence. A stale scheduler cannot dispatch without a matching attestation. Restore
recovery may reuse the same task before dispatch; running commands never replay.
A started command without a terminal exit is uncertain, blocks further migration
admission, and requires database inspection. Start within one minute of promotion
admission; execute within the existing ten-minute release task limit.

An owned successful zero exit unlocks normal preflight, which checks both the
baseline and the candidate graph against the expanded schema. Cutover repeats
migration identity, success and runtime-input checks inside existing project
publication locks. Normal pre/post health windows and rollback rules remain.
Schema expansion is never reversed by code/config rollback. Multiple owners,
workers, sidecars, destructive contract cleanup and inferred compatibility are
outside this admission mode.

## Consequences

Destination database expansions no longer require an out-of-band promotion
step. Durable task outcomes survive APID or scheduler restarts. Failed and
uncertain commands keep the previous release selected, but database changes may
already exist; operators must inspect those effects. The compatibility assertion
and application health endpoints cannot prove arbitrary SQL safety. Admission
stays off until the native acceptance procedure qualifies actual task execution,
credential isolation, process loss, probing and rollback.
