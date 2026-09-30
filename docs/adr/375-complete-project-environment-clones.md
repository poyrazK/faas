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

### Target value publication checks (2026-09-30)

Every captured workload now requires explicit variables and secrets inventory
receipts, including empty sets, tied to its app and frozen source-values hash.
Both the transition into publication and the final release-graph transaction
read the actual target rows. For customer values they compare the complete
captured configuration, mapping only the source/target scope address. This
includes the sealed envelope, host-key identity, lifecycle class, ownership
metadata and secret version; runtime delivery observations are excluded.
Missing, extra or modified rows reject publication without acquiring a serving
graph. Error diagnostics include workload and field categories, never values.

PostgreSQL configuration writes to variables and secrets now take the same app
row lock held by publication, covering direct writes, deletion, customer intent,
resealing and managed-binding reconciliation. Delivery-only updates are excluded
from this fence. MemStore already serializes these operations with its mutex.
This establishes an order between the proof/graph commit and configuration edits;
editing a published stage remains a normal configuration operation.

Focused MemStore and PostgreSQL contracts verify edits before publication,
changes between the two gates, unexpected variables, changed envelopes under an
unchanged value digest, secret deletion, missing receipts and wrong capture
hashes. Real PostgreSQL blocking tests verify existing-key updates, inserts,
deletes and maintenance resealing against the publication lock. The managed
credential contract verifies a named rejection instead of advertising readiness
without an isolated-resource and prepared-envelope proof.

Managed values remain intentionally unavailable at this publication gate until
their durable binding specifications, fresh target-envelope identities and
independent provider-resource proofs are integrated. Host-key resealing and
captured-key retention also remain required. These store checks do not establish
native readiness, coordinated provider data capture or the complete command.


### Frozen managed-resource catalogue (2026-09-30)

New private workload captures include managed PostgreSQL binding definitions,
database specs and provider placement/identity, plus every workload-owned bucket
in the selected source scope. Bucket definitions include public-read/serve-path
policy, active credential labels and permissions, compute-binding ownership and
API-key access grants. Standalone buckets and customer credentials are included
without relying on application secret discovery. Transient leases, retries,
rotation staging rows, signing keys and sealed credential material are excluded
from the worker catalogue. Captured managed application envelopes remain in the
existing private values capture and are never replayed as customer credentials.

PostgreSQL reads these catalogue rows in the same repeatable-read transaction as
workload artifacts, deployed settings and source values. Capture rejects unready
resources, missing managed envelopes, mismatched credential generations, and
incomplete or foreign-scope object bindings. Each normalized definition set has
a separate hash and enters the complete workload capture hash. Retries and the
account/project-scoped worker reader use the persisted payload, including after
source edits or metadata deletion; returned definitions are independent copies.
Older captures without a catalogue cannot supply a complete resource plan.
MemStore captures its object catalogue atomically, but rejects managed PostgreSQL
capture with a named unavailable error because that memory catalogue belongs to
a separate store. A secret ownership ID cannot substitute for a provider spec.

Publication rejects captured standalone resources with a named proof-unavailable
error until their isolated target resources and copy receipts can be verified.
This closes the path where a bucket with no managed application secret could be
omitted while the workload/value graph was advertised ready. The existing
managed-envelope publication gate remains in place. Catalogue capture preserves
configuration metadata; it does not retain provider data or establish a shared
application write checkpoint.

Focused MemStore and real PostgreSQL contracts cover standalone bucket policy,
credential permissions and access grants, source deletion, retry stability,
caller-copy isolation and ownership boundaries. PostgreSQL contracts also cover
frozen database specs/access/generation and missing managed envelopes. The managed
credential preparation/publication regression now runs against the real
PostgreSQL catalogue rather than invented memory-store binding IDs. The complete
worker, coordinated provider capture, independent target proofs, credential
issuance/resealing and native VM acceptance remain required.


### Restore preparation from captured database definitions (2026-09-30)

Managed PostgreSQL restore requests can now carry a frozen source definition.
The account-owned source must still resolve to the captured provider resource,
backend and fingerprint. The restore target uses the captured spec rather than
re-reading later desired configuration. Preparation checks both captured and
current recovery windows, and rejects incompatible existing or concurrently
adopted targets before reconciliation/provider I/O. Project environment database
copy preparation supplies this definition from its plan. The durable catalogue
worker can supply the same definition from its private capture.

Focused service contracts verify frozen specs after source edits, idempotent
restore retries, unchanged production intent, physical placement/identity drift,
shortened recovery windows and conflicting target adoption. This preserves
restore configuration identity; application write checkpoints and the complete
leased clone worker remain separate required integrations.


### Frozen scoped route and edge policies (2026-09-30)

Durable workload captures now include the selected deployment's explicit route
configuration and the source environment's headers/CORS policy, including the
absence of a scoped policy. Each normalized policy definition has a separate
hash and enters the complete private workload hash. Materialization uses these
captured definitions rather than reading mutable source route/edge rows again.
Routes therefore agree with the deployed workload settings even when a desired
source head or legacy route row differs. A scoped edge policy added after
capture does not silently appear in the target.

Both publication gates authenticate the actual target route and scoped edge
rows. PostgreSQL route/edge configuration inserts, updates and deletes take the
same app lock held by publication; readers do not acquire policy-row locks in a
reverse order. Existing environment-before-app route writers keep their lock
order. Captures predating the policy payload cannot materialize a complete
configuration through a live-source fallback.

MemStore and real PostgreSQL contracts cover source policy edits/additions after
capture, route agreement with deployed settings, edge changes before both gates
and direct route-row changes under unchanged workload hashes. PostgreSQL blocking
contracts cover route/edge insertion, update and deletion as well as variable and
secret writers. The expanded store contracts run through the temporary runner
with narrow fixture interfaces to avoid the large state test binary on this
shared disk. SQL generation matches independent regeneration.

This captures the current environment-owned policy surface. Application-wide
edge-rule kinds, inherited application policies, CORS preset references and
OpenAPI-backed route documents still require isolated ownership, frozen payloads
and coverage checks before enabling complete stages. Provider copy proofs,
coordinated data capture, the complete worker/command and native VM acceptance
remain required.

### Full-copy command and durable status contract (2026-09-30)

The SDK and CLI now recognize `env create --full --wait`. Full requests use a
dedicated `POST /v1/projects/{slug}/environment-clones` route, so older servers
cannot ignore a new flag and perform a partial clone. Both this route and full
requests to the legacy create endpoint reject admission with the stable
`environment_full_clone_unavailable` code and named coverage, checkpoint,
managed-binding and policy blockers. Admission performs no clone writes.

`GET /v1/projects/{slug}/environment-clones/{clone}` exposes the durable
operation before its target environment exists. The account/project checks
apply to the operation itself. Explicit response DTOs exclude private captures,
values, idempotency keys and worker leases; status responses are not cached.

`env clone-status` resumes inspection or waiting. Polling keeps the captured
operation identity and monotonic revision, distinguishes terminal failure from
success, and returns a single JSON receipt. Local timeout returns exit code 3
and a resume command without cancelling the server operation. SDK/CLI/handler
contracts and OpenAPI route/schema/error parity pass. Full admission remains
closed until the complete leased worker and isolation proofs are implemented;
these command contracts do not establish full stage support.

### Leased PostgreSQL copy step from the frozen catalogue (2026-09-30)

The copying-phase database worker step now reads only the stored workload and
binding captures. It authenticates their rosters and hashes, rejects conflicting
definitions across workloads, and produces one physical restore per captured
source database. Target reservation names include the operation identity. The
capture owner must record the same canonical microsecond recovery time for all
database resources before entering copying; a retry never chooses a new time.

Before provider IO the worker renews its token/revision lease, verifies the
exact database resource roster, and bounds IO by the lease deadline. Each
completed reservation is checkpointed with its target ID and status before
another database starts. Target account, spec, backend, physical separation,
restore origin and recovery time must agree with the capture. A worker that
loses its lease cannot checkpoint or compensate a replacement worker's copy.

Account-scoped lookup by the exact operation-owned reservation name repairs a
crash after provider completion but before the operation checkpoint. Completed
restores can be adopted after their original PITR window expires, without
re-reading source definitions or performing another restore. Pending restores
still pass the service's current and captured recovery-window checks.

Focused real PostgreSQL evidence covers two workloads sharing one database,
source spec/binding edits after capture, an unavailable checkpoint, replay after
PITR expiry, stale-worker rejection, takeover during provider IO, and rejection
of changed target lineage. Planner contracts cover invalid/missing catalogues,
resource inventories and inconsistent recovery points. This step does not yet
create target credentials or establish a cross-provider write checkpoint.
The claim loop, object-copy/credential steps, complete coverage gates and
native acceptance remain required; full-copy admission stays closed.

### Object manifest writes require worker ownership (2026-09-30)

Manifest insertion and copied-object checkpoints now accept explicit worker
authority. Both stores verify account/project, token, revision, phase and live
lease under the operation lock. Legacy writer methods are limited to operations
that have never been claimed, including after a claimed lease is released.
A replaced worker cannot record progress under its replacement's live lease.
The PostgreSQL manifest path uses generated sqlc queries throughout.

Leased capture/copy helpers renew ownership before provider IO and bound IO by
the renewed deadline. Retries use the already committed version manifest.
MemStore and real PostgreSQL contracts cover expiry, release, takeover and
idempotent checkpoints; the application worker contract covers takeover after
physical copy and retry of the same pinned version without relisting.

These checks fence durable metadata writes. An already accepted provider write
can outlive a worker, so physical write completion, pending target access and
publication still require isolation checks before full-copy admission opens.

### Durable object target reservations and copy worker steps (2026-09-30)

A full-copy bucket reservation now has a durable operation owner as well as its
captured source bucket identity. The leased reservation writer reads placement
only from the authenticated private catalogue under the project/operation and
app locks. Generic reservation writers cannot assign or adopt this owner.
Retries adopt the same owned placement after source metadata deletion, provider
completion or worker takeover; a foreign target or changed placement is rejected.
Standalone buckets are included even when no managed application secret refers
to them. Target names include the operation identity, and quota checks include
unfinished copies.

Targets remain private and inaccessible through customer bucket reads/lists,
S3 credential creation/resolution, API-key grants, multipart initiation and
upload-route creation until the owning operation is ready. Internal target
reads require the current worker token, revision and lease. Captured public
policy remains in the private catalogue for the eventual publication writer.

The capturing worker step requires the coordinator's persisted canonical
microsecond data point, preflights all providers, reserves/checkpoints targets,
provisions private buckets and commits exact version manifests before copying.
The copying step authenticates the complete bucket roster and manifest hashes,
uses frozen provider placement, verifies copied bytes and checkpoints completion.
Object and PostgreSQL resources must declare the same data point. This equality
check does not itself establish an application write barrier. Replays of completed
steps make no redundant progress write, relist or object copy.

MemStore and real PostgreSQL contracts cover ownership, quota exhaustion, foreign
targets, source deletion, takeover and pending-target access. The application
worker contract runs against both stores and injects failures after manifest
commit and byte-verified copy but before resource checkpoint. Existing bucket,
credential, grant, multipart and upload-route store contracts and focused API
regressions pass; independent sqlc regeneration matches.

These are callable phase steps, not a deployed claim loop. Full admission remains
closed while credential preparation/publication proofs, complete policy/resource
coverage, source data retention and coordinated checkpoints, physical provider
write fencing, compensation and native VM acceptance are still required.

### Atomic object credential preparations (2026-09-30)

A private preparation ledger now commits each captured source credential's fresh
target credential and, for a compute binding, all six sealed runtime envelopes
in one transaction. Its hash excludes delivery observations and authenticates
the prepared identities and encrypted content. The ledger is not an API DTO.
Replay recovers the same credential rather than minting another key, and the
reader checks the actual credential and target envelopes against the ledger.
Changed, revoked or missing prepared content cannot silently become a proof.

The writer requires the live token/revision lease in copying, the frozen source
credential's label/permissions/binding prefix, an operation-owned private bucket
and a complete verified object-copy receipt. It accepts neither source resource
identities nor source scopes as targets. Customer credentials have no runtime
envelopes; compute credentials have exactly the captured binding's six keys.
Preparation happens before environment materialization, which already requires
independent prepared managed values. The operation's existing target reservation
provides ownership while the environment row does not yet exist.

Store contracts cover stale authority, missing data proof, source deletion,
quota exhaustion, incomplete/mixed envelopes, secret-key collision rollback,
replay, independent caller copies, takeover, private data-plane denial and
changed target-envelope rejection. They run against MemStore and real PostgreSQL
with the existing credential/binding regressions. Independent sqlc regeneration
matches. Generic customer credential writers continue to reject pending copies.

The worker still needs to generate/seal credentials and consume these receipts,
and the complete publication proof must authenticate every resource, grant and
managed value. These preparations do not open full admission or substitute for
coordinated provider capture, physical write fencing and native acceptance.

### Object credential worker and source runtime isolation (2026-09-30)

The copying phase now consumes the private credential preparations. It requires
every captured bucket's copy receipt to be ready before preparing credentials,
renews the worker lease before each preparation, and reads the durable receipt
before generating keys. New target credentials retain captured labels,
permissions and binding prefixes, receive independent signing keys, and seal
the six compute values for the target bucket/scope. Only compute credentials
contribute the IDs and secret count required by environment materialization.
Standalone customer credentials are recreated without application envelopes.

A completed replay uses the persisted identities and sealed values without
requiring the current endpoint configuration or host encryption keys. It neither
regenerates keys nor changes operation progress. Private preparation skips the
generic app-wide runtime stamp and snapshot invalidation: the target has no
running release, and preparing it must not invalidate production snapshots.
Normal customer binding creation retains its existing invalidation behavior.

The MemStore and real PostgreSQL worker contracts inject failure after a
credential commit but before its acknowledgement. They remove production
credentials before preparation, decrypt target values with an independent test
identity, verify exact target mappings and permissions, materialize the prepared
stage, and resume under replacement ownership without additional credential
writes. Store contracts also verify production runtime stamps and snapshots
remain intact during private preparation. Full admission remains closed pending
the complete publication and operational isolation proofs described above.

### Object preparation proofs before materialization (2026-09-30)

Durable environment materialization now authenticates every captured managed
object credential against this operation's private preparation ledger. Matching
binding IDs and secret counts alone cannot establish isolation. The check
requires the mapped target ID to be one of the caller's prepared bindings,
revalidates captured permissions/prefix, operation-owned private placement and
the byte-verified copy receipt, and compares actual signing material and all
runtime envelopes to the immutable preparation hash before writing an
environment. Mixed PostgreSQL/object secret ownership is rejected explicitly.
Legacy configuration clones keep their existing preparation contract.

The PostgreSQL verifier locks the app/bucket and then the actual signing and
envelope rows in the materialization transaction. The ledger's separate target
identity must agree with its authenticated payload. A concurrent host rekey
cannot leave materialization using a stale equality result: it waits for the
signing row and rejects a serialization conflict or changed prepared content.
Missing content behind an existing receipt is a conflict, rather than an
invitation to generate replacement keys. Infrastructure errors retain their
original classification for retry handling.

MemStore and real PostgreSQL contracts reject forged prepared bindings with
matching counts, reject altered envelopes before environment creation, and
accept the authentic preparation after restoration. The PostgreSQL contract
observes an actual lock wait behind an in-flight signing rekey, commits that
change, and verifies no environment was created. The worker's real-crypto
materialization/retry contract continues to pass. Independent sqlc regeneration
matches. This establishes the object preparation portion of materialization;
complete resource/managed-value publication and full admission remain gated.

### Managed object value publication proof (2026-09-30)

Publication now builds its expected managed object values from authenticated
target preparations, using the same catalogue, independent placement and copied
data checks as materialization. It verifies the original captured value hash
first, copies customer variables/secrets into the target address, and substitutes
each managed object envelope with its fresh target owner, ciphertext, key
identity, class and initial version. The complete actual target value set must
match that expected configuration; extra, missing, mis-scoped or changed values
cannot be justified by a generic ready receipt. Managed PostgreSQL values still
require their own preparation proof.

Store contracts exercise a workload containing both customer values and six
managed object secrets. The authentic preparation passes the value check and
reaches the independent resource-publication guard. An altered preparation is
rejected before that guard, and no serving release is created. Existing
customer-value, missing-receipt, scoped-policy, PostgreSQL managed-value and
concurrent-intent publication contracts pass against MemStore and PostgreSQL.

The resource publication guard remains closed for captured databases/buckets:
the value proof does not establish every credential/grant/public-policy mapping,
provider write fencing, cross-provider checkpoint consistency or retention.
Full-copy API admission remains closed and this work is not native acceptance.

### PostgreSQL ownership and customer visibility (2026-09-30)

Managed PostgreSQL now has an operation ownership marker. Owned rows must carry
complete restore lineage and an independent logical/provider identity. Ordinary
reservations cannot assign or adopt that marker. Customer database reads/lists,
create/restore adoption, deletion and binding operations exclude private targets.
Visibility requires a ready owning operation, its target environment and an exact
ready source-to-target database resource mapping. Unknown owners stay private.
Internal catalogue reads remain complete for lifecycle and accounting work.

Normal restore and binding reservations also check customer visibility inside
their transaction. The new queries use sqlc; normal databases retain their
existing behavior. MemoryStore lacks the durable clone catalogue and therefore
keeps every owned database private rather than guessing publication authority.

The managed PostgreSQL package and APId database/worker regression contracts
pass against real PostgreSQL. They cover non-ready owners, incomplete publication
metadata, ordinary operation denial without provider calls, independent identity
constraints and preservation of internal visibility. Independent sqlc output
matches. This slice supplies visibility enforcement; the clone worker still
needs to assign ownership atomically during reservation before provider IO.
Fresh PostgreSQL binding preparations, compensation and full publication remain
unfinished, and full-copy admission remains closed.

### Leased PostgreSQL reservation before restore (2026-09-30)

The database phase now reserves operation-owned targets through the state store,
before invoking the managed PostgreSQL reconciler. The reservation transaction
locks the project/operation, authenticates the current worker token and revision,
validates the complete database roster and common canonical recovery point, and
derives target configuration solely from the authenticated workload catalogue.
It serializes account quota with ordinary reservations and checks live source
provider identity, availability and both captured/current recovery retention.
Lease authority is rechecked after lock waits and before commit.

Ownership, frozen configuration and restore lineage commit in the same target
row. A unique operation/source reservation index retains that identity even
after deletion. Recovery reads it under the current clone lease and rejects
changed placement, names, lineage, owners or checkpoint mappings. Foreign
customer reservations, including deleted name collisions, cannot be adopted.
The database definition receipt encoding remains compatible with the earlier
worker. The managed service exposes admission/capability validation without
provider IO; ordinary restores use that same validation.

Completed replay reads only the authenticated private target. It can survive
lost reservation/restore acknowledgements, source unavailability or shortened
retention, exhausted quota and a subsequently disabled provisioning rollout.
Pending targets reconcile through the internal lifecycle path; customer
Get/Restore intentionally cannot access them. A restored database remains
private until full publication, rather than becoming visible on provider ready.

Real PostgreSQL worker contracts verify invalid authority/rosters, quota failure
without writes, source identity and retention drift, observed account-lock waits
that outlive the lease, lost acknowledgements, changed/duplicate reservations,
private customer access during provider restore and takeover without another
restore. Managed PostgreSQL package and APId clone/binding regressions pass;
independent sqlc output matches.

This completes private database reservation wiring, not full data isolation
acceptance. Fresh PostgreSQL credential preparations and their publication
proofs, leased compensation, coordinated retained capture and physical provider
write fencing remain required. There is still no persistent full clone loop,
and full-copy API admission remains closed pending the remaining coverage and
native acceptance gates.

### Private PostgreSQL binding preparation (2026-10-01)

The PostgreSQL credential phase reserves each independent binding identity before
provider IO. Its private ledger binds the operation, captured source binding,
target app/scope/key/access, restored database placement and data receipt. The
reservation is authenticated under the current clone lease and shares the
account/app/secret-target locks used to serialize intent and quota. A provider
request uses the reserved binding ID and first credential generation, so lost
acknowledgements and replacement workers retry the same provider intent.

The managed binding service accepts this reserved intent through a separate
deadline-bound preparation path. The worker seals the new connection URL and
atomically commits its envelope, ready binding state and immutable preparation
hash through the leased state writer. Reads compare the actual binding and
sealed envelope with both reservation and preparation hashes. Completed replay
uses that durable proof without the source secret, current host encryption keys,
provisioning configuration or additional quota. Source secrets are never opened
to issue target credentials.

Private preparations preserve production runtime stamps. Ordinary binding
reconciliation, due sweeps and the ordinary credential sink exclude pending
operation-owned databases. The schema snapshot also includes the pre-existing
PostgreSQL secret ownership columns, allowing all new SQL and typed catalogue
reads to be generated by sqlc.

Real PostgreSQL worker contracts cover read/write and read-only bindings sharing
one restored database, independent target decryption, lost reservation and
preparation acknowledgements, quota rollback, mismatched ownership/scope/key,
altered envelopes, completed replay and worker takeover during credential IO.
An old worker cannot commit after takeover; the successor retries the identical
provider request. The managed PostgreSQL package regression and APId clone and
binding contracts pass. Independent sqlc regeneration matches.

This supplies the private preparation phase. Materialization and publication
must still authenticate these receipts before accepting PostgreSQL managed
values. It does not fence already accepted physical provider writes, implement
leased compensation or the persistent full clone loop. Full-copy admission and
native acceptance remain gated by the requirements above.

### PostgreSQL preparation proofs before materialization and publication (2026-10-01)

Durable materialization now authenticates every captured managed PostgreSQL
binding against its private reservation and preparation ledger. It reuses the
worker's captured database roster, canonical recovery point and independent
operation-owned placement checks, then locks the actual binding and sealed
secret and compares their content with both immutable hashes. Every resulting
target binding must appear in the prepared ID set. Caller-supplied IDs and equal
secret counts cannot justify an unowned database or a changed preparation.
Legacy configuration clones retain their existing contract; MemStore cannot
establish a durable PostgreSQL restore proof.

Publication derives expected PostgreSQL values from the same authenticated
preparations, substituting the target owner, credential reference, generation,
scope/key, ciphertext, key identity, class and first version. Customer values
remain tied to their captured source hash. Every workload's complete actual
target value set is checked before the independent resource-publication guard;
an earlier resource blocker cannot conceal drift in a later workload.

Real PostgreSQL worker contracts reject forged preparation IDs, altered binding
identities/generations/keys, reservation hashes and ciphertext before environment
creation. They observe materialization waiting behind a concurrent binding-row
edit and reject its committed change. Authentic preparations materialize with
the captured customer values and reach the independent resource guard. Changed
managed envelopes and edited/extra customer variables on either workload fail
publication before that guard, and no serving graph is created. APId clone and
binding regressions and the state clone/object/value-publication contracts pass.

These checks establish PostgreSQL managed-value preparation evidence. Complete
resource, grant and public-policy publication, coordinated retained data capture,
physical provider write fencing, compensation, the persistent full clone loop
and native acceptance remain required. Full-copy admission stays closed.

### Atomic configuration capture and derived source revision (2026-10-01)

A new internal PostgreSQL creation path accepts source/target names and an
idempotency key without a caller-supplied revision or release. In one repeatable
read transaction it locks the project/source and workload roster, selects the
actual source release and effective value scopes, freezes the existing typed
workload catalogue, derives its canonical root, reserves the operation, stores
the captures and pins their layer artifacts. The operation's source revision is
the derived root hash. A failure, including retired artifact detection after
row insertion, rolls back the reservation and catalogue together.

The versioned root binds account/project/source/release identity, the project
configuration version and normalized content hash, and the sorted workload
roster with artifact/snapshot, settings, values, bindings and policy hashes.
Operation IDs, target names and target preparation progress do not change the
source revision. Its stored definition contains identities and hashes; values
and sealed material remain in the private workload captures.

A durable marker distinguishes this path from legacy operations. Every
PostgreSQL workload-catalogue read verifies a marked root against the operation,
its stored bytes and the reconstructed frozen catalogue before workers,
materialization or publication consume it. A missing root or workload cannot
downgrade to legacy behavior or trigger recapture from current production.
Leased root reads check authority before and after verification. Retrying the
creation request authenticates and returns its committed capture without
re-reading the live source; a legacy operation cannot be adopted as a marked
capture through an idempotency collision.

Real PostgreSQL contracts cover effective production/default scopes, equal
source revisions across different target identities, source edits/new captures,
lost-response-style creation replay, wrong/stale ownership, altered/missing
roots, incomplete rosters, artifact-pin rollback and an undeployed workload
failure without partial reservation. State clone/object/value-publication and
APId clone/binding regressions pass; independent sqlc generation matches.

This root authenticates the currently implemented configuration catalogue. It
does not assert coverage of all related policies, triggers and integrations,
capture empty projects, coordinate or retain provider data checkpoints, or
enable full-copy admission. The resource coverage registry and capture barrier
must extend this path before the complete command can use it.
