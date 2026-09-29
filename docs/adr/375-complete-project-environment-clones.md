# ADR-375 · Complete project-environment clones and qualified promotion

- **Status:** proposed
- **Date:** 2026-09-29
- **Decision:** A complete clone captures one immutable, effective project-environment revision, recreates every workload and its selected artifact in a new environment, and prepares isolated point-in-time managed data. A qualified promotion carries the tested artifact and logical configuration revision to its target while preserving the target's live data and target-specific physical identities.
- **Why:** `env create --from` currently copies scoped values and selected policies but does not create live deployments. Many app controls remain on the shared `apps` row. Promotion copies artifacts and, optionally, the project config JSON; it does not carry the rest of the tested effective state. A successful clone or promotion must never imply parity that the platform cannot prove.
- **Consequences:** A clone and a promotion become durable, resumable operations with complete resource inventories, hashes, readiness gates, explicit substitutions, and rollback receipts. Existing partial clone and release-only promotion remain available during migration; the complete mode fails closed until every resource in its inventory has an implemented strategy.
- **Rejected alternatives:** Copying mutable database rows without a snapshot can mix versions. Reusing production database or bucket identities makes staging writes affect production. Copying staging data back to production can discard production writes. Treating application-wide settings as equal across environments can make a staging edit change production before promotion.

## Contract

`gregale env create staging --from production --full --wait` creates a usable,
independent staging environment from one source revision. Its receipt includes
the source revision, every copied or remapped resource, data capture points,
and a completeness result. The command succeeds only when the stage is ready.
An unsupported or uncloneable resource is a named blocker, never a silent skip.

`gregale projects environments promote PROJECT --from staging --to production
--full --wait` previews and applies the exact qualified logical revision. It
rechecks source and target generations and the qualification receipt before
preparing target resources or moving traffic. The result records each applied,
preserved, or substituted setting and the previous production revision for
rollback. A post-cutover verification failure triggers the fenced rollback.

The operation is project-wide: one selected artifact and one effective
configuration per workload, plus project-level routing and dependencies. New
mutable customer-facing settings must declare clone, diff, qualification,
promotion, and rollback behavior in a coverage registry. CI rejects a new
setting with no policy. The effective-state API and the clone receipt expose
the same inventory, so a resource cannot be omitted from both by accident.

## Environment ownership

Application identity remains stable across environments. Mutable runtime
behavior is resolved from a versioned `(project_environment_id, app_id)`
workload specification before build, wake, scheduling, and routing. Existing
unscoped app writes resolve to the production environment for compatibility.
The selected effective specification includes compute and scaling, runtime and
health, ingress and egress, routes and edge actions, sidecars, triggers,
bindings, alerts, environment variables, and customer secrets. It records
which values are inherited or explicitly owned; a complete clone materializes
inherited values so later source edits cannot alter the target.

The current non-secret project config JSON is a versioned plan identity. It
must be connected to runtime behavior through typed settings or named
environment variables before a change to it counts as a tested runtime change.
The revision hash covers the canonical effective settings and immutable
artifact identities. Secret values never appear in state/diff/receipt output;
the hash uses secret revision identities and keyed fingerprints.

## Clone and data capture

Clone starts from a database snapshot of the source environment revision and
active release set. Target deployment rows reuse immutable source artifacts
without rebuilding. The target release set is published only after all target
workloads are ready and every resource clone has been verified. Until then its
stable environment URLs return not-ready rather than falling through to the
source or ordinary application route.

Managed PostgreSQL is restored into a new database at a recorded recovery
point. Object storage is copied into a new bucket from a durable manifest of
source object versions/generations, with size and checksum verification. A
live `ListObjects` plus copy-by-key loop does not meet the point-in-time
contract. The provider must support version-addressed reads/copies or the
clone fails before it is advertised as complete. Cross-resource consistency
requires a source write barrier or application checkpoint while the PostgreSQL
recovery point and object manifest are captured. The barrier can be released
after immutable capture identities are secured; bulk copying is asynchronous.

Customer secrets are resealed for the target scope. Managed credentials are
new and refer only to the isolated target resources. External integrations
and unique hostnames use explicit logical-to-physical mappings. A staging URL
is generated automatically; a production custom domain is never claimed by
both environments. Missing mappings are blockers. Stage data is test data and
is never copied back during promotion.

## Qualification, promotion, and rollback

Qualification runs health and smoke checks against every member of one active
release set and records the complete effective-revision hash, data-clone
receipt, and target mapping requirements. Any relevant edit invalidates it.
For each logical binding, promotion either preserves an existing production
resource or prepares a compatible production resource; it never imports the
staging database or bucket ID. Customer secrets have explicit `promote` or
`target-preserve` policy, and generated credentials always resolve at the
target. The preview shows these decisions without revealing values.

Production schema changes use a compatible migration step before cutover.
The platform fences mutations with compare-and-swap on both environment
generations. It prepares target-scoped deployments and configuration, checks
readiness, then atomically switches the release graph and effective-config
pointer. Rollback restores both pointers together and leaves production data
at its current state. Operations are idempotent and retain a durable status
and compensation ledger for retries and cleanup.

## Acceptance

The production-shaped acceptance path must: clone a multi-workload project
with PostgreSQL and object storage while production writes continue; prove the
captured data point and isolation; change one setting of each supported kind
in staging; prove production is unchanged; qualify the exact staging revision;
promote in one command; prove the intended settings and artifacts changed in
production while production data was preserved; and roll back code and config.
Mutation races, failed resource preparation, stale qualification, unsupported
providers, and cleanup retries must fail closed. The native x86_64 KVM lane
must exercise actual deployment, wake, and route behavior.

## Implementation progress

The complete command contract above is not available yet. Implemented building
blocks are durable fenced clone operations, version-pinned S3/GCS manifests,
verified resumable object copies, and immutable environment workload settings.
The workload field registry assigns every `App` field a copy, remap, identity,
or operational disposition; it does not yet cover every related resource table.

The existing environment clone transaction now materializes independent desired
workload settings. New deployments pin their settings in the deployment create
transaction. Build/image processing, explicit scheduler admission, prime,
migration, park, task execution, environment URLs, deployment previews and
aliases resolve the pinned configuration. Declared-route edits advance the
desired revision, and the gateway uses the route revision pinned to a deployment.
Legacy deployments without a pin retain the pre-existing App behavior.

`GET/PATCH /v1/apps/{slug}?environment=staging` and the CLI's
`app [scale] --environment staging` support desired configuration edits.
Revision headers fence concurrent CLI edits, and direct app settings edits to
protected environments are rejected. Ordinary production App settings remain
the legacy authority until complete promotion supports their atomic cutover.

Remaining work includes a complete source inventory and consistent capture
barrier, connecting the PostgreSQL and object copy workers to one durable clone,
cloning and publishing the active deployment graph, environment ownership of
the remaining policies/triggers/integrations, scope-specific capacity and
reconciliation, source-manifest/reconcile writes, and qualification plus atomic
promotion/rollback of the complete effective state. Production ingress must
also exclude other scopes from ordinary weighted routing. Native x86_64 KVM
acceptance remains required; the current local checks use store integration
tests and scheduler/gateway fakes on macOS.
