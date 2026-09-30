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
protected environments are rejected. Production App fields remain a compatibility
projection: workload configuration promotion updates that projection atomically,
and ordinary App edits advance an existing production desired head.

Remaining work includes a complete source inventory and consistent capture
barrier, connecting the PostgreSQL and object copy workers to one durable clone,
connecting captured deployments to the durable clone worker, environment ownership of
the remaining policies/triggers/integrations, scope-specific capacity and
reconciliation, source-manifest/reconcile writes, and qualification plus atomic
promotion/rollback of the complete effective state. Production ingress still
needs request-specific settings for retained release/revision selections. Native x86_64 KVM
acceptance remains required; the current local checks use store integration
tests and scheduler/gateway fakes on macOS.

### Qualification configuration fence (2026-09-29)

Qualification state and receipts now include per-workload runtime configuration
hashes. The CLI captures these before probing exact deployment previews. Receipt
creation rejects a desired revision that differs from the deployed pin, and
promotion publication rechecks the latest receipt, source graph, workload hashes,
project configuration, and secret revisions in the cutover transaction. Legacy
receipts without workload hashes are accepted only while the environment has no
owned workload settings. New clients fingerprint legacy settings as well.

The MemStore and real PostgreSQL contracts exercise caller mutation, missing
fingerprints, desired edits during probes, and a source edit after qualification
but before publication; the rejected publication leaves the target graph intact.
API/CLI qualification checks and OpenAPI compliance pass. These checks do not
establish full clone readiness or native VM lifecycle acceptance.

### Workload configuration promotion (2026-09-29)

Promotions with `sync_config` fingerprint the exact source deployment settings,
the target desired configuration, and the configuration after target placement
is preserved. These hashes participate in preview/approval identity; an unchanged
artifact with changed settings is an update. Preparation inserts an immutable
target revision and deployment pin without advancing its desired head. The prior
head and fallback settings are persisted, and an unpinned legacy target deployment
is frozen before the production App projection changes.

Release activation switches the prepared heads and production projection in the
same transaction as the graph and project configuration. Rollback restores the
captured head or legacy fallback and projection in its graph transaction. Both
operations compare the exact expected head and configuration hash. Ordinary App
updates synchronize an existing production head; deployment pins stay immutable.
Revisions are allocated from retained history, including prepared revisions, so
rollback to a lower head does not reuse revision numbers. Preparation, publication,
and rollback retries reuse durable identities. Target static egress and overflow
placement are retained; these operations do not write managed data bindings.

The focused MemStore and PostgreSQL contracts verify preparation isolation,
configuration projection, retained legacy releases, retries, revision allocation,
rollback, and refusal to overwrite concurrent production edits. The API covers a
configuration-only promotion and rollback using the same artifact, with protected
approval and qualification, plus reconstruction after cutover. The complete state
package binary exceeded local disk capacity during concurrent unrelated builds;
its existing stage test files were also run unchanged through a small temporary
harness against the real stores. This is focused evidence, not full-suite or native
VM acceptance. Remaining full-clone gaps listed above still apply.

### Production routing isolation (2026-09-29)

Ordinary ingress weights, quarantine checks, and companion routes now select
only named production or legacy default deployments. Serving legacy default
traffic survives dark preparation in named production; named production takes
precedence once it serves traffic. Stage deployments remain available in exact
deployment target sets, but cannot acquire implicit default weights on cache
hydration. An authoritative empty production set stays empty, and first ordinary
host lookup hydrates stored weights before it can route.

Exact deployment routes retain their own settings without replacing the app-ID
production cache. Their companion routes also work for zero-traffic candidates.
Focused gateway and daemon contracts cover stage-only apps, preparation and
cutover, out-of-band stage wakes, cache eviction and failed-read retry, quarantine,
sidecars, and interleaved production/preview settings. Request-specific retained
release settings and scope-specific capacity/reconciliation still require work.

### Production wake selection (2026-09-29)

Gateway scope filtering and scheduler default selection share one production
lane policy. An unscoped scheduler wake honors the active production graph;
without a graph it selects serving named production or legacy default traffic.
Stage-only and dark-only deployments cannot become production implicitly.
Running-instance reuse is limited to the selected deployment, including explicit
stage and retained-release wakes. Fast-path port and provenance come from that
instance's deployment, and its execution mode is checked against pinned settings.
Legacy migration fallback, production warm-pool snapshot selection, and cron's
missing-production check use the same resolver. Warm-pool restore reads pinned
settings for its selected deployment.

The MemStore and PostgreSQL selection contracts cover legacy preparation,
production cutover, stage-only apps, and a newer direct deployment after graph
publication. Scheduler fake lifecycle checks cover cross-scope running-instance
reuse, pinned stage execution mode, existing wake behavior, and warm pools.
Scope-specific admission ledgers, pool counts, reaper floors, and reconciliation
remain unfinished. These local checks do not replace native VM acceptance.

### Effective production values during cloning (2026-09-29)

Cloning production now selects variables, customer secrets, and managed bindings
from the namespace of the deployment actually serving each workload. An active
production graph takes precedence; otherwise named production traffic wins over
legacy default traffic. Undeployed workloads preserve explicitly configured
production values, falling back to default only when production has no values.
The two namespaces are never merged by key, and preview apps are excluded from
the logical project workload roster.

The API captures the workload/scope roster once and uses it for managed binding
preparation. Both stores recheck that roster before committing target state, so
a scope cutover or roster change rejects the clone. Focused MemStore and real
PostgreSQL contracts cover legacy/named/undeployed values, secrets, previews, and
cutover rejection. API contracts cover legacy managed bindings and binding-plan
scope reuse. The value fence below also rejects same-scope edits. Provider data
versions still require the durable source capture and barrier above.

### Shared database topology and retry ownership (2026-09-30)

Isolated binding preparation now restores each source database once per target
environment, preserving sharing across workloads while issuing distinct scoped
credentials for each binding. All database copies in a preparation use one
microsecond-precision recovery time. Retries adopt the recorded time from an
existing restore intent; incompatible provider identities, specs, or recovery
times reject preparation before further restore calls. One matching legacy
copy can be adopted. Multiple existing copies of a shared source are a blocker.

The restore service reports whether the caller reserved a new target, including
the reservation-race path. Compensation never acquires ownership of an adopted
copy. Environment cleanup recognizes both database-level and legacy app-level
names and revokes all bindings before deleting each owned database once.

Focused provider/service and API contracts cover shared topology, distinct
credentials, retry reuse, compensation ownership, mismatched captures, legacy
adoption, and cleanup without source mutation. A common recovery time does not
establish cross-provider application consistency; the source write barrier and
durable complete-clone orchestration remain required.

### Source value fingerprint and secret lifecycle (2026-09-30)

Clone preparation now captures the effective value-scope roster and a canonical
fingerprint of source variables, encrypted customer secrets, lifecycle classes,
secret revisions, and managed binding/credential identities. It returns only the
scopes and hash. Both stores compare that fingerprint before writing target
configuration; same-scope edits, deletion, rotation, or binding changes reject
the clone. Delivery observations and timestamps do not affect the fingerprint.
Other scopes and preview apps are excluded.

PostgreSQL capture and clone run at repeatable-read isolation, so the scope check,
fingerprint, quotas, and copied rows observe one database snapshot. Source row
lock serialization failures surface as conflicts. The copy retains ephemeral
secret classes as well as customer revision identities; a clone cannot silently
make an ephemeral secret eligible for persistent VM snapshots.

Focused MemStore and real PostgreSQL contracts cover stable captures, irrelevant
scope changes, variable edits/deletions, secret rotation, and lifecycle class
preservation. The API verifies rejection after source edits during preparation,
compensation of the new data copy/credentials, and preservation of an existing
stage. API environment/qualification/promotion and OpenAPI checks pass locally.
This is an optimistic preparation fence, not the complete environment revision
or durable resource capture. Deployment artifacts, remaining settings/resources,
and the coordinated PostgreSQL/object-storage barrier still require integration.

Native acceptance has not run. A read-only check of the dedicated host configured
in `e2e-native.yml` failed because GCP reports its project suspended; restored
access or another designated native KVM host is required for that gate.

### Resource inventory and publication fences (2026-09-30)

Clone operation updates now retain every recorded resource identity. An assigned
source/version/capture point or target cannot be replaced or cleared. The roster
freezes after capture; later phases cannot add resources or erase them, including
compensation. Duplicate resource keys and unknown status values reject updates.
Declared resources must have a recognized strategy and a captured/ready status
before the operation can enter its copy phase.

Publication and readiness require the captured source revision marker, only
recognized resource strategies, every resource ready, and no operation error.
Physical data/workload resources require distinct source/target identities and a
capture/version identity. Object-storage resources must match all stored bucket
manifests, including the exact capture timestamp and manifest hash. Every entry
must have a persisted verified copy receipt; an omitted manifest, an incomplete
copy, or a claimed-ready bucket with no manifest rejects publication.

Focused MemStore and real PostgreSQL contracts exercise omitted/rebound resource
identities, duplicate keys, unsupported strategies, incomplete or missing object
manifests, exact nanosecond capture identities, error-bearing readiness, and
historical compensation receipts. Existing clone operation/object manifest
contracts also pass through the temporary focused runner, and API environment
contracts pass locally. The sqlc generation consistency check passes.

The store gates do not establish inventory coverage by themselves. The capture
worker must still enumerate the complete effective state, connect these operation
and object-copy primitives to resource preparation, and publish a ready deployment
graph. Those integration steps and native acceptance remain outstanding.

### Clone target ownership (2026-09-30)

All environment creation paths now honor active durable clone reservations.
PostgreSQL serializes reservation and environment creation on the project row;
MemStore uses its existing mutex. Only the owning operation in `copying`, at its
current revision, may materialize the isolated target. The operation row remains
locked through the target configuration transaction, fencing concurrent failure
or compensation. Shared-resource requests cannot use the full-clone owner path.

Focused MemStore and PostgreSQL contracts cover stale workers, unauthorized
creation, owner materialization and idempotent replay, plus concurrent creation
versus reservation races. These store contracts do not establish VM acceptance.

### Captured workload artifacts and atomic publication (2026-09-30)

The operation can now persist an immutable workload roster, selected source
artifacts, deployed settings, sidecar layers, and main/sidecar secret reload
opt-ins before provider copying. Capture retries adopt the stored revision;
later desired edits or source rollouts do not replace it. Configuration
materialization for an owning operation uses these captured deployed settings.
Nested JSON numbers retain their exact representation through persistence.
Source variable/secret and project-policy capture remain separate work.

Target deployment creation atomically attaches a fresh, zero-traffic deployment,
its configuration pin, cold-bootable rootfs and sidecar artifacts to the capture.
A retry reuses the original target. An internal API worker step submits durable
prime notifications to schedd and waits for every target to become live; it does
not rebuild source artifacts or copy production memory snapshots. This step is
not yet connected to a claim loop or public full-create API/CLI.

Publication verifies every workload receipt against its captured source and
attached target, live state, unchanged artifacts, desired head and deployment
pin, in addition to verified object-copy receipts. It atomically publishes the
target release graph and completes the operation. Generic operation updates
cannot advertise workloads ready without that graph. Stable environment hosts
return 503 during preparation, including before an edge route substitution;
preparation state cannot contaminate the shared production app cache. Exact
deployment preview routes remain available for readiness checks.

Focused MemStore and real PostgreSQL contracts verify source rollout isolation,
retry identities, sidecars/reload signals, pending/altered target rejection,
configuration races, atomic publication and replay. API handoff/retry and gateway
routing, cache and readiness contracts pass locally. sqlc regeneration is
consistent. The state contracts run through the existing temporary focused
runner because the full state test binary exceeded local disk capacity.
Native KVM acceptance remains outstanding. Full resource coverage, coordinated
data capture, artifact retention during long captures, physical setting remaps,
per-environment capacity and the one-command integration are still required.

### Durable clone worker leases (2026-09-30)

Both stores now support claiming, renewing and releasing clone work with delayed
retries and attempt counts. PostgreSQL owns its lease clock and skips locked
projects while retaining the project-before-operation lock order. A claim or
release increments the operation revision, so a previous owner's capture,
configuration materialization, deployment preparation and publication cannot
commit at its old revision. Expired claims reject clone mutations until takeover.
Renewal requires the current token, status and revision, and does not advance the
configuration fence or shorten a valid deadline. Terminal transitions revoke
the lease atomically, including release-graph publication. Worker tokens are
excluded from serialized lease status.

Focused MemStore and real PostgreSQL contracts cover competing replicas, expiry,
renewal, takeover, stale writes, retry delay, independent projects and locked
projects. The captured-workload publication contract also runs under a worker
lease. Existing operation, reservation, manifest and resource-publication fences
pass through the focused runner. sqlc regeneration is consistent.

These are queue primitives; the persistent API worker loop and public full-create
command still require integration. Provider calls must be bounded by the lease
and replay the frozen resource identities. Immutable object-copy receipts retain
their existing exact source-version/target verification checks; cancellation and
ownership of provider-side preparation remain part of the worker integration.

### Frozen project configuration (2026-09-30)

Workload capture now includes the source project configuration JSON in the same
store snapshot as the selected artifacts and deployed settings. Retry reads the
persisted capture, and owning target materialization copies it instead of the
latest source version. An absent source configuration materializes an explicit
empty target configuration. Public workload metadata exposes only its captured
configuration hash. The private workload hash authenticates the captured values
as well as the existing source version identity; JSONB numeric normalization does
not silently replace that identity.

Publication requires a matching project-configuration inventory receipt and
rechecks both the target version hash and actual stored values under the project
lock. Different values persisted under the same hash cannot pass. MemStore and
PostgreSQL contracts exercise a source edit after capture, capture replay,
exact large JSON numbers, omitted configuration receipts and target changes
between preparation and publication. Source variables, sealed customer secrets,
remaining policy/binding specifications and coordinated provider data capture
still need durable payloads and worker integration.

Source-layer cleanup currently remains based on deployment/snapshot retention.
Reused artifacts require durable retention throughout capture and for every
prepared/serving clone reference before enabling the complete command. The
existing macOS store/fake-readiness checks do not establish that lifecycle.

### Durable scoped value capture (2026-09-30)

The private workload snapshot now persists scoped variables and encrypted secret
configuration in the same database snapshot as its artifact and deployed
settings. Public metadata adds only a source-values hash. Owning target
materialization uses the frozen payload and source scope, so a later rollout,
variable edit or customer-secret deletion cannot redirect or alter the copy.
The preparation hash and quota checks use the captured rows. Missing value
payloads in an older capture are rejected instead of falling back to live rows.

Copied customer-secret rows retain their captured version and lifecycle class,
with independent pending delivery in the new scope. Managed ownership metadata
is captured for resource planning; those envelopes are excluded from customer
secret insertion. Prepared managed-secret accounting remains separate.

Focused contracts pass in the normal state package against MemStore and the
isolated local PostgreSQL fixture. They cover source edits/deletion after
capture, a legacy-to-named-production rollout, replay, independent target secret
delivery, preflight quota rollback and hidden values in metadata. A MemStore
contract proves that removing a source managed credential cannot bypass its
captured preparation requirement or overwrite an independently prepared target.
API handoff retry and gateway publication/readiness checks also pass. Earlier
disk-space failures were superseded by these successful focused runs; these are
not full-suite or native VM acceptance results.

This is durable capture and configuration materialization. Target-value
publication checks and mutation fencing across all intent/reconciliation
writers, host-key resealing and captured-key retention still require integration
before enabling the complete command. Binding/policy payload capture, coordinated
provider data capture and artifact retention remain required as described above.

### Immutable workload layer retention (2026-09-30)

Captured rootfs and sidecar keys now acquire durable pins in the capture
transaction. Existing private captures are backfilled by the migration. A full
capture requires a backend storage key; a host filesystem path alone cannot
establish an immutable shared artifact identity. Prepared deployments, serving
deployments, usable snapshots, resident instances, aliases, and active or
unexpired release graphs retain their layers. Captured and retained references
remain in deduplicated physical layer accounting after source soft deletion.

Cleanup records a durable request even when a consumer currently holds the
layer. Once unreferenced, it commits a deletion identity before touching the
backend. Publication and consumer guards serialize against that identity, and
a claimed/deleted key cannot acquire another consumer. Backend errors keep the
claim pending; a fresh imaged daemon retries both failed deletions and previously
held cleanup requests without requiring the original snapshot rows. Cleanup
resolves recorded main and sidecar keys as well as the canonical legacy key.
Permanent backend refusals remain pending and observable as errors; they are
not recorded as successful deletion.

MemStore and real PostgreSQL contracts cover captured source retirement,
prepared targets, deduplicated capacity, concurrent cleanup/publication,
snapshot/instance/alias/release retention, retired-key rejection, stable deletion
identities, cleanup-request persistence and migration backfill. Imaged contracts
cover backend failure followed by daemon reconstruction, held stage layers,
sidecars and reclamation after the last reference disappears. Earlier focused
checks passed in the normal state package; the expanded contracts use a
temporary runner with narrow fixture interfaces because local disk capacity
prevented linking the full state test binary. This is store/backend evidence,
not native VM acceptance.

Physical existence verification and shared base/kernel/runtime artifact
lifetimes still require audit. The complete command, coordinated provider data
capture, complete configuration/binding coverage and native KVM lifecycle gates
remain required before this feature can be advertised as fully copyable stages.
