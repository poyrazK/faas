# ADR-20261104201200000: Bind qualification to shared-secret revisions and retain stage origins

- Status: Accepted
- Date: 2026-11-04
- Amends: ADR-1139, ADR-20261019120041, ADR-20261104110000

## Context

Qualification stored the current shared-secret fingerprint after clients finished
their probes. A concurrent rotation could therefore be recorded as tested.
Deleting and recreating a key also restarted its per-key version, allowing an
older qualification to match a different credential. Stage-only membership
depended on a remaining desired head, so deleting the last stage could silently
add its shared app identity to production. Deployment readiness used global
membership and ignored inherited project configuration and secrets.

## Decision

Each environment retains a monotonic `shared_secret_generation`, including when
its secret set becomes empty. Database triggers advance it on insert, delete,
and changes to a value fingerprint or secret version. Master-key reseals leave
it unchanged. Triggers cover N-1 writers as well as the new control plane;
MemStore advances the equivalent counter under its mutex. Secret deletion takes
the same environment lock as secret writes and promotion publication.
Existing shared secrets are recognized by an existence check, so this requires
no bulk update. The generation constraint is added without validating existing
rows, then validated in a later migration that permits concurrent writes.

Environment state exposes an optional `project_secrets_revision_hash` derived
only from this generation. The CLI reads it before probes and returns it under
`project:secrets` in its existing revision map. Qualification compares this
observed identity under the environment lock before accepting the result.
Preview and final promotion publication check the same persistent identity.
Adding then removing a secret during probes remains a conflict, as does
deleting and recreating a key with the same per-key version.

Older clients can qualify an environment that has never held shared secrets.
Once the set has changed, qualification requires the observed shared revision,
even if every key has since been removed. Existing qualifications with shared
secrets must be refreshed after this deployment. Environments that never held
shared secrets retain the existing stored receipt shape. The new response field
is optional; a new CLI can continue using the old protocol with an N-1 server.

`project_app_stage_admissions` records a shared app identity's stage origin and
outlives environment deletion. Existing stage heads are backfilled; a trigger
records future heads, including heads written by N-1 code. Production admission
through a production head, deployment, or accepted source retains precedence.
The ledger is project identity metadata: it is neither copied as environment
configuration nor removed with a stage. Legacy production membership is kept.
Checked graph authorization uses the same rule and ignores retired stage
deployments when an environment slug is recreated.

Atomic readiness filters production membership before comparing source apps,
matching the deployment planner. Runtime-key readiness includes inherited
project variables and shared secrets, preserves project values when replacing
app overrides, and excludes shared secrets for an explicit secret selection.
Reports retain keys and opaque state hashes without exposing project values.

## Validation and rollout

Regressions exercise shared-secret addition, rotation, deletion, recreation,
fresh and stale probe evidence, legacy clients, and master-key resealing on
MemStore and PostgreSQL. Lifecycle tests delete a stage with no deployment
history and verify that its service remains outside production until explicitly
admitted. Readiness tests cover unrelated stages, inherited values with and
without an active graph, replacement of app overrides, and explicit grants.

Deploy data migrations before the control plane, then update CLI clients that
qualify environments with shared secrets. No guest or node protocol changes are
required. Migrations are additive; down migrations restore the previous graph
membership helper and remove only the new revision and origin metadata.
