# ADR-531 · Complete project-environment clones and qualified promotion

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
barrier, independent data-resource publication proofs, complete compensation,
environment ownership of the remaining policies/triggers/integrations,
scope-specific capacity and
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

### Application schema coverage registry (2026-10-01)

The clone boundary now has an explicit table/column registry checked against
the actual migrated PostgreSQL schema through sqlc. Discovery includes every
regular or partitioned parent table in the active application schema. Tenant
identity columns and foreign keys alone are insufficient: generic scope IDs,
including `runtime_config_entries.scope_id`, can also select application
configuration. Partition children inherit the parent policy. Other schemas are
outside this check.

The initial registry classifies 289 tables: 80 configuration, two retained
customer-data tables, 172 operational tables, 25 account-identity tables and
10 platform-configuration tables. Configuration requires an environment-scoped
capture/copy/diff/qualification/promotion/rollback strategy. Retained realtime
channel heads and messages require an isolated data strategy. Operational
records describe executions, leases, observations, delivery history or platform
bookkeeping; target operational state must be initialized by its owner.
Account identities remain account-owned. Platform infrastructure and immutable
runtime catalogues are references whose existing acceptance and retention
requirements remain applicable.

Every table and column name is explicit. Unknown tables/columns and missing or
renamed tables/columns produce deterministic named blockers. The canonical
schema report hashes sorted table names, classifications and column names.
Input order or caller classifications cannot alter the registry, and returned
policies are defensive copies. This hash describes schema coverage; it does
not hash customer row content, types, constraints or JSON object keys.

Atomic internal capture checks for an unrecognized schema before reserving a
new operation. Committed idempotent replay continues to authenticate its frozen
catalogue without depending on the current live schema. Both public full-clone
entry points add named schema/strategy blockers to the existing closed
admission response. A schema read failure returns an unavailable response and
cannot fall through to creation. The report is project-authorized and reads
metadata only, without customer values or sealed material.

The migrated-schema contract detects a new setting column, a new table without
ownership columns or foreign keys, directly and indirectly referenced tables,
and a renamed table. It verifies rejection before operation/target reservation,
immutable replay, restored-schema recovery and account isolation. Registry
contracts check configuration/data/identity/operational/platform boundaries,
canonical ordering and caller mutation isolation. HTTP contracts cover named
blockers, failed schema reads and absence of a partial target on both routes.
State clone/object/value-publication regressions and APId clone/binding
regressions pass. Independent sqlc regeneration matches the committed output.

Registration alone establishes no complete copy strategy. The two retained
data tables and all configuration tables still report unavailable complete
strategies, including tables with existing partial capture support. Freezing
their actual source instances and content, extending the configuration root,
coordinating retained data checkpoints, proving publication and promotion,
worker orchestration and native acceptance remain required. Full-copy admission
stays closed.

### Frozen work-policy definitions and producer bindings (2026-10-01)

Atomic configuration creation now captures the application work-policy
catalogue and its event/trigger bindings in the same repeatable-read transaction
as workloads, values, managed bindings and scoped route/edge policies. The
versioned private definition contains the effective source app/scope, the
complete policy roster with policy revisions, replacement/debounce/expiry and
fairness settings, and every event/trigger work binding with its source identity,
selectors and action. Unbound policies are part of the roster. Creation/update
timestamps, runtime lanes, invocation/trigger work queues and cancellation
receipts are excluded from this configuration catalogue.

The current work-policy and producer APIs are application-wide. This capture
freezes that actual shared effective configuration; it does not assert that the
source or target already has independently scoped policy ownership. The source
query includes every policy/binding row for the app and counts invalid policy
or producer-parent ownership separately, so an ownership mismatch cannot become
a silently omitted row. Normalization validates policy revisions, bounded
durations before duration conversion, modes, identities, selectors, duplicate
rosters and binding-to-policy references, then sorts each roster. Empty
collections are explicitly versioned captures.

The definitions are private workload-snapshot material. They contribute to the
workload policy hash, snapshot hash and derived configuration-root revision.
The root remains an identity/hash-only document. Timestamps cannot cause a
configuration change through these definitions. Existing snapshots without
this extension retain their serialized/hash contract; new atomic captures do
not regenerate policy definitions on idempotent replay.

A leased internal reader authenticates the operation's marked configuration
root and frozen workload roster, checks its live authority before and after
reading, and returns defensive typed definitions with a canonical catalogue
hash. Missing, changed or invalid material fails; the reader never falls back
to live production policies, and legacy operations cannot supply the marked
capture contract.

Publication of a capture with this catalogue is explicitly blocked pending
durable work-policy isolation evidence. Matching values or counts cannot prove
environment-scoped admission, independent producer identities or work lanes.
The guard applies to empty catalogues as well, because a later production
policy must not appear in an already copied empty stage.

Contracts cover policy/producer field coverage, canonical ordering, invalid
graphs and overflowing durations, caller mutation isolation, explicit empty
capture, immutable replay after live policy/binding edits, changed source
revision on a new capture, wrong lease ownership, tampered/missing frozen
catalogues, and invalid policy ownership without a partial operation. The sqlc
schema snapshot now includes these three pre-existing tables; no new live
tables or migration are introduced by this capture extension.
State clone/object/value-publication, schema/capture and work-policy/binding
regressions pass, as do the APId clone, binding and work-policy regressions.
Independent sqlc regeneration matches.

Runtime integration still requires scoped policy definitions and edits, scoped
producer configuration, admission and retained-release routing that select the
requested environment, independent replacement/fairness/cancellation lanes,
and complete qualification/promotion/rollback proofs. Work lanes are currently
keyed by app/policy/key and do not establish stage isolation. Full-copy admission
remains closed while these and the previously listed requirements are completed.

### Stage selection for durable request invocations (2026-10-01)

The gateway async envelope now carries the environment selected by host routing
as a separate internal field. An admission resolver compares any client release
or deployment pin with that environment. Unpinned stage requests select the
stage's active release and persist its immutable pin. A stage with no active
graph cannot fall back to production or an unscoped wake. Standalone named
environments remain unavailable through this path. The current APId invocation
endpoints explicitly select the default environment and reject stage pins until
their configuration and producer admission support scoped selection.

Delivery recovers the environment from the owned identity of the persisted
release/deployment pin. A new sqlc query reads only that identity; the existing
resolvers still enforce retention, membership and live deployment status. Stage
resolution additionally checks the published environment's account/project
identity and the selected deployment's actual app, scope and live status. A
missing or expired graph, foreign pin, malformed stage-to-production member or
unavailable stage reader cannot select production. No new customer header,
invocation column or migration is needed. Older delivery code rejects the stage
pin against its production lookup, so newly admitted stage work cannot become a
production invocation during a rolling upgrade.

Async route receipts have independent namespaces for each stage. Production
keeps its committed receipt IDs, including the legacy default alias. Conflicting
existing receipts must resolve to the same logical environment. Stage request
admission reads retry defaults and request-listener compatibility from the
selected deployment's immutable workload settings, so a desired-head edit does
not change a retained deployment's retry configuration.

Queue consumers, keyed policy lanes and completion destinations still have
application-wide ownership. Stage use of these resources returns a named work
isolation conflict. Both state stores reject queue/destination admission and
keyed admission before insertion or lane mutation, and delivery rechecks the
same restriction. The async HTTP surface returns the stable
invocation_environment_work_isolation_unavailable conflict code for this guard.
A delayed rejection alone would permit keep_latest to replace
an existing production row before failing delivery.

MemStore and PostgreSQL contracts cover stage admission and frozen delivery
after active-graph changes, explicit revision pins, both directions of ingress
scope mismatch, absent graphs, invalid scope/account/foreign-app pins, and
rejection before production replacement. PostgreSQL contracts also cover an
expired release and a malformed graph member. Gateway/enqueuer contracts cover
host-derived scope, independent and conflicting receipts, legacy receipt
identity, and pinned retry defaults after desired settings edits. The scheduler
component contract verifies that draining an admitted stage invocation selects
the stage deployment. These checks use fake lifecycle components and do not
establish native KVM acceptance.

Selected state, gateway, gateway-internal, scheduler and APId regressions pass.
Independent sqlc regeneration matches the generated files.

The API regression run exposed a pre-existing inventory assertion expecting a
newer deployment outside the active release. It failed identically against the
previous commit through a source overlay. The assertion now follows the active
release member, consistent with the existing environment-state implementation
and the release selection decision above.

This routing contract does not establish scoped policy definitions, producer
ownership, work lanes, complete runtime configuration/binding isolation or
qualification/promotion/rollback coverage. Full-copy admission and the captured
work-policy publication guard remain closed while those requirements and the
previously listed resource/provider and native acceptance requirements remain.

### Environment-owned desired work policy collections (2026-10-01)

Work policy definitions now have a complete optional collection within the
existing immutable workload settings. This reuses desired-head compare-and-swap,
environment protection, deployment pinning and configuration hashes. It adds no
table or independently mutable pointer. Nil identifies legacy material; an
explicit empty collection persists through deleting the last policy and never
inherits later application-wide production definitions.

State edits preserve unrelated settings and use a collection revision clock.
New or changed definitions receive the next clock value; unchanged definitions
keep their revisions. Deletion advances the retained clock, so recreating a name
cannot reuse its earlier revision. Caller-supplied definition revisions are not
authority. A no-op preserves the settings ID and hash while still checking CAS
and protection transactionally. Clock overflow, malformed definitions, duplicate
names, invalid duration ranges and policy quota overflow fail before mutation.
Reads authenticate account/project/app/environment identity and the settings
hash. Generic app settings edits preserve the extra collection defensively.

Policy CRUD accepts `?environment=<registered-stage>` and the optional
`If-Workload-Revision` header. Responses expose the containing workload revision
and configuration hash. An uninitialized stage collection is unavailable rather
than a production lookup. Stage responses use the containing immutable settings
creation time for their configuration metadata timestamps. Legacy unqualified
production requests continue using their existing collection. Protected scoped
edits are refused, including no-ops. Direct production collection activation
through the new state edit helpers remains unavailable.

Atomic source capture freezes the deployed policy collection into workload
settings. For legacy deployments it captures the complete production roster,
including unbound definitions and explicit emptiness. A deployment already
pinned to an environment-owned collection uses that collection, independent of
later production policies or desired-head edits. The private producer catalogue
and settings book must agree. Application-wide producer bindings without a
definition in the scoped collection produce the named isolation conflict and
leave no partial operation. Committed operation replay retains its old root and
does not recapture live source policies. Legacy nil-book serialized hashes remain
unchanged.

This establishes editable desired policy ownership and immutable capture, not
runtime activation. Work lanes, cancellation receipts and producer ownership
still need isolated environments. Stage cancellation is rejected before the
legacy idempotency lookup, which otherwise could replay a production receipt for
the same path and key. Production receipt replay remains usable without
cancelling newer work. Qualification refuses any desired or pinned policy book,
including an empty one, with a named activation-proof conflict. Clone publication
also refuses a settings book even when a legacy capture has no producer catalogue.
Neither byte equality nor a settings revision proves isolated execution.

MemStore and PostgreSQL contracts cover explicit emptiness, production/sibling
stage isolation, frozen deployment reads, stale and protected edits, no-op
identity, deletion/recreation clocks, defensive copies and qualification refusal.
PostgreSQL capture contracts cover stage-to-stage pinning, unchanged roots after
global production edits, empty capture, replay and unsupported producer rollback.
API contracts cover scoped CRUD, invalid/ambiguous selectors, protection and
cancellation before receipt replay. The OpenAPI embed and generated Node/Python
clients are synchronized, including previously added stage APIs. These checks
do not establish native KVM, provider data consistency or complete clone acceptance.

Selected state and APId regressions pass, including clone and qualification
contracts and legacy idempotency cases. OpenAPI lint passes with existing
warnings, the Node SDK builds, and the selected Python client/post-processing
tests pass (35 cases). No new SQL was added in this step.

Full-copy admission and policy publication remain closed. Next requirements are
durable environment ownership of keyed lanes and producer bindings, runtime
selection of pinned policy definitions, their activation/publication proof, and
the remaining resource/provider, promotion and acceptance work listed above.

### Environment-owned keyed admission and cancellation (2026-10-01)

Keyed state admission now resolves an owned stage pin and selects the work
policy from that deployment's immutable workload settings. Caller policy fields
and revisions must match the deployed definition before any lane mutation. An
uninitialized or explicit empty collection cannot inherit production policies.
Desired edits or deletions do not change already deployed or admitted work.

Stage key and fairness digests include the immutable environment UUID in
separate namespaces. Existing production digests remain unchanged. Identical
application keys in production and two stages have independent sequences,
keep-latest replacement, running reservations, fairness limits and cancellation.
Account capacity remains shared. Raw application/fairness keys are not persisted.

Two operational ledgers record domain ownership and admission identity. Admission
includes the owning environment, pinned workload specification and settings hash,
policy revision, key/fairness digests and fairness limit. It commits with the
invocation and lane mutations. These ledgers are registered as operational state
and are never copied into a clone. PostgreSQL admission anchors creation, debounce
and expiry to one server clock rather than mixing server and transaction-start
timestamps. The sqlc schema snapshot now includes the established invocation work
columns used by these queries; all new SQL is generated through sqlc.

Claims reject missing or mismatched domain/admission records and scoped pins
that lack ownership. Delivery authenticates the admission against the selected
deployment's policy book and checks timing and scheduling metadata. Removing a
stage pin or work fields from an owned invocation cannot select production.
In-memory admission records and work envelopes are copied defensively. Retention
deletion removes the admission with its invocation, matching PostgreSQL cascade;
permanent app deletion removes its operational ownership.

`cancel-pending?environment=staging` now cancels only the stage namespace. It can
drain previously admitted work after deleting a desired policy, without looking
up a production policy. Running work continues. HTTP receipt keys include the
environment's immutable identity, and the state operation rechecks that selected
identity. Production's existing receipt identity and replay behavior remain
unchanged. Reusing an operation UUID for another state namespace conflicts, and
receipt replay never cancels subsequently admitted work. API documentation and
the generated Node/Python clients describe the scoped cancellation behavior.

Environment deletion and failed-clone compensation refuse owned work ledgers
until an isolated drain/cleanup transaction is implemented. API deletion reports
`environment_work_cleanup_unavailable` and retains the environment. Queue-source
work, event/broker producers and completion destinations remain blocked for
stages until their own bindings, consumers and counters have isolated ownership.
The public invocation APIs still select production; this step enables the pinned
keyed state path and scoped cancellation, not every stage producer.

MemStore and migrated PostgreSQL contracts cover production/sibling isolation,
policy mismatch before mutation, independent claims/fairness, pinned policy edits
and deletion, scoped cancellation/replay, defensive copies, malformed operational
ownership and cleanup fences. The scheduler fake dispatches keyed stage work to
its pinned stage deployment after desired policy deletion. Selected state, API,
scheduler and gateway regressions pass. Independent sqlc regeneration matches;
OpenAPI lint, Node build and 35 selected Python tests pass.

Full-copy publication and policy qualification remain closed pending complete
producer activation, cleanup, resource/data orchestration and promotion proof.
These local contracts do not establish provider checkpoint consistency or native
x86_64 KVM acceptance.


### 2026-10-01: invocation ownership and transactional stage work cleanup

Every newly admitted stage asynchronous invocation now stores the environment's
immutable UUID in `invocations.environment_id`. Production keeps the legacy NULL
owner. The owner is operational state, excluded from invocation JSON and clone
capture. The upgrade adopts existing keyed admission proofs and canonical,
owned revision/release pins; foreign release membership and ambiguous dual pins
are not adopted. The sqlc schema snapshot and clone column registry include the
owner and established key/fairness lane tables.

Admission authenticates and canonicalizes the stage pin before writing, then
rechecks ownership and the selected deployment inside the admission transaction.
Scoped ordinary/keyed admission, cancellation and both claim paths acquire an
environment KEY SHARE lock before lane, invocation or quota locks. Deletion and
clone compensation acquire the environment's exclusive lock first. A deletion
that wins that lock prevents late admission or cancellation from committing an
invocation, lane or receipt. Claims authenticate stored owner/pin identity before
state transitions or quota reservation. Delivery also reads the persisted owner,
so stripping the marker and routing headers from a passed work envelope cannot
select production.

After the existing live-deployment/protection guards, deletion validates work
ownership and removes the environment's pending/terminal invocations, admission
proofs, key/fairness lanes and domain ledger in its existing cleanup transaction.
Failed-clone compensation uses the same cleanup after its deployment and managed
secret guards. Ownership mismatches, cross-environment digest references and
unsupported broker/producer references fail before deletion. MemStore validates
all work before any map mutation. Cancellation receipts remain durable to fence
operation UUID reuse after deletion; recreating the same slug gets a distinct
HTTP receipt namespace because its environment UUID changes.

Any dispatching row or unreleased quota reservation blocks deletion, including
an expired execution lease. The normal invocation reaper must release the claim
and capacity first, or the worker must finish. API deletion returns 409
`environment_work_busy` for this case and
`environment_work_ownership_conflict` for inconsistent ownership. Idle owned
work no longer returns the earlier `environment_work_cleanup_unavailable`
placeholder. OpenAPI and generated Node/Python clients describe these outcomes.

Migrated PostgreSQL and MemStore contracts cover isolated cleanup of ordinary,
keyed, pending, superseded and completed rows, fairness/lane removal, sibling and
production sequence preservation, running-work recovery, retained receipts,
recreated-stage idempotency, damaged plain/keyed claims, migration backfill and
real PostgreSQL lock contention against deletion. Selected state, API,
scheduler and gateway contracts pass. Independent sqlc regeneration matches;
OpenAPI lint passes with warnings, the Node client builds, and eight selected
Python tests pass. These are the local verification gates for this increment.

Full clone publication and producer activation remain closed. Queue, cron,
broker and completion-destination bindings still require scoped ownership;
provider checkpoint consistency, complete resource orchestration, promotion,
and native x86_64 KVM acceptance remain required for the full feature.

### 2026-10-01: stage queue configuration and immutable clone capture

Queue definitions now have a complete environment-owned collection in the
immutable workload settings. It records logical binding and queue names,
delivery mode, workload class, enabled state, concurrency and retry policy.
It excludes runtime consumer IDs, leases, counters and messages. An absent
collection remains a legacy/uninitialized value; a complete empty collection
cannot inherit current production bindings. Collection edits share the workload
revision CAS and the environment protection transaction, including no-op writes.
The collection retains its clock after deleting its final definition. Reads for
a deployment use its pinned settings rather than the current desired head.

Atomic clone admission now captures queue definitions in the same repeatable-read
transaction as the selected artifacts, settings and other configuration. Legacy
production captures retain private source row IDs for later identity remapping;
stage-owned definitions retain logical names without production consumer IDs.
Both the policy and settings hashes cover the canonical queue definitions. A
source stage without an owned collection cannot adopt nonempty production queues.
Wrong account ownership aborts capture without creating a partial operation.

A leased queue catalogue reader authenticates the committed configuration root
and worker token before returning defensive copies. Subsequent production edits,
desired stage edits and live source ownership changes do not alter a committed
capture. Missing or damaged catalogue proof never falls back to the live source.
The sqlc schema snapshot now includes the established queue configuration table;
the new capture query is generated through sqlc.

MemStore and migrated PostgreSQL contracts cover isolated edits, stale revisions,
protected no-ops, canonical hashes, pinned deployment reads, complete empty
collections, frozen clone replay, lease authentication, foreign ownership and
damaged capture rejection. These establish configuration persistence and capture.
Public scoped queue editing, isolated consumer projection and message partitions,
admission/dispatch ownership and activation proof remain required. Qualification
and clone publication reject queue collections until that proof exists, including
complete empty collections. These contracts do not establish native runtime or
provider data-copy acceptance.

### 2026-10-01: scoped queue configuration API and CLI

Stage workloads expose GET and PUT at
`/v1/projects/{slug}/environments/{environment}/workloads/{workload}/queue-bindings`.
Both methods verify the account, project, registered stage and workload ownership.
The PUT body contains `expected_revision` for the complete workload settings head
and a required complete `bindings` array. An empty array clears definitions;
an omitted/null array is invalid. Protected stages and stale revisions conflict.
The response contains logical definitions, collection/workload revisions and the
configuration hash, without production binding IDs. An uninitialized collection
returns `environment_queue_collection_unavailable` and the workload revision in
`X-Gregale-Workload-Revision`, including zero for a new settings head.

The CLI exposes the same collection through:

```sh
gregale projects environments queues get shop staging shop-worker
gregale projects environments queues set shop staging shop-worker --file queues.json
```

`queues.json` contains the workload revision observed before editing and all
desired bindings, for example `{"expected_revision":4,"bindings":[]}`. The set
command also accepts stdin. It requires an explicit stage and revision and never
uses the production queue API. Existing deployments keep their pinned collection.
Responses and human output report consumer activation as unavailable; saving a
definition does not provision a consumer or enable qualification/promotion.

Legacy queue-binding CRUD, status and the simple queue-workload endpoint now
reject stage/ambiguous environment or scope queries before production access.
Create validates selection before idempotency reservation/replay, so a stage
refusal cannot poison a production receipt and a production receipt cannot answer
a stage request. Existing production receipt identity is preserved.

Contracts cover complete replacement and clearing, no-op/stale/protected writes,
foreign account/project ownership, function push validation, plan restrictions,
no accidental consumer creation, legacy selector/replay isolation and CLI routing.
OpenAPI and generated Node/Python clients describe the scoped API. The generated
CLI reference now includes queue commands and preserves the full-copy/status
commands through their source manifest rather than hand-edited generated text.
Full consumer isolation, data checkpoint orchestration and promotion acceptance
remain required for fully copyable stages.

Selected MemStore/migrated PostgreSQL queue, capture and qualification contracts
pass, including worker release membership with revision retention configured.
Selected API/CLI, scope, idempotency and generated-reference contracts pass.
OpenAPI lint passes with warnings, the Node SDK builds, and nine selected Python
SDK contracts pass. These local gates do not establish full native/provider
acceptance or complete configuration coverage.

### 2026-10-01: atomic preparation of owned queue consumers

A stage deployment can prepare its complete pinned queue collection through
`PrepareProjectEnvironmentQueueConsumers`. Preparation verifies the account,
project, registered environment, active workload, live deployment and immutable
workload settings. PostgreSQL locks environment ownership before app ownership
and the deployment; retries serialize on that deployment. The complete parent
record and all consumer rows commit together. An explicit empty collection gets
its own record, while an uninitialized collection cannot inherit production.

Each deployment and sibling environment gets fresh physical consumer IDs. A
retry of the same preparation returns the persisted IDs. Disabled definitions,
delivery mode, workload class, concurrency and retry policy are preserved. The
parent authenticates the complete collection revision, count, settings hash and
logical book hash; every consumer authenticates its definition. Reads reject
missing children, damaged definitions, unknown fields, altered hashes and
foreign ownership. A damaged persisted projection cannot be repaired implicitly
by re-running preparation. Later desired edits retain older deployment settings
and consumer identities, and the next deployment receives a fresh projection.

The new runtime-set and consumer tables are operational reset boundaries in the
clone schema registry. Production queue binding rows and triggers are untouched.
Environment deletion removes prepared consumers with the owned workload specs;
the existing live-deployment guard still applies. Ordinary workload edits now
preserve the owned queue collection, including explicit empty collections.

The only supported runtime-set state is `prepared`. Preparation does not admit
queue messages, activate a worker, reserve account capacity or establish queue
dispatch isolation. Qualification and clone publication remain unavailable until
message partitions, producer ownership, quota-aware claims, delivery and recovery
checks exist. The public queue API continues to report activation as unavailable.

Selected MemStore and migrated PostgreSQL contracts pass for owned/pinned
preparation, concurrent retries, complete empty collections, new deployment
identities, configuration edits, environment deletion locks, idle cleanup,
injected partial-write rollback and damaged/unknown-field rejection. Existing
queue configuration/capture, invocation ownership/cleanup and migrated schema
coverage contracts also pass. Independent sqlc regeneration matches the generated
files and `git diff --check` is clean. These local contracts do not establish
message dispatch, full configuration coverage, provider copying or native VM
acceptance.

### 2026-10-01: production queue consumer boundaries

Production queue triggers previously selected pending work by app and source.
That selection could claim a stage-pinned delayed task, and a production trigger
could hide the same task from the generic drain. Generic due lists and the drain
now retain environment-owned tasks regardless of production trigger definitions.
Their ordinary quota-aware claim and pinned deployment delivery paths apply.

Named and retained legacy queue pollers exclude environment-owned rows and owned
stage revision/release pins before the candidate/batch limit. The ownership
checks also apply to named claims, active production concurrency counts, partial
batch releases, retry, acknowledgement and dead-letter callbacks. Header matching
accounts for case, whitespace and supported UUID spellings. A missing ownership
marker cannot make an owned stage pin eligible for a production consumer.

The named claim takes the environment ownership lock before lane/row/binding
locks and verifies production eligibility again at row lock and update. All
changed poller, claim and due-list queries are generated through sqlc. A shared
typed invocation conversion preserves private environment ownership, quota and
work-lane metadata along with the persisted delivery envelope.

These boundaries do not activate prepared stage queue consumers or admit stage
queue messages. Their isolated message partition and consumer claim/dispatch
proofs remain required, along with complete resource and data-copy strategies.

MemStore and migrated PostgreSQL contracts verify that production triggers do
not hide stage delayed tasks, their quota-aware claims retain ownership, and
production named claims reject owned stage rows/pins without mutations. Poller
contracts cover delayed, named and retained unnamed queues, forged acknowledgement,
retry/dead-letter/release callbacks, preserved stage leases/account reservations,
and a 1,100-row stage backlog that must not consume the production candidate
budget. Existing keyed queue receipt, concurrency, fairness, paging, quota,
invocation ownership/cleanup, consumer preparation and generic drain checks pass.
The drain's simulated gateway/VM contract selects the pinned stage deployment;
this is not native VM acceptance. Independent sqlc regeneration and the whitespace
check pass. Complete stage queue dispatch and full cloning remain unverified.

## Deployment-owned stage queue message admission (implementation increment)

A private state-store admission path now requires an explicitly selected live
stage deployment and a previously prepared complete queue runtime set. It resolves
an enabled logical binding from the pinned workload specification, authenticates
revision or release membership, freezes the binding's retry settings, and writes
the message and its operational ownership record atomically. Generic enqueue
continues to reject shared producers pointing at stages. The internal admission
supports every valid worker/job pull/push and function HTTP push definition.

The ownership record retains environment, account, app, deployment, runtime set,
consumer, workload specification and configuration/definition hashes, together
with the server admission clock, canonical revision/release pin hash and logical
queue name. A different release graph reusing the same deployment cannot rewrite
a previously admitted message’s graph. Delivery and claim verify
that record against the complete pinned projection. Missing, stripped or changed
ownership, source, queue, retry settings, pins or hashes cannot be treated as
production or ordinary async work. These messages and records are operational
state and are reset at clone; they never become copied production messages.

Queue claims require the account-quota-aware path. Consumer concurrency counts
all dispatching or quota-reserved messages in the environment/app/logical queue
domain, including prior deployment generations and expired unreaped leases.
PostgreSQL performs that count after reserving the account quota row, whose lock
serializes competing claims across consumer generations. Any rejection rolls
back the reservation and message transition. MemStore provides the same atomic
checks under its mutex. Production and sibling environments have independent
consumer limits and share the account's existing total quota.

Idle environment deletion authenticates retained queue ownership and the complete
pinned projection before removing messages and dependent operational consumer
records. Retired deployments and expired release pins remain usable as cleanup
evidence; claims and delivery require live, usable pins. Busy reservations block
deleting the environment. Foreign or corrupted ownership blocks the entire
cleanup. The cleanup query fetches queue-owned IDs, rather than collecting all
stage message payloads in memory. MemStore copies envelope bytes and optional
fields so returned values cannot rewrite stored delivery intent or leases.

Public queue activation, class-specific consumer dispatch, clone qualification
and full clone publication remain gated. This increment supplies message
ownership and atomic concurrency admission, not complete stage queue execution
or the full resource/data-copy orchestration. Native VM/provider acceptance,
common database/object checkpoints, remaining resource isolation strategies and
production promotion/rollback still require completion.


MemStore and migrated PostgreSQL contracts cover atomic admission/rollback,
namespace ownership, frozen workload/retry/revision/release inputs, all valid
consumer classes/modes, fresh message identities across UUID spellings,
concurrent consumer/account admission, cross-deployment logical domains,
production/sibling separation, expired-lease recovery, completion/cancellation,
retired-deployment cleanup and corrupted ownership without partial mutations.
Existing consumer preparation, work-policy admission/lifecycle, environment
ownership/cleanup, due-list, quota, schema coverage, production queue poller and
generic drain contracts also pass. The final selected run completed state in
154.530 seconds and scheduler in 18.526 seconds. Independent sqlc regeneration
and the whitespace check pass. This evidence is local state/scheduler coverage;
full VM/provider acceptance and full clone capability remain outstanding.

### Production queue diagnostics and failed-event isolation

Legacy app-only queue readers now own production. The PostgreSQL
`production_invocation_work` view and the equivalent in-memory predicate exclude
persisted environment owners, private queue/work admission records, and owned
stage revision/release pins. Pin matching handles folded header names, compact
UUIDs, braces, URN prefixes and whitespace independently of release expiry.
Filtering precedes counts, page limits, cursor anchors and mutations.

This boundary applies to app/named-queue state, producer backlog admission,
replaceable work-lane counts, queue peeks, dead-letter pages, queue completion
notifications, acknowledgements, queue replay, and the pending-per-app rollup.
Queue replay also checks the URL's app before mutation. Completion notifications
are hints: both their predicate and the final payload read enforce ownership.
Returned in-memory envelopes do not alias stored payloads, leases or retry data.

Invocation-derived entries in the unified failed-event ledger have a durable
`environment_owned` marker. A database trigger records stage ownership when an
event is captured, preserves it on later updates, and the migration backfills
existing failures from source ownership or retained revision/release pins.
Source retention cannot turn an owned stage failure
into a production event. App/account reads, cursor anchors, individual and bulk
replay, and individual and bulk discard filter the production projection.
Global ledger retention still removes expired operational evidence without
changing source work. The schema registry records the new operational column.

Legacy queue/inbox and unified failed-event HTTP routes reject stage or
ambiguous selectors. Mutation guards precede idempotency lookup so an existing
production receipt cannot satisfy a stage-selected request. Handler extraction
keeps every changed HTTP handler within the repository's 50-line convention.

Prepared stage queue messages remain private and excluded from production
consumers. Public stage queue activation and delivery, the coordinated database
and object checkpoint, complete configuration/resource strategies, promotion
and rollback, and native VM/provider acceptance remain required for the full
one-command clone capability. These isolation changes do not qualify a stage
for promotion or open full-clone admission.

Verification for this increment: the final focused state run passed in 18.780
seconds against PostgreSQL 16 and MemStore. It covers shared queue names across
production/two stages, stage backlog filtering before limits, counts/oldest age,
leases and quota remaining unchanged by reads/rejections, defensive payload
copies, foreign queue and unified-ledger cursors, bulk replay/discard,
damaged owners/pins, deleted sources, migration rollback/reapplication/backfill,
legacy queue replay/state/peek and exhaustive schema-registry coverage. The
final API run passed in 1.612 seconds, including stage IDs/notifications,
selector validation before receipt replay, production app ownership, existing
queue/inbox APIs, project environment queue configuration, app/account unified
DLQ and dashboard failed-event operations. Production queue poller contracts
passed in 11.429 seconds and queue scaling contracts in 0.630 seconds.
Independent sqlc 1.31.1 regeneration matches checked-in output; the whitespace
check and changed-handler size check pass. These are selected local tests, not
a full-suite or native VM/provider acceptance claim.

### Stage queue producer depth admission

Private stage queue enqueue now enforces the account's current
`api.Limits.MaxQueueDepth` in an independent `(environment_id, app_id)` domain.
Every queue name and deployment generation shares that workload's depth; a
binding rename or new deployment cannot create another capacity allowance.
Production and sibling environments retain their separate backlogs. Free-plan
accounts cannot use private stage queue admission, and a plan change affects
subsequent admissions without rewriting pinned consumer definitions.

MemStore checks capacity under its existing mutation lock. PostgreSQL acquires
an environment `FOR NO KEY UPDATE` lock before app/deployment ownership locks,
then checks the domain and inserts the invocation plus private admission proof
in the same transaction. This serializes concurrent producers across deployment
generations while remaining compatible with the claim path's environment
key-share lock. Existing deletion locks still take precedence. SQLC queries
select the domain through its ownership indexes before counting.

Pending/dispatching rows and unreleased reservations retain their depth,
including expired leases that have not been recovered. Private admission
ownership remains authoritative when a row's mutable environment/source fields
are damaged; persisted invocation ownership also counts a queue whose admission
proof is missing. Over-capacity failures leave no row or proof and do not
reserve async claim capacity. Completion and cancellation free depth normally.
No new plan limit is introduced.

Worker/job delivery requires a pull consumer path: the existing request wake
and HTTP gateway reject workloads without listeners. HTTP function push uses
the request transport. Producer depth admission is a prerequisite for those
class-specific stage paths; public activation and full-clone qualification
remain unavailable until the entire runtime contract is wired
and verified. Preparation continues to cover all supported consumer classes.

Verification: the final selected state run passed in 71.973 seconds, covering
MemStore and PostgreSQL producer races across queue names/generations,
production/sibling separation, authoritative plan changes, expired leases,
terminal release, damaged ownership and no partial row/proof/quota writes.
Existing private queue ownership, class/mode preparation, consumer concurrency,
cleanup, production queue boundaries and account quota contracts also passed.
The API caller regressions passed in 1.869 seconds and scheduler queue/drain
regressions in 24.382 seconds. Independent SQLC 1.31.1 generation and the
whitespace check passed. Full suite and native VM/provider gates remain open.


### Private stage queue claims and delivery receipts

The private store transport now claims the next due message in one selected
stage deployment and pinned binding. It orders by due time, creation time and
message ID, excludes future messages and breached deadlines, and never searches
production or another stage/deployment. The adapter must name the pinned
transport mode. This supports the pending-message pull needed by worker/job
consumers and the same lease primitive for push adapters; preparation still
covers every supported consumer definition, including HTTP function push.
This is a store contract, not evidence of native worker/job execution or an
HTTP push runtime adapter.

Claim resolves the complete owned consumer book, authenticates the message's
private admission and selected pin, reads the current account plan/status and
abuse hold, and enforces the existing account and logical consumer concurrency
limits. Lease duration is positive and bounded by the account's existing async
invocation deadline limit. PostgreSQL takes environment/app/deployment ownership
locks first, then reserves the account counter before checking consumer
capacity across generations and claiming the message. The invocation transition,
reservation and receipt insert commit together. MemStore mirrors this under its
mutation lock. Empty reads and rejected claims leave no quota, receipt or attempt
increment.

Every delivery attempt returns a fresh cryptographically random 256-bit opaque
receipt. Only its SHA-256 digest is persisted, alongside the attempt, issue and
expiry times and a digest of the complete admission owner. Completion and retry
require this receipt in the same selected scope, a matching frozen consumer and
pin, the same current attempt and lease, and a live lease/deadline. An expired,
recovered, cancelled, wrong-scope, duplicate or previous-attempt receipt cannot
acknowledge or retry a new delivery. Retry uses the message's pinned policy,
clamped to the current account plan's finite attempt budget; each transition
away from dispatching releases its reservation once. Lease recovery consumes
that attempt budget too: the next scoped claim moves an exhausted pending
message to stage dead-letter without reserving quota or issuing a receipt,
and a subsequent read can consume the next message. Deployment retirement
stops claims while allowing an existing valid lease to finish.

Generic invocation discovery, claims and finish methods cannot bypass a receipt-owned
attempt. Their PostgreSQL receipt check observes the invocation row lock before
reading the receipt, and their mutations/counters roll back on rejection.
Receipt ownership survives damage or loss of the admission proof: the receipt
references the invocation directly, and production queue readers, rollups,
queue poller selection/claim/release/retry/finish and retained failed-event
ownership exclude receipt-owned work even when environment and pin fields are
lost. Receipt rows cascade with invocation retention and are explicitly
registered as operational state that resets at clone. Downgrading the receipt
migration is rejected while receipt-owned messages remain, preserving their
ownership and acknowledgement fence.

Public stage ingress/activation, consumer access credentials, all native
class-specific adapters and full-clone qualification remain gated. This change
does not enable the one-command full clone or claim an independent coordinated
PostgreSQL/object-storage checkpoint. Those runtime/provider and complete
configuration gates remain part of this ADR's full objective.


Verification: the state regression run passed in 161.299 seconds, covering
existing private queue admission/preparation/depth/concurrency/cleanup,
production queue boundaries, generic invocation completion/retry/quota, keyed
work lifecycle and migrated schema coverage. The final focused state run passed
in 31.768 seconds, including all five supported class/mode definitions,
scoped FIFO selection, future/deadline exclusions, concurrent claims and
completion, receipt rotation, retries and timeout attempt budgets, cancellation,
lease recovery, retirement, failure rollback, lost ownership, generic drain
filtering before page limits, migration rollback/reapplication and schema policy.
API regressions passed in 1.434 seconds and scheduler queue/drain regressions in
17.119 seconds. Final independent SQLC 1.31.1 regeneration matches checked-in
output and the whitespace check passes. These are selected local tests; full
suite, lint and native VM/provider acceptance remain open for the full feature.

### Deployment-scoped live environment reads

The guest metadata environment endpoint derives its deployment, app and account
from the accepted instance-bound Firecracker stream. Its existing `default`
request selector means the running deployment's values. It cannot select another
deployment or environment. Named production, legacy default and stage values
remain separate row sets, including when a selected row set is empty.

The store reads deployment ownership and plaintext environment values in one
PostgreSQL statement snapshot (or one MemStore critical section). App deletion,
failed/cancelled deployments, incompatible project ownership and broken pins
reject the read. Pinned deployments retain their original environment UUID in
`deployment_runtime_environment_owners`. This operational marker survives
environment/spec/pin deletion and cannot be rebound to a recreated environment
with the same slug. It cascades only when deployment history is removed. The
migration backfills existing pins and rejects rollback while owners remain.
Legacy project deployments without pins require an environment that predates
the deployment; they cannot adopt a later replacement stage.

Live environment reads no longer use an application-wide cache or rely on
notification delivery for isolation and freshness. Each request revalidates
ownership and reads the current selected values. An opaque content revision
includes deployment, scope and environment lifetime and changes when any key
is removed, including a key older than the latest modification timestamp.
The framing, request dispatch and secret-reload helpers are shared across build
targets so their protocol contracts run without KVM; Linux retains the native
Firecracker listener registration.

This increment covers the plaintext live environment endpoint. Secret lifetime
fences, boot/restore consumers and native stage queue adapters still require
integration and acceptance. It does not open public full-clone activation or
establish a coordinated database/object-data checkpoint.

Verification: the final focused MemStore/PostgreSQL state run passed in 67.386
seconds, covering scope separation, empty projections, ownership, stage
deletion/recreation, one-snapshot reads, lost pins, immutable rebinding, migration
backfill/rollback, deployment-history purge, schema coverage, queue receipts and
workload clone/promotion regressions. Shared runtime environment and existing
secret-reload protocol tests passed in 0.787 seconds. Independent SQLC 1.31.1
regeneration matches checked-in output, and the whitespace check passes. The
vmmd test binary cross-compiles for native Linux x86_64; it was not executed on
a KVM host. Full suite, lint, test-metal, leakcheck and VM/provider acceptance
remain open for the complete feature.

### Owned runtime values for boot and live secret reads

The runtime value reader now captures plaintext values, sealed secret inputs,
main/sidecar secret grants and reload opt-ins together with the deployment's
environment owner. PostgreSQL uses one statement snapshot; MemStore uses one
critical section and copies mutable byte slices and maps. It retains the
deployment/environment lifetime checks described above. Production, legacy
default and stage values remain distinct, including empty scopes.

Wake, snapshot priming, migration and app-task runtime preparation use this
snapshot for plaintext and main/sidecar secret selection. A failed or mismatched
read prevents boot rather than silently omitting configuration. Wake reads the
snapshot before reserving an instance, and derives its ephemeral-secret restore
policy from that same secret set. This removes the separate policy/payload
reads that could otherwise permit restore while supplying an ephemeral secret.
Sidecar declarations retain their immutable layer ordering and positive grants.

Live guest secret reads use the trusted instance's deployment/app/account tuple
to obtain the same owned snapshot. An old VM cannot adopt secrets from a deleted
and recreated stage with the same slug. Main and sidecar reload opt-ins are read
with the secret grant and sealed rows, preserving revocation behavior.

Secret delivery/reload observation writes still need transactional deployment,
environment-lifetime and sealed-envelope fences. Scope-specific park/reaper
policy, native stage queue adapters, the coordinated database/object-data
checkpoint and all full-clone activation gates remain open. This increment does
not enable public full-clone activation or establish native VM acceptance.

Verification: owned value/environment MemStore and real PostgreSQL contracts
passed in 69.309 seconds, including scope/grant isolation, defensive copies,
MVCC visibility, deletion/recreation, lost pins and retained lifetime owners.
The scheduler gate passed in 5.970 seconds, covering wake/prime, migration,
app-task runtime preparation, coherent main/sidecar values under rotation,
ephemeral-secret restore policy, failed-read lock release/retry and real
PostgreSQL restore-pressure selection. Guest runtime environment/secret protocol
contracts passed in 0.846 seconds, including an actual MemStore stage
deletion/recreation. API clone/queue/workload regressions passed (API 0.550
seconds, apid 1.246 seconds). Independent SQLC regeneration matches checked-in
output and the whitespace check passes. The additional Linux x86_64 test-binary
cross-compile ran out of host disk space; this increment has no completed native
build/VM evidence. Full suite, lint, test-metal, leakcheck and provider acceptance
remain open for the complete feature.

### Transactional secret reload and acknowledgement ownership

Guest projection/signal reports and application acknowledgements now carry a
host-generated fence from the owned runtime snapshot. The fence fingerprints
the deployment, environment lifetime, sealed secret book and main/sidecar
grants, excluding mutable delivery observations and plaintext values. Secret
creation time distinguishes a deleted/recreated row even when its envelope and
delivery version are copied back unchanged.

Guest revisions use the same owned identity and the selected secret rows,
including their creation lifetime and grant configuration. They also include
the workload identity. A delayed guest revision cannot acquire a fresh host
fence by matching the copied envelope of a replacement row. Changes to an
unselected secret do not change the guest revision, while the host's complete
book fence still detects intervening mutations during a write.

Both stores revalidate the fence in the observation write transaction/critical
section, including empty projections. PostgreSQL locks the environment before
app/deployment ownership, retained pins, reload opt-ins and sorted secret rows.
Main summaries, per-runtime observations and acknowledgements use SQLC queries
and roll back together on conflict. Revocation acknowledgements are limited to
the instance's exact scope. Historical unfenced callers are accepted only for
unowned production/default deployments; a stage or retained ownership marker
requires a fence even after its current pin disappears. Stopped instances cannot
submit a new owned reload acknowledgement.

MemStore secret deletion now cascades per-runtime observations everywhere,
matching PostgreSQL's scoped secret foreign key. Stage deletion/recreation,
managed credential cleanup and clone compensation cannot attach old
observations to a replacement secret with the same key and version.

Boot/start secret delivery summaries still require these transactional fences.
Native stage queue adapters, scope-specific lifecycle/reconciliation, coordinated
PostgreSQL/object data capture, complete configuration strategy coverage and
full-clone activation/qualification remain open. This increment does not enable
the public one-command full clone or establish native VM/provider acceptance.

Verification: the expanded MemStore and real PostgreSQL gate passed in 75.250
seconds, including owned snapshot/lifetime contracts, transactional report/ack
fences, a PostgreSQL writer observed waiting for an environment deletion,
empty revocation acknowledgements, per-workload observations, managed secrets,
object credentials and clone/deletion/rollback regressions. Guest protocol
contracts passed in 1.109 seconds; API/apid clone, queue, workload and secret
regressions passed in 0.693/1.780 seconds. Independent SQLC 1.31.1 output matches
the checked-in files, and the whitespace check passes. The vmmd test binary
cross-compiles as an x86_64 Linux ELF; it was not executed on KVM. Full suite,
lint, test-metal, leakcheck and provider acceptance remain open.

### Owned boot secret delivery summaries

Wake and snapshot priming now carry the host-generated sealed configuration
fence from their single owned runtime snapshot through the unlocked VM start
window. Main and sidecar delivery candidates share that same complete-book
fence. Completion does not acquire a newer snapshot to authorize an older
payload. The store requires this fence for every boot delivery write.

Both stores validate the deployment, original environment lifetime, sealed
envelopes and grant configuration again in the write transaction/critical
section. They also require the exact instance's current wake ID. PostgreSQL
locks the instance attempt, and the entire candidate batch is checked before
any summary is changed. Rotation, resealing, identical-envelope recreation,
stage recreation or a changed wake attempt rejects the old completion without
attributing it to replacement inputs.

A successful delivery requires an instance that still holds runtime resources.
Failure summaries also accept the same attempt after its instance becomes
FAILED; STOPPED instances cannot write a new boot summary. A later failure
cannot downgrade an already delivered version. Failure reasons use the closed
`runtime_start_failed` code. Delivery writes use SQLC; its schema snapshot now
includes the existing delivery columns from migration 20260922174018492,
without adding a new database migration.

This increment covers delivery observations, not authorization to activate the
complete-clone feature. Native stage queue adapters, scope-specific lifecycle
and reconciliation, coordinated PostgreSQL/object capture, remaining resource
strategies, complete activation and qualification, and native VM/provider
acceptance remain open.

Verification: the final expanded MemStore/real PostgreSQL gate passed in
122.873 seconds, covering boot and reload fences, same-envelope recreation,
version-preserving reseals, rejected partial batches, current failed attempts,
stopped attempts, exact wake IDs, deletion/recreation, a boot writer observed
waiting for an environment deletion, managed secrets, object credentials and
clone/deletion/rollback regressions. Scheduler wake/prime, main/sidecar delivery,
migration and app-task regressions passed in 1.179 seconds; API secret and clone
regressions passed in 1.634 seconds; guest runtime protocols passed in 0.803
seconds. Independent SQLC 1.31.1 generation matches the checked-in output and
the whitespace check passes. Scheduler and vmmd test binaries cross-compile
as Linux x86_64 ELF files; neither was executed on KVM. Full suite, lint,
test-metal, leakcheck and provider acceptance remain open.

### Owned snapshot parking and isolated production pools

Parking now checks the deployment's owned runtime values when evaluating
ephemeral secrets and at each existing capture freshness boundary. A deleted
or recreated environment cannot supply policy for an older VM. Migration and
paused restore share the owned configuration snapshot used to construct their
payload; paused restore checks that configuration again after the VM RPC and
rejects changed plaintext, sealed values, grants or environment ownership.

Production warm-pool reconciliation resolves the selected production/default
release and its pinned settings before computing its target. Sibling stage
rows neither satisfy that target nor enter production cleanup. Obsolete
production releases and incompatible RAM/mode rows are retired; ephemeral
production values retire only production paused capacity. Warm promotion reads
owned inputs before resuming, rejects recreated stage ownership and retires
selected incompatible or ephemeral candidates without touching sibling pools.

Periodic recovery uses a conservative bulk SQLC candidate read for shared
positive targets, live production deployment pins and retained paused rows,
filtered by scheduler ownership. A zero shared target cannot hide a positive
pinned target or cleanup after a missed notification. A failed candidate read
falls back to reconciling the owned app list. Idle paused-pool counts are now
separate for each deployment and use its pinned target, preventing production
recovery from being undone by the subsequent idle pass.

These checks are not transactional runtime or snapshot publication fences.
Delayed imaged publication, changes after a paused restore recheck, stored
paused-payload revisions, scope-specific configuration stamps and serving
floors/billing remain open. Native stage pool fills, remaining configuration
and customer-data strategies, full-clone activation/qualification and native
VM/provider acceptance are also unfinished; the public one-command full clone
remains unavailable.

Verification: scheduler parking, prime, migration, boot-delivery, warm-pool,
promotion and reaper regressions passed in 1.425 seconds. The shared MemStore
and real PostgreSQL candidate contracts passed in 3.571 seconds, including
pinned targets after desired-head edits, retained stage rows, deleted apps,
superseded production pins and scheduler node ownership. Independent SQLC
1.31.1 generation matches the checked-in output and the whitespace check
passes. The scheduler test binary cross-compiles as a Linux x86_64 ELF; it was
not executed on KVM. Full suite, lint, test-metal, leakcheck and provider
acceptance remain open.

### Snapshot publication retains original environment ownership

The sole snapshot-row writer now validates the original deployment environment
inside the insertion transaction/critical section. PostgreSQL discovers the
owner without taking locks, then locks that exact environment before the app,
deployment and immutable pins. It rechecks the owned runtime environment after
locking, matching environment deletion's ordering and retaining serialization
with the existing configuration-change stamp trigger. Deleted or recreated
stage ownership cannot publish a delayed warm or init capture.

Stage notifications require a matching source instance. Its current start
time must equal the captured start time, so an earlier runtime cannot publish
after the same row has started again, even without a config-change stamp.
Historical production/default notifications remain accepted without a source
only when no app configuration-change stamp exists. New owner, source and
stamp reads/locks use SQLC. Imaged's retention-policy reads use the same owned
runtime values instead of a slug lookup. Failed/cancelled snapshot attempts
acknowledge redelivery before reading runtime values, preserving referenced
artifacts and avoiding repeated smoke attempts.

Verification: the expanded MemStore/real PostgreSQL gate passed in 23.448
seconds, including original and replacement lifetime publication, sibling
source rejection, exact source start times, existing config-stamp fences,
owned runtime reads/pins and warm-pool candidates. One PostgreSQL test observes
the snapshot writer blocked by the specific environment-deletion transaction
before committing it and asserting rejection. Focused imaged publication,
activation, redelivery, ephemeral policy and artifact-cleanup regressions
passed in 1.360 seconds; scheduler regressions passed in 1.597 seconds.
Independent SQLC 1.31.1 output matches, the whitespace check passes, and the
scheduler cross-compiles as a Linux x86_64 ELF without KVM execution.

The full imaged suite still fails
`TestHandleNotification_AppChanged_Deleted_CarriesAppID` and
`TestRunGCTick_PerEvictionSQLCount`. Both reproduce with an unmodified
`a9dca5680` source overlay; artifact lifecycle/GC integration remains open.
An earlier expanded state run exhausted host disk during migration setup;
the 23.448-second rerun passed after superseded task cache archives were freed.
Captured configuration revisions, scope-specific change stamps and GC/floors,
transactional paused runtime publication, native stage adapters, coordinated
customer-data capture, complete strategies and activation/qualification, full
suite/lint and test-metal/leakcheck/provider acceptance remain unfinished.


### Snapshot retention follows environment lifetimes

Snapshot GC now groups rollback generations by app and original environment
UUID, using normalized production/default scope only for genuine legacy
rows without an environment. Each deployment's immutable warm-snapshot policy
controls its tiers, so desired-head or shared-App edits cannot alter retained
stage snapshots. Normal and pressure cleanup use the same generation policy;
account byte totals still span the account's stages. Lost original owners,
missing pins and terminal deployments are cleanup debt and do not consume a
healthy rollback slot. Deleted/recreated slugs cannot acquire old snapshots.

The normal, stale-retention and pending-deletion selectors carry original
ownership, pinned policy and exact physical rootfs keys through one SQLC join
or MemStore critical section. The schema snapshot now includes the existing
snapshot deletion tombstone column. Pending retries have no age cutoff,
including when a snapshot timestamp is ahead of the process clock. The GC
loop no longer reloads deployments per eviction and still uses durable layer
deletion claims: a shared stage image survives until its last retained
reference is gone. App cleanup checks authoritative deletion status before
removing snapshot captures, protecting active apps from replayed delete hints.

Verification: the focused MemStore and real PostgreSQL GC/retention gate
passed in 15.456 seconds. It covers pinned policy after both desired-head and
shared-App edits, all three selectors after stage recreation, lost owner/pin
records, age-fenced legacy ownership, future tombstone retries, and existing
slug/tier/storage-key/stale-retention contracts. The full imaged suite passed
in 6.039 seconds, including shared physical-layer retention and final deletion,
per-stage windows, pinned tiers, original lifetime separation and active-app
artifact protection. The two previous full-imaged failures are resolved:
the delete notification fixture now records the actual app deletion, and
physical rootfs metadata avoids per-eviction deployment lookups. Independent
SQLC 1.31.1 regeneration matches and the whitespace check passes. An earlier
expanded state run hit host disk exhaustion during fixture migration; the
focused rerun passed after superseded task archives were freed.

This closes snapshot retention isolation, not the complete clone contract.
Scope-specific capacity/admission/reaper floors and change stamps, captured
configuration publication fences, transactional warm publication, remaining
native adapters and resource strategies, coordinated customer-data capture,
full-clone activation/qualification and production-shaped acceptance remain
open. The public one-command complete clone is still unavailable. Full-suite
and lint gates plus native x86_64 KVM test-metal, leakcheck and provider
acceptance have not been completed.


### Idle reaping uses pinned environment policy

The native scheduler reaper resolves workload settings and original environment
ownership once per deployment on each tick. Running replicas contribute only
to their own environment lifetime's minimum floor; retained deployment
overrides are folded into that lifetime's floor. Default/production normalize
for legacy rows. Paused pools remain tied to the exact deployment payload.
Idle timeout, worker exemption, warm-pool target, scale-in cooldown duration
and eviction priority now come from the deployed pin rather than the mutable
shared App projection. Missing policy or deleted/recreated ownership cannot
authorize idle or aggressive scale-in. Every instance's app/account is checked
even when its deployment policy is cached.

Production prewarm windows apply against the pinned runtime floor, including
when the desired App floor is larger, and never protect sibling stage rows.
A stage-only idle park does not write the production scale-in timestamp or
emit a production floor-release audit. The existing production load signal
cannot authorize aggressive stage scale-in. The pure selector accepts an
explicit environment load key; a native scoped stage load feed remains to be
implemented before the full clone contract can support that behavior.

Verification: the final full scheduler package passed in 42.061 seconds on
macOS; the focused reaper/ownership/warm-pool gate passed in 1.070 seconds.
New native loop tests wake pinned production and stage generations, mutate
desired/shared settings, and verify independent minimum floors and idle
timeouts, stage-only scale-in stamp isolation, and production prewarm against
a retained pin. Pure and store-backed policy tests cover recreated lifetimes,
transient read failure, explicit stage load selection, worker/priority policy
and cached ownership checks. Existing conntrack, active-request, tail-task,
minimum-floor, cooldown, warm-pool and lifecycle regressions pass. VM calls
use the scheduler test fakes; this is not KVM or provider acceptance.

The idle floor and runtime policy selection are isolated, but admission caps,
minimum replica fills, billing, load/prewarm feeds, shared scale-out/cooldown
telemetry, debugger/audit aggregation and stage lifecycle/resource cleanup
still require environment ownership. Captured configuration and warm
publication fences, remaining configuration/resource strategies and native
adapters, coordinated customer-data capture and full-clone activation and
qualification also remain open. The public complete-clone command remains
unavailable. Repository-wide tests/lint and native test-metal, leakcheck and
provider acceptance remain unverified.

### Scaling history belongs to the original environment

Operational scale-in and scale-out clocks are now persisted per app and
original environment UUID. They reset for a cloned or recreated environment;
deployment generations in one lifetime share them. A stage never reads or
writes the production App clock projection. Pinned default/production aliases
share their production environment, and genuinely unpinned legacy aliases
share a production scope key. PostgreSQL legacy runtime-value ownership now
matches MemStore for those aliases, without inferring a production UUID from
a desired environment created independently of the deployment.

Stamp writers lock the original environment, app, deployment and retained pin,
then recheck ownership before publishing history. Lost pins or owners,
terminal deployments and deleted/recreated environments reject writes.
Environment deletion removes its operational clock rows. Production-only
compatibility stamps update existing production rows and the App projection.
Ordinary native wake admissions consult and stamp the actual deployment's
history. Idle and aggressive reaping use environment clocks and stamp the
actual parked instance's deployment once per environment.

Verification: the focused scheduler gate passed in 1.026 seconds and the full
scheduler package passed in 18.020 seconds on macOS. The combined MemStore and
real PostgreSQL scaling, runtime-value ownership, snapshot-publication and
clone-schema coverage gate passed in 35.472 seconds. Tests cover independent
stage/production cooldowns, retained history across generations, fresh
replacement history, compatibility aliases/stamps, caller pointer isolation,
foreign identities, lost owner/pin rejection and a PostgreSQL writer blocked
on deletion of its original environment. Independent SQLC 1.31.1 regeneration
matches; the whitespace check passes. Scheduler VM calls use test fakes.

Admission counters and their cold-start discriminator still use app-wide
concurrency. Explicit deployment reconciliation retains its existing gate
bypass. These paths, minimum/warm replica fills, load/prewarm feeds, billing,
debugger/audit aggregation and stage resource cleanup still need environment
ownership. Captured configuration/warm publication fences, remaining native
adapters and configuration/data strategies, coordinated customer-data capture
and full-clone activation/qualification remain open. The public complete-clone
command remains unavailable; repository-wide tests/lint and native x86_64 KVM
test-metal, leakcheck and provider acceptance remain unverified.

### Serving counts and startup recovery retain environment ownership

The scheduler ledger now retains an authenticated original environment key
alongside each instance reservation. Deployment generations share a serving
count only within that lifetime. Admit, snapshot transition, warm promotion
and release maintain it under the existing ledger mutex; paused/primes/tasks
continue to consume resident node capacity without serving concurrency.
Legacy production reservations retain their compatibility key.

Ordinary admission uses its own environment's count for cooldown cold-start
bypass and the no-signal minimum-floor decision. Production running replicas
therefore cannot hold a parked stage on cooldown or satisfy its no-signal
floor. The scaling-history and runtime-value reads must agree on the original
lifetime before a new VM is admitted; reaping also checks that agreement.
The actual selected deployment's scaling history is read once on ordinary
admission, avoiding the previous redundant read during logical resolution.

Startup recovery rebuilds original environment and deployment counters from
resident rows, caches policy per deployment, and restores CPU sizing from the
deployed pin. Already-resident over-cap rows remain accounted for through a
recovery-only concurrency bypass; subsequent normal admits still see the full
count. A deleted original stage is assigned an orphan deployment key, so its
resident RAM stays charged without contributing to a replacement lifetime.
Transient ownership failures abort startup instead of adopting a fallback.
Warm fills and missing-reservation repairs retain the owned environment key.

Verification: the focused environment/lifecycle/recovery gate passed in 1.197
seconds and the final full scheduler package passed in 18.318 seconds on
macOS. Contracts cover a stage cold-start beside running production, scoped
no-signal floors, cross-generation counts, snapshot/warm/release lifecycle,
recreated lifetime separation, retained orphan capacity, pinned CPU recovery,
over-cap recovery and transient ownership failures. A concurrent property
test matches environment counts to successful admissions, preserves the shared
plan cap and checks complete counter/RAM release. Existing literal-ledger
callers are supported through lazy index reconstruction. VM calls remain
test fakes; no native KVM/provider acceptance is claimed.

The configured concurrency ceilings and rollout grants still use aggregate
app counts; their environment policy is the next admission requirement.
Explicit deployment reconciliation retains its existing gate bypass. Scoped
minimum/warm fills, load/prewarm feeds, billing, debugger/audit aggregation,
resource cleanup, captured configuration/warm publication fences, remaining
adapters and clone strategies, coordinated data capture and full-clone
activation/qualification remain open. The public complete-clone command
remains unavailable. Repository-wide tests/lint and native test-metal,
leakcheck and provider acceptance remain unverified.

### Environment serving ceilings within the shared plan budget

Native admission now checks the selected deployment's environment serving
count against its deployed configured ceiling. Every environment still shares
the app's plan concurrency budget and the node's physical RAM, vCPU and CPU
limits. Desired configuration changes do not replace a deployed stage's limit.
The existing rollout allowance requires a serving generation in that same
environment; a new sibling stage cannot borrow production's allowance. Even
simultaneous environment rollouts remain bounded by the shared plan ceiling
plus one slot. Warm promotion and missing-reservation repair use these same
boundaries and retain the original owner. Public limit errors retain the
existing steady-plan ceiling and stable error code.

Valid pinned production and genuinely unpinned legacy production share the
production compatibility count, while retaining distinct original ownership
indexes. Stages and orphaned reservations do not contribute to it. Production
scale-in/out writes now synchronize existing production clock rows with the
App projection in the same PostgreSQL transaction, preventing legacy and
pinned generations from maintaining conflicting cooldown histories. Stage
history remains independent.

Verification: the focused capacity gate passed in 1.166 seconds. MemStore and
real PostgreSQL runtime-scaling contracts passed in 17.074 seconds, including
alternating legacy/pinned production clock writers and independent stage
history. The final full scheduler tree passed: scheduler 18.132 seconds and
all eight child packages. Tests cover independent deployed limits, shared
plan exhaustion, same-environment rollout prerequisites, concurrent rollout
bounds, warm promotion, recovered over-cap stage reservations, and mixed
legacy/pinned production. Independent SQLC 1.31.1 generation matches and the
whitespace check passes. VM operations use macOS test fakes.

Explicit deployment reconciliation retains its existing ordinary gate bypass.
Coordinated wakes, minimum/warm fills, load/prewarm feeds, billing, debugger
and audit aggregation, resource cleanup, captured configuration and warm
publication fences, remaining adapters and clone strategies, coordinated
customer-data capture and full-clone activation/qualification remain open.
The public complete-clone command remains unavailable. Repository-wide
tests/lint and native x86_64 KVM test-metal, leakcheck and provider acceptance
remain unverified.

### Coordinated wakes retain deployed environment and boot ownership

The engine selects an actual deployment and its original environment before
entering the wake coordinator. Production and stage requests cannot share a
leader outcome, and a replacement deployment or recreated stage cannot join
an old boot. Detached leaders preserve that selection. Native burst siblings
recheck it through ordinary admission, without taking the explicit-deployment
gate bypass. A changed selection refuses a continuation instead of admitting
on another generation. Initial burst and prewarm targets use only the selected
environment's serving count; the shared plan and physical node budgets remain
authoritative in every admission. Fan-out resolves deployed settings, replacing
the mutable App policy cache. This adds policy/ownership reads to coordinated
entry; its production latency has not been benchmarked.

App deletion forgets all of the app's deployment coordinator entries without
touching another app. Original stage lifetime checks also run on the existing
instance fast path. The current EnsureWake RPC remains production-only;
preview and exact-deployment gateway routes still use their scoped admission
path. Internal engine callers can use an explicitly scoped context.

Replacement testing exposed that a boot with no secret deliveries could
publish RUNNING after its original environment disappeared. Native wake and
prime now use a mandatory host-authored publication fence even for an empty
secret set. MemStore validates ownership and updates runtime/state under its
mutex. PostgreSQL locks the original environment, app, deployment, instance,
retained configuration pins and sealed inputs through a SQLC runtime/state
CAS. It verifies the exact app, deployment, node, wake ID and provisional
state. Lost owners/pins, recreated lifetimes and changed sealed inputs reject
publication. A failed native boot destroys its VM, releases the ledger and
retires its still-owned provisional row, preserving another reconciler's
state. Priming uses the same publication boundary and transition observations.

Verification: the focused wake/coordinator gate passed in 1.444 seconds before
the prime extension. The final full scheduler tree, including prime and
secret-rotation contracts, passed: scheduler 37.911 seconds and all eight
child packages. Real PostgreSQL and MemStore publication contracts passed in
8.358 seconds; a writer was observed blocked on deletion of its original
environment, then rejected after replacement committed. Contracts also cover
empty sealed inputs, wrong identity/epoch/node, lost pins, desired edits that
preserve deployed settings, retained readiness/request observations and
replacement/production independence. Schedd daemon tests passed in 1.206
seconds. The related gRPC and gateway suites passed in 1.034 and 50.724 seconds
before the prime extension; that broader run failed while linking scheduler
packages because the host disk filled. The final scheduler gate supersedes
those failed links. Independent SQLC 1.31.1 generation matches and whitespace
checks pass. VM operations remain macOS fakes; native KVM/provider acceptance
is not claimed.

Stage-native warm fills, producer load/prewarm feeds, floor aggregation,
captured plaintext/configuration publication fences, transactional WARM
publication, billing/audit/resource cleanup adapters, remaining configuration
and resource strategies, coordinated PostgreSQL/object-data capture and
full-clone activation/qualification remain open. The public complete-clone
command remains unavailable; repository-wide tests/lint, native test-metal,
leakcheck and provider acceptance remain unverified.

### Warm pools reconcile deployed stages and publish their original ownership

Scoped warm-pool reconciliation now selects an environment's active release
graph before traffic-bearing direct deployments. An unscoped call retains
production selection, including its legacy default lane. Dark direct rows
and desired workload heads cannot select a pool. Each selected deployment
supplies its immutable settings and original environment lifetime; sibling
paused rows cannot satisfy its target or enter its cleanup set. A proven lost
owner is retired through its physical instance, while an unavailable owner
read fails closed. Recreating a stage does not adopt its former paused VMs.

The notification and periodic paths discover every live environment plus
retained paused rows. Bulk candidate selection includes positive stage pins,
even when the shared App or the current desired head requests zero. All
environment fills in an app pass share the existing four-restore budget and
the physical node ledger. Paused capacity reserves RAM/vCPU/CPU without
consuming serving concurrency. A failed environment policy read does not
prevent another environment's recovery. Snapshot-less targets remain pending;
warm-pool filling does not cold boot.

Owned runtime publication now supports WAKING to WARM and WARM to RUNNING,
alongside ordinary wake and prime. It requires the captured deployment and
original environment fence, node, wake ID and expected state. Runtime identity
and lifecycle state are committed by the same SQLC CAS or MemStore mutex
operation. Restore and resume use this boundary; changed sealed inputs or a
lost environment detected at publication reject the transition. Cleanup uses
the expected provisional state and preserves another state writer. Conditional
terminal writes now stamp their retention anchor in that same SQLC update.

Verification: the focused scheduler pool gate passed in 2.175 seconds. Final
MemStore and real PostgreSQL contracts passed in 23.852 seconds, covering
stage/direct/dark graph selection, positive deployed pins after desired edits,
owned paused publication, replay and identity rejection, original-environment
deletion locks, sealed-input changes and conditional terminal retention.
The full scheduler tree passed: scheduler 56.466 seconds and all eight child
packages. Schedd daemon tests passed in 56.415 seconds. Contracts cover pinned
shape and scoped values, shared fill budgets,
periodic recovery, disable cleanup, recreated lifetimes, temporary ownership
failures, changes after the restore read and changes during warm resume.
Independent SQLC generation matches and whitespace checks pass. VM calls use
macOS fakes; this is not native KVM or provider acceptance.

The following section closes the plaintext publication and paused-runtime
configuration proof gaps identified at this step. Ordinary scoped wake
selection still has its earlier direct-deployment fallback and
needs alignment with graph selection where applicable. Producer load/prewarm
feeds, floor aggregation, billing/audit/resource cleanup, remaining adapters,
all configuration/resource clone strategies, coordinated isolated PostgreSQL
and object-data capture, durable one-command orchestration and qualified
promotion/rollback remain open. Public complete cloning stays unavailable.
Repository-wide tests/lint, native x86_64 KVM test-metal, leakcheck and provider
acceptance remain unverified.

### Runtime publication retains captured environment configuration (2026-10-01)

The owned runtime snapshot now includes the deployed workload specification,
its validated settings, deployment artifact inputs and built sidecar layers.
PostgreSQL reads these with scoped variables and sealed secrets in one statement;
MemStore uses one mutex window. Boot builders compare their loaded App and
Deployment inputs against that snapshot and use its sidecar layers directly.
Desired revision heads and delivery observations do not enter the fingerprint.
Unknown or hash-mismatched pinned settings fail closed. Empty App collections
with equivalent runtime semantics are normalized for SQL/JSON hydration; owned
work and queue pointers retain their explicit-empty behavior.

Wake, prime and paused restore require a configuration fingerprint at runtime
publication. It covers plaintext keys/values and row lifetimes, sealed inputs,
deployed settings, artifacts and sidecar configuration. The original environment,
app, deployment and retained specification remain protected through the instance
CAS. Variable/secret writers serialize on the app; deployment child writers
serialize on their deployment, including insertion into an empty set. Publication
reads committed value rows without taking tuple locks that could wait behind a
writer already blocked on those parents.

Publication stores the captured fingerprint with the physical instance, wake ID
and node in the same transaction as runtime identity and lifecycle state. A warm
resume must match that stored capture and the current owned inputs. Supplying a
new fingerprint cannot bless an old paused VM. Reconciliation retires stale or
unproved paused rows; promotion checks before the resume RPC and rechecks during
its atomic WARM-to-RUNNING publication. Missing historical proofs are not backfilled
from current configuration. Instance retention deletes its proof as well.

Focused MemStore and real PostgreSQL contracts pass through an isolated harness
containing eleven unchanged repository contract files and the original pool
fixture, importing the actual working-tree Store implementation. They cover
plaintext insertion/edit/deletion/recreation, sibling isolation, artifact and
sidecar changes, corrupted settings, durable proof reconstruction and refusal to
adopt new values. Blocking-query observations prove writer/publication ordering
in both directions, including secret, variable and sidecar tuples whose writers
wait on their parent. Legacy contracts also verify actual route, CIDR, egress-port and retry inputs,
including mutable App settings for deployments without a pin. The final focused
harness passed in 73.081 seconds. The full scheduler tree passed (scheduler
18.945 seconds and all eight child packages); schedd daemon tests passed in
1.042 seconds. These include missing/stale paused proofs before resume, edits
during resume and after the restore read, and production/stage isolation.
Independent SQLC generation matches and whitespace checks pass. The full state
package binary hit local disk exhaustion before linking; this harness is focused
evidence and does not stand in for the full state suite.

The global runtime-config timestamp and snapshot publication/cache invalidation
still require environment qualification; a stored VM proof is not a complete
snapshot lineage or migration handoff protocol. Ordinary scoped wake graph
selection, remaining resource/configuration adapters, coordinated isolated
PostgreSQL/object data capture, one-command orchestration, full qualification and
promotion/rollback remain open. Public complete cloning stays unavailable.
Repository-wide tests/lint, native x86_64 KVM test-metal/leakcheck and provider
acceptance remain unverified.

### Durable copying coordinator and target materialization (2026-10-01)

apid now polls the durable clone queue and drives an authenticated configuration
capture through database restoration, version-manifest object copying, fresh
PostgreSQL bindings and object credentials, target configuration materialization,
dark deployment creation, schedd prime notifications and publication checks.
Successful resource checkpoints retain the current operation revision for the
next step and lease release. Provider calls retain their lease deadlines. Errors
and readiness waits release the claim with a delayed retry; an unknown commit
outcome waits for lease expiry rather than adopting another worker's revision.
The worker logs stable reason codes without arbitrary provider error text.

The coordinator checks the frozen workload/configuration inventory and common
database/object capture point before provider copying. Pending/capturing work
still returns `data_checkpoint_unavailable`; it does not choose a wall-clock
point and call it a coordinated checkpoint. Compensation remains explicitly
unavailable. These phases do not materialize a target or publish a release.

A private materialization receipt commits in the same transaction as the target
configuration. It retains the original environment UUID and each workload's
original desired specification identity and hash. A retry reuses that lifetime
without recopying configuration. It rejects an edited desired head, corrupted
settings, or a deleted/recreated environment with the same slug. Deletion cannot
cascade away the receipt's original environment identity. Lease authority is
rechecked after lock waits before both first materialization and replay commit.
Publication also checks the receipt before app/resource locks and in its final
graph transaction.

Copying-phase checkpoints now allow `copying` and `verifying` resource statuses
while waiting for providers or schedd. Entering copying still requires captured
or ready resources. The resource set and captured source/version identities
remain immutable, and assigned target identities cannot be replaced. The new
integration path exposed this distinction: the previous gate rejected every
pending deployment's durable readiness checkpoint.

The schema registry now includes this operational receipt and the prior runtime
instance configuration proof table. Real migrated-schema capture had previously
rejected that unregistered runtime table; registering it does not assert a
customer configuration copy strategy or open complete admission.

Verification: the new coordinator/inventory/checkpoint tests and five real
PostgreSQL coordinator contracts pass (12.293 s), including committed-but-lost
materialization acknowledgement, failed prime handoff, worker takeover,
independent shared-database/bucket preparation, original object-version copying,
no duplicate targets, mixed capture points rejected before provider copy,
partial workload readiness, developer edits, environment recreation and delayed
checkpoint waits. Existing API clone worker/admission regressions pass (6.525 s).
Four unchanged state test files also pass against the working-tree store through
a task-owned focused runner (5.033 s): MemStore/PostgreSQL resource publication
fences, migrated-schema coverage and atomic configuration capture. This runner
is not the full state suite. Independent sqlc regeneration and `git diff --check`
pass.

The tests use real PostgreSQL for control-plane transactions and fake database
and object providers. Complete-mode admission remains closed. Even fully primed
targets stay in copying when independent data-resource or work-policy activation
proofs are unavailable; the coordinator does not bypass those existing guards.
Coordinated data capture, complete resource/policy strategies, compensation,
qualified full promotion/rollback, real-provider acceptance and native x86_64 KVM
`test-metal`/`leakcheck` remain required before the requested feature is complete.

### Observed PostgreSQL restore lineage and durable receipts (2026-10-01)

Neon restoration now checks the actual target branch's project, parent branch,
recovery timestamp and full-data initialization before returning an adoptable
identity. The same checks apply to fresh creation, deterministic-name recovery,
an accepted creation whose response was lost, and discovery-based cleanup before
the target identity was acknowledged. Cleanup retains the original requested
point, including provider qualification cleanup. A matching name alone cannot
authorize adoption or deletion of an unrelated fork.

Inspection reports lineage from actual branch metadata. It does not echo the
restore request. The timestamp must represent the exact captured instant;
equivalent time zones are accepted, while different instants reject adoption.
Neon's API permits branch lineage without a `parent_timestamp`. Such an
observation carries no timestamp proof; an LSN requires a separately verified
mapping before it can establish this timestamp contract. The adapter also
requires observed full-data initialization rather than accepting a schema-only
fork. See the [Neon branch API schema](https://neon.com/api_spec/release/v2.json).

Clone-owned lifecycle reconciliation requires observed lineage before recording
a provider identity and again before asynchronous readiness. A private
`managed_postgres_restore_proofs` receipt then commits atomically with the ready
generation. It retains the original account/operation, backend fingerprint,
source and target physical identities, recovery point, observed specification
and generation. PostgreSQL reads its clock after obtaining the target row lock,
checks the still-live worker lease, and timestamps the receipt with that server
clock. A failed or conflicting receipt insertion rolls back readiness. The
receipt's lifetime belongs to the target; retained source/account/operation
identities do not introduce additional parent-lock ordering during this write.
MemoryStore implements the same atomic receipt contract for local tests.

Ready-state recovery requires the original matching receipt, including after a
committed-but-lost acknowledgement. A missing receipt or changed backend,
physical identity, recovery point, specification or generation cannot be
adopted as verified readiness. A Store without the receipt capability rejects
clone reconciliation before provider calls. Credential preparation and captured
configuration materialization/publication lock and check this same durable
receipt against the actual target row. No credential material is stored in it.

This establishes an individual PostgreSQL restore observation and its durable
control-plane receipt. Complete publication still requires the coordinated
database/object checkpoint, retained immutable source identities, object data
and access-policy proofs, remaining configuration/resource strategies,
compensation, qualified promotion/rollback, native acceptance and real-provider
acceptance. In particular, a Neon project ID selects a mutable default branch;
the coordinated capture protocol must pin the exact source branch. The adapter
reports that exact branch identity, and clone reconciliation rejects a bare
project selector as equivalent evidence. Complete-mode admission remains closed.

Verification: focused managed PostgreSQL contracts pass (82.657 s), including
four real PostgreSQL receipt contracts, provider observation failures before
adoption and asynchronous readiness, and qualification cleanup retaining the
original point after an ambiguous restore. They cover atomic readiness/receipt
commit, a lost acknowledgement followed by service restart, missing receipts,
account isolation, target/backend/spec/point/generation drift, rollback on
receipt conflict and an expired worker waiting on an unchanged locked row.
The separate capability check verifies that a Store without atomic receipts
rejects both provisioning and legacy-ready clones before provider calls.
The full Neon test package passes (1.073 s), using HTTP fixtures and leaving
paid live-provider qualification disabled. API clone/coordinator/materialization
regressions also pass against real migrated PostgreSQL (12.765 s), including
missing or stale receipts rejected before target credential issuance or target
environment materialization. Independent SQLC generation matches and whitespace
checks pass. These focused control-plane/adapter contracts do not establish
cross-provider checkpoint consistency, real-provider acceptance, a complete
state/repository test run, lint, or native KVM acceptance.


### Upstream reconciliation (2026-10-02)

The stage implementation is reconciled with main through `72893dc28`. The
merged migration set was applied to a fresh local PostgreSQL 16 database;
`schema.sql` was regenerated from that database and SQLC regenerated from the
combined named queries. This preserves the runtime configuration proofs,
materialization receipts, environment-owned queue/work admissions, and atomic
PostgreSQL restore receipts alongside upstream tenant, scheduling, exclusive
operation, service-capacity, development-bridge, and Runs changes.

The upstream shared async-route enqueue helper now owns stage selection as well
as trusted platform-tenant identity. Idempotency receipts are distinct for each
stage/customer combination; committed production receipt IDs retain their
original encoding. The helper uses the selected deployment's frozen retry
settings and rejects a receipt from another environment or customer.

Environment host visibility is checked after loading the pinned workload
settings. A production visibility edit cannot hide or expose a stage. Private
stage hosts still require the development bridge's verified scope. The new
`platform_tenant_required` app setting is copied into workload settings,
deployment pins, production projection, promotion, and rollback. False is
omitted from the canonical settings encoding to preserve preexisting immutable
configuration hashes; a true value is explicitly captured.

Both SQLC and raw invocation readers retain platform-tenant identity, occurrence
ID, first-start deadline, failure rules, work decision, and outcome code. Invalid
JSON is returned as an error before claims or delivery receipts commit. These
fields must reach the scheduler and queue consumers without dropping the
admitted customer identity or the scheduled work's retry decision.

The schema inventory now names 323 base tables, including 23 new upstream table
boundaries. Feature flags, exclusive policies and trigger bindings, UDP listener
configuration, issue alert policies, and ingest credentials retain explicit
`isolated_strategy_unavailable` blockers. Knowing a table's schema is not
proof that its rows have an isolated capture/activation/promotion strategy.

Complete admission and publication remain closed. This integration does not
establish a coordinated application/database/object-storage checkpoint, provider
configuration independence, full row coverage, compensation, or native VM
acceptance.

Verification for this merge used a task-owned local PostgreSQL instance:

- The migrated-schema/configuration/promotion/rollback contract set passed
  (`pkg/state`, 6.632 s).
- The final tenant/scheduled-work metadata, environment queue delivery,
  service-capacity, schema coverage, and workload contract set passed
  (`pkg/state`, 71.943 s).
- Clone coordinator, materialization/credential proof, app environment, tenant,
  and message/flag-context regressions passed (`cmd/apid`, 16.693 s).
- Environment/tenant/async-route regressions passed (`cmd/gatewayd-internal`,
  0.948 s; shared `pkg/gateway` regressions, 0.804 s).
- CLI environment/tenant/clone regressions passed (`cmd/gregale`, 0.875 s).
- Selected environment/clone/runtime-config/exclusive-operation regressions
  passed (`cmd/schedd`, 0.822 s; `cmd/vmmd`, 0.820 s).
- Complete local managed-PostgreSQL and Neon package suites passed
  (29.270 s and 0.829 s). Real-provider tests were disabled.
- Independent SQLC regeneration reproduced the committed generated sources;
  whitespace/conflict-marker checks passed.

These are local contracts and mocked provider tests, not native x86 Linux KVM,
`test-metal`/`leakcheck`, or real PostgreSQL/object-storage provider acceptance.

### Captured and isolated feature flag configuration (2026-10-02)

Environment cloning now includes feature flags in both stores. The private
project-configuration snapshot records the original environment identity, its
selected flag version, and the complete flag configuration. Rules, customer
groups, subjects, variant weights, progressive rollout settings, and stable
rollout seeds are retained. Captured operations materialize this immutable
payload without rereading later source flag edits. Each workload capture must
agree on the same flag snapshot, and the authenticated configuration root
includes its hash without exposing the targeting configuration.

The cloned environment starts its own flag history at version one with actor
`environment-clone`. Source history and same-environment restore pointers are
not copied. Existing flag seeds remain unchanged so percentage, variant, and
subject allocations remain comparable during testing. Subsequent edits use the
ordinary version compare-and-swap API; new flags get seeds derived from the
target environment, while copied flags retain their original immutable seeds.
Production and stage edits are independent. An empty source flag configuration
is explicitly captured and copied rather than represented by missing evidence.

Flag insertion commits with target environment/configuration materialization.
A materialization retry returns its original environment receipt and preserves
developer flag edits. Both publication proof boundaries require the original
target flag version, clone actor, and exact configuration. Even a later version
with identical contents fails the initial clone proof. PostgreSQL holds the
flag writer's environment lock through graph publication. Flag writers acquire
their project foreign-key lock before the environment lock, matching clone
capture order and avoiding a clone/writer deadlock. Environment deletion and
clone compensation remove the independent flag history in both stores.

The optional snapshot/root fields retain the encoding of older captures for
authentication. An older capture without flag evidence cannot materialize or
publish by falling back to current source flags; it requires a fresh capture.
Materialization also verifies that the source slug still identifies the original
environment lifetime.

This increment does not qualify feature flags as a complete resource strategy.
The schema registry retains `isolated_strategy_unavailable` for
`feature_flag_versions` until promotion and rollback can atomically activate
the selected tested flag configuration with durable version fences. Full-mode
admission and publication remain closed while those strategies, coordinated
database/object checkpoints, compensation, and provider/native acceptance are
unfinished.

Verification on the final source used the task-owned local PostgreSQL 16
instance. Environment-clone, captured-configuration, flag isolation/publication,
flag lock-order, and schema coverage contracts passed (`pkg/state`, 18.184 s).
API clone coordinator/materialization and feature flag regressions passed
(`cmd/apid`, 14.653 s), including preservation of developer flag edits after a
lost/retried materialization response. Independent SQLC regeneration matched
the generated sources, and whitespace checks passed. An earlier API attempt
failed when the host ran out of disk space during test database creation; the
final suites passed after reclaiming stale task-owned build artifacts. No real
provider or native KVM acceptance was run for this increment.

### Qualification pins the observed flag revision (2026-10-02)

The existing qualification receipt's per-workload fingerprints now include
the complete flag snapshot whenever the environment has published a flag
version. The flag fingerprint authenticates environment lifetime, version,
and configuration contents. It includes empty configurations published as a
version, and distinguishes a new version containing identical configuration.
An environment that has never published flags retains the legacy workload
fingerprint. Legacy or missing fingerprints cannot certify an environment
with a published flag revision.

The environment-state API exposes `feature_flags_hash` as opaque metadata.
Deployment settings hashes remain their existing identities; their values
are not replaced by qualification fingerprints. Before health and smoke
probes, the CLI captures the environment's flag hash and combines it with
each selected deployment's settings hash. The wire fingerprint is lowercase
hex SHA-256 of UTF-8 `gregale.dev/environment-workload-qualification/v1`, NUL,
the settings hash, NUL, and the flag hash. With no published flag version,
the settings hash is sent unchanged. This algorithm is shared by the CLI
and state stores through `api.QualificationWorkloadConfigHash`.

Receipt creation verifies those fingerprints under the environment lock.
PostgreSQL acquires the project foreign-key lock before the environment lock
so receipt insertion cannot deadlock with a clone holding the project row.
Promotion preview and the final cutover transaction both revalidate the
recorded fingerprints. A flag publish during probes or after qualification
is rejected without changing the target release graph. Fresh probes can
qualify the unchanged deployment against the new flag version; flag edits
do not require rebuilding that deployment. A stored flag payload changed
without advancing its version also invalidates the receipt.

The OpenAPI source/embed copy and generated Node and Python state models
include the optional metadata field. Both SDK generators reproduced their
output on a second run. Flag contents and targeting lists remain absent from
the qualification fingerprint metadata.

Verification used real task-owned PostgreSQL 16 and both state stores:
qualification, promotion, environment-state, flag-clone, and OpenAPI contract
regressions passed in `pkg/state` (22.647 s). API qualification/promotion/state
contracts passed (1.841 s), and CLI qualification/promotion contracts passed
(0.802 s). An initial combined build ran out of host disk space after the
state tests passed; API and CLI verification passed after reclaiming this
task's completed test archive. Full `pkg/api` testing from that initial
attempt did not complete. Spec source/embed copies match and whitespace
checks pass.

This establishes the flag qualification fence required by full promotion.
Atomic copying of the tested flags into the target release graph and rollback
to its retained previous flags are still required. The complete-clone feature
and the flag schema strategy remain unqualified until that activation contract
and the remaining data/resource/native acceptance gates are implemented.


### Atomic flag activation and rollback (2026-10-02)

`sync_config` promotion now freezes the source flag configuration and previous
target configuration when its durable operation is created. The private
`project_environment_promotion_feature_flags` row retains both environment
UUIDs, exact versions, authenticated payloads, hashes, and committed target and
rollback version receipts. Creation reads both environments under the same
project/environment lock order used by cutover. Preview and approval identity
include both flag snapshots, including the never-published version-zero state.
The store checks the preview identities while creating the operation, so a
change between approval and creation cannot silently become its new snapshot.

Forward activation authenticates the retained snapshots and requires unchanged
source and previous-target heads. It appends one independent target flag
version together with project configuration, workload settings, release graph,
and operation receipts in the existing cutover transaction. The exact tested
configuration retains its rollout seeds. A key independently created in both
environments with different seeds causes a conflict. Referenced customers must
still belong to the account; PostgreSQL locks those identities through commit.
Artifact-only promotion leaves the target flag history untouched.

Rollback requires the exact promoted target flag version and configuration,
then appends a new version containing the retained previous configuration in
its graph/configuration transaction. Its restore pointer refers only to the
target's own history. Replay verifies the committed version, configuration,
actor, and restore pointer; a later flag publication with identical contents
still invalidates both cutover and rollback replay. Source changes after a
committed cutover cannot change its replay or retained rollback configuration.
Missing or corrupt snapshots fail closed, including legacy operations that
would otherwise recover by reading current source flags.

The API now sends the preview flag hashes to operation creation and reconstructs
resume identity from authenticated retained metadata. Configuration verification
also revalidates the atomic cutover receipt. Recovery paths cannot identify a
synced cutover or rollback from matching deployments alone. A flag-only change
with unchanged project settings and artifacts still publishes a new graph and
restores flags on rollback. A revision changed during operation creation returns
a stale-preview conflict without retaining an operation or changing the graph.

The new migration is recorded in the schema registry as operational state;
operation snapshots and receipts are not copied into another environment. It
is included in the live schema dump and generated SQLC bindings. Full clone
admission/publication remain closed. This increment does not qualify the flag
schema strategy without its remaining resource/runtime audit, or replace the
coordinated database/object checkpoint, other resource strategies, compensation,
provider, and native acceptance requirements.


Verification passed against both stores and the task-owned local PostgreSQL 16
instance: flag promotion/rollback, source and target revision drift, first
source publication, empty configurations, conflicting seeds, unchanged-content
revisions, corrupt and missing snapshot evidence, failed graph atomicity,
workload promotion, qualification, flag lock-order, and schema coverage contracts
(`pkg/state`, 19.062 s). API promotion/qualification/state regressions passed
(`cmd/apid`, 1.370 s), including unchanged-artifact flag-only cutover/rollback,
stale target approval, and flag drift injected after validation but before
operation creation. Independent SQLC regeneration matched all generated files;
whitespace checks passed. Earlier attempts encountered a test fixture reserved
slug and shared-host disk exhaustion; the final runs passed after correcting the
fixture and reclaiming this task's stale build cache artifacts. No whole-repo,
provider, or native KVM acceptance is claimed.


### Captured Neon restore endpoint configuration (2026-10-02)

Neon branch restoration now sends the captured service-class compute bounds and
scale-to-zero timeout in its dedicated endpoint creation options. Previously
only the endpoint type was sent, so creation inherited mutable project defaults;
those could differ from the configuration authenticated by clone capture. Root
project provisioning and restore endpoint creation use the same adapter mapping
for the provider-neutral specification. The request changes only the new branch
endpoint and does not update source project or source endpoint settings.

The endpoint options are defined in the primary
[Neon OpenAPI schema](https://neon.com/api_spec/release/v2.json), under
`BranchCreateRequestEndpointOptions`. This use of explicit options preserves the
captured compute intent; it does not establish an independent project-level
region, PostgreSQL major, retention, or quota configuration strategy.

The complete Neon adapter suite passed (0.883 s). The new contract covers all
three service classes, scale-to-zero enabled/disabled, and successful/lost
creation responses. It decodes actual HTTP request fields independently, then
inspects the restored target against deliberately different project defaults;
the observed target specification and exact data lineage match the requested
capture. Replays issue no additional creation request. The test provider rejects
any source mutation request. Real provider acceptance was not run.

Checkpoint inspection also confirmed that root database identities currently
retain a project alias, while a verified restore lineage identifies an exact
project/branch pair. Source branch identity must be frozen before the coordinated
capture, rather than resolving a mutable default during bulk restoration. The
shared application/database/object checkpoint and source-data retention remain
required; pending/capturing coordinator phases continue to report
`data_checkpoint_unavailable`. This increment does not enable full admission or
publication.

### Pinned managed PostgreSQL data identities (2026-10-02)

A managed database now records a separate `data_resource_id` from the provider's
actual observation. The lifecycle `provider_resource_id` retains its cleanup and
metering scope. For Neon, the former is an exact project/branch pair and the
latter remains the project ID for a root database. This avoids changing root
cleanup into branch-only deletion or billing a project aggregate as branch usage.

The data identity commits atomically with provisioning readiness under the same
lease, backend fingerprint, desired generation, specification and lifecycle
identity. PostgreSQL obtains the row lock before reading the server clock; a
worker that loses its lease during that wait cannot publish a pin. Readiness
acknowledgement loss recovers the committed identity without another provider
request. A ready row cannot have its pin replaced through this provision writer.

Credential issuance, rotation and revocation use the pinned data identity, as do
ordinary restore reservations for databases with evidence. Lifecycle cleanup
continues using the original provider identity. Clone-owned restores atomically
retain their target data identity in the provider lineage receipt; receipt reads
and credential preparation reject a changed target dataset.

The frozen clone binding catalogue and database source hash now include the
source data identity. Both the worker and leased reservation writer require it,
compare it with the live logical source before creating a private target, and
restore from that exact identity. Distinct lifecycle/data identities are covered
in the clone worker contracts. Empty legacy fields retain their old receipt
encoding, but cannot authorize a complete clone. Existing ready rows are not
backfilled from their mutable current default; attesting their original bound
dataset remains necessary before those sources can support full cloning.

Migration `20261002030000000` adds checked nullable data identities to databases
and restore receipts. The source schema inventory names both columns. Managed
PostgreSQL lifecycle and usage-list projections now use SQLC and include the pin
through internal, customer, list, and recovery reads. The provider evidence
contains no credential material and adds no public API fields.

This increment pins source identity; it does not establish the shared database
and object-store checkpoint or source retention. The coordinator's
pending/capturing phases and complete admission/publication remain closed pending
that evidence, remaining resource strategies, compensation and acceptance.

Verification: the complete managed PostgreSQL suite passed (12.131 s), including
atomic identity readiness, lost acknowledgement recovery, stale observation and
lease rejection, post-lock expiry fencing, credential rotation/revocation,
restore lineage and lifecycle cleanup. The complete Neon adapter suite passed
(0.896 s), with a simulated default-branch change followed by exact-branch
inspection, credentials, restoration and root project cleanup. No live provider
mutation was performed.

Clone-worker/coordinator and ordinary database-copy contracts passed (11.049 s).
Focused state contracts passed (2.741 s), including schema drift admission,
registered migration columns and the frozen PostgreSQL binding catalogue after
source data-identity changes. Final state/API builds used temporary Go overlays
containing the original selected test declarations and their helpers, with all
production code included, to fit the shared host's available disk. A preceding
API run passed before the final validation refinements (10.572 s); later full
test-file builds exceeded shared disk space. These results do not claim the
complete state/API or repository suites. The overlay files and diagnostics are
retained in the task-owned runtime; repository test files were not reduced.
Independent SQLC regeneration matched all generated files and whitespace checks
passed. Provider and native KVM acceptance remain outstanding.

### Observed object version retention (2026-10-02)

Leased clone workers now require provider observations showing that every
uncopied source version remains protected through the worker's deadline. The
check runs before committing a manifest, before adopting an existing manifest,
and before consuming its remaining bytes. It authenticates the manifest hash
before provider IO, rejects mismatched version/metadata identities, and checks
deadline and cancellation again after observations. Worker takeover still fences
checkpoint writes. Already verified target copies can replay without reading
the source version or depending on its continued retention.

The S3 adapter reads the exact version with `HeadObject`, verifies the returned
version, size, ETag and modification time, and observes its existing fixed
COMPLIANCE retention. Governance retention and removable legal/event holds do
not satisfy this conservative strategy. A bucket default does not establish
protection for an older version. The adapter follows the provider's
[per-version protection contract](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-lock.html)
and [HEAD retention fields](https://docs.aws.amazon.com/AmazonS3/latest/API/API_HeadObject.html).

The GCS adapter requests the exact generation with a metageneration precondition
and validates the returned bucket/key, generation, metageneration, size, ETag
and creation time. It observes either an existing Locked object retention or a
locked bucket policy with the server's per-generation expiration. Unlocked
policies and removable holds are rejected. Sub-day bucket policies are excluded
because the pinned Go SDK does not guarantee their enforcement. These checks
use the provider's [Object Retention Lock](https://docs.cloud.google.com/storage/docs/object-lock)
and [Bucket Lock](https://docs.cloud.google.com/storage/docs/bucket-lock) contracts.

These adapters only read existing protection. They do not set irreversible
retention on a customer's source. Missing or insufficient observations produce
`object_snapshot_retention_unavailable`; a later lease obtains new observations
and cannot substitute live keys or recapture a newer manifest. This secures the
current worker IO window. Durable protection across a queued operation's entire
lifetime, retention acquisition/renewal, and a strategy for sources without
preconfigured permanent locks remain necessary. A retry can fail if its pinned
source versions expire or disappear between leases.

Retention protects object bytes. GCS editable metadata and S3 tags still require
their own frozen capture strategy. These observations do not drain application,
background, external, or shared-resource writers and do not establish a common
database/object checkpoint. Pending/capturing coordinator dispatch, complete
admission, remaining resource strategies and compensation remain closed pending
that implementation and acceptance.

Verification: the complete object-storage package passed (0.634 s), including
native SDK requests against local S3/GCS HTTP fixtures, exact version and
metageneration selection, identity mismatches, missing/short/bypassable
protection, cancellation and expiry. Focused API contracts passed (2.616 s),
including MemStore capture/copy rejection, lease takeover, durable target replay,
and real PostgreSQL object-worker recovery after manifest/copy checkpoint
failures. The API build used a temporary test-only overlay containing original
selected declarations and helpers, with all production code included; initial
attempts exceeded the shared host's available disk. This is scoped verification,
not the complete API/repository suites or live provider/native KVM acceptance.

### Frozen configuration capture dispatch and commit leases (2026-10-02)

The coordinator now drives pending/capturing operations from their authenticated
typed configuration root, workload snapshots, and frozen binding catalogue. It
builds a deterministic roster containing the source revision, project config,
and each workload's selected artifact, variables, secrets, workload settings,
route policy, and edge policy. Reader ordering cannot change this roster.
Existing capture progress must equal the canonical roster before replay.

When the captured catalogue contains no PostgreSQL or bucket data, the worker
advances through capturing into copying without inventing a data timestamp. A
lost capturing acknowledgement leaves its committed roster and no target
environment. The previous worker retains its acknowledged revision; a later
lease recovers the frozen source rather than rereading later source edits.
Captures with either dataset still return `data_checkpoint_unavailable` before
advancing the capture phase, invoking providers, or materializing a target.

PostgreSQL status advancement and final release-graph completion now recheck a
claimed operation's live lease with `clock_timestamp()` at the actual write.
This closes expiry during operation-row or target-flag lock waits. A rejected
final completion rolls back the release graph and keeps the operation's status,
revision, and target release identity unchanged. The never-claimed legacy path
requires both zero attempts and no lease token. Legacy operations with no
captured workloads retain their resource-only publication behavior; workload
captures still authenticate their roster and hold the target flag lock through
publication.

These changes cover the currently implemented internal catalogue. They do not
establish complete schema/resource coverage, a coordinated database/object
checkpoint, compensation, or full public admission. Those requirements and
provider/native acceptance remain outstanding.

Verification: 12 selected API coordinator/capture contracts pass (8.534 s), and
79 selected state clone/environment tests pass (25.023 s),
including real PostgreSQL locks, publication rollback, feature-flag isolation,
frozen configuration, qualification, promotion, and rollback. The publication
lease test first reproduced a successful graph commit after lease expiry, then
passed with the final write fence. The tests use original selected declarations
and helpers through temporary test-only overlays, with all production code
included. A full state test compilation exceeded the temporary build volume;
the selected tests are scoped evidence, not the full package/repository suites.
SQLC 1.31.1 output matches an independent regeneration. The final API run used a
fresh PostgreSQL 16 cluster on a temporary RAM volume after the shared host disk
filled during fixture creation; its fixtures ran the complete migration set.

### Owned PostgreSQL snapshot capture and dispatch recovery (2026-10-02)

Managed PostgreSQL providers can now implement an optional snapshot interface.
The Neon adapter captures the exact pinned project/branch and the capture owner's
supplied timestamp through the native
[create snapshot API](https://api-docs.neon.tech/reference/createsnapshot).
It authenticates the observed manual snapshot's owner, source, timestamp and
creation identity. Automatic expiry is cleared only on that owned copy through
the native [update API](https://api-docs.neon.tech/reference/updatesnapshot), and
an independent observation must report explicit null expiry. Missing retention
evidence, duplicate owner names, changed identities and different points fail.
Neither snapshot creation metadata nor a running-operation acknowledgement is
reported as completed creation, restorability or stage readiness.

Discovery and retention are separate operations. Discovery cannot create a
snapshot or change its retention. Retention accepts an existing exact snapshot
ID and cannot replace a disappeared copy. Cleanup authenticates the same owned
identity and observes absence after the native asynchronous
[delete API](https://api-docs.neon.tech/reference/deletesnapshot); HTTP 202 alone
cannot complete cleanup. A missing name match cannot establish absence of a
known snapshot ID. Provider resolution uses the frozen backend fingerprint,
without falling back to current defaults, and provider contexts inherit the
worker deadline. Cleanup remains available when provisioning is disabled.

Provider methods alone do not establish a shared database/object/config
checkpoint, snapshot completion, target restoration or full stage readiness.
Public admission and native/provider acceptance remain closed.

Verification: the complete managed PostgreSQL suite passed (128.334 s), and the
complete Neon adapter suite passed (3.803 s). The native API fixtures exercise
identity and retention mismatches, missing evidence, lost acknowledgements,
query/body contracts, read-only discovery and owned asynchronous cleanup. No
live provider resources were mutated.

### Leased PostgreSQL snapshot ownership (2026-10-02)

Migrations `20261002040000000` and `20261002050000000` add a private checked
snapshot ledger and a dispatch checkpoint. A leased capturing operation first
reserves the source's frozen definition, hash and common data point, under the
same database row lock used for lifecycle deletion. New intent requires the
live logical source to retain that exact dataset and sufficient PITR history.
A retained replay uses its original receipt after that finite history changes.
The source has a lifecycle deletion hold from reservation until observed cleanup,
including during an unknown provider creation outcome. Restrictive foreign keys
also prevent cascaded removal of the uncleaned ownership ledger.

The ledger commits `requested` before network IO. Only the worker that receives
the first successful dispatch acknowledgement may invoke creation. Worker
retries and takeovers discover the owned copy and retain its exact ID; they do
not issue another creation POST or select a new point. An unknown dispatch with
no observable copy remains unavailable and held. This includes a crash between
dispatch commit and the HTTP call: absence does not resolve whether the call was
submitted. A provider-supported authoritative resolution or an explicit operator
recovery procedure is still required for that ambiguous case.

Snapshot observations commit under the current token, revision and phase with a
server-clock lease check at the actual write. Lease expiry while waiting on the
source or receipt row cannot reserve, dispatch, retain or complete cleanup.

These receipts alone do not establish asynchronous snapshot completion, a shared
writer checkpoint, target restoration or full clone readiness. Worker capture
and compensation dispatch still need to consume them. Public admission and
provider/native acceptance remain closed.

Verification: nine selected state contracts passed (462.212 s), including real
PostgreSQL source lifecycle holds, recovery after takeover, frozen-source
mismatches, schema registry drift and lease expiry while waiting on the source
and receipt rows. The tests use original selected declarations and helpers
through a temporary test-only overlay with all production code included. This
is scoped evidence, not the complete state/API or repository suites. Independent
SQLC regeneration matches, and whitespace checks pass.

### Snapshot worker capture and compensation recovery (2026-10-02)

The internal capture worker authenticates its immutable plans and receipts, records
provider observations, and leaves the operation in capturing. It cannot release
a writer barrier, restore targets or publish a stage based on snapshot metadata.

Compensating coordinator dispatch now recovers owned snapshot cleanup. It
persists any discovered ID before deletion so a lost deletion/checkpoint reply
can recover by that exact ID. Pending asynchronous deletions retain their hold.
Never-dispatched intents can be cleaned without provider IO; dispatched unknown
outcomes cannot be marked cleaned merely because discovery finds no copy.
Other resource compensation still gates the final compensated state.

The source writer barrier, common database/object/config checkpoint, snapshot
completion and restoration proofs, successful-copy snapshot disposal, complete
resource strategies, account/project deletion integration and ambiguous dispatch
resolution remain required. Clone database targets still use their existing PITR
restore path; these retained receipts are not yet used as target restore inputs.
Full public admission and native/provider acceptance remain closed.


Verification: eleven selected API contracts passed (90.274 s), including
creation/reservation/receipt/cleanup acknowledgement loss, takeover,
finite-history changes, pending deletion, unknown dispatch, and existing
capture/coordinator regressions. The final run uses original selected test
declarations and helpers through a temporary test-only overlay with all
production code included. Full test-file builds were attempted; the API linker
exhausted the shared disk. This is scoped evidence, not the complete API or
repository suites. No live provider resources were mutated. Native KVM and live
provider acceptance remain outstanding.


### Shared object writer tracking and private bucket fences (2026-10-02)

The object data plane now reserves a durable placement-pinned writer receipt
before each synchronous provider mutation. The API object-delete and multipart
workers, and the S3 gateway PUT/part upload/delete/batch delete/copy/tag/multipart
paths use this guard. Reservation failure prevents provider IO. Observed
synchronous provider success removes the receipt through a bounded completion
acknowledgement that can survive caller cancellation. Provider errors, response
loss, asynchronous HTTP acceptance and failed completion persistence leave the
writer outstanding; request deadlines do not establish drainage.

The API reserves a separate native-write-grant receipt before exposing a native
PUT URL or multipart-part URL. Signing success, a failed signing response, an
expired URL and an expired multipart session do not clear that receipt. An
expired native URL may already have admitted a provider-side upload. This
increment deliberately supplies no generic grant-expiry or lease-expiry drain
rule. Existing outstanding native grants require a future provider-specific
revocation/drain proof or a replacement upload path controlled by Gregale.

Private per-bucket fences close new instrumented write admission, report
outstanding synchronous writers and native grants independently, and require
an exact owner token plus the source placement to inspect or release. They do
not expire automatically. A source-row lock serializes fencing and writer
admission. Lifecycle deletion takes that same lock and checks fences in a fresh
statement after waiting; it cannot delete a fenced source. Successful writers
already admitted can still record completion while the fence is held. Releasing
and reacquiring a fence does not erase unknown writes or native grants.

These primitives are not an environment checkpoint and are not wired into full
clone admission. Zero tracked writers does not prove coverage of legacy issued
URLs, external provider credentials or application/background writers. The
future leased coordinator must own and recover its fences, establish complete
writer coverage, secure PostgreSQL and object snapshots at one justified
application checkpoint, and release only after immutable source retention or
failed-capture recovery. No coordinator-selected timestamp or ready-stage
publication is added here. The full clone and other-resource compensation gates
remain closed.

Verification: the complete S3 gateway suite passed (4.353 s). Eleven selected
state contracts passed (25.996 s), and twenty-one selected API contracts passed
after the final handler extraction (1.999 s). A separate verbose PostgreSQL run
confirmed actual execution of the
new receipt/fence and blocked-lifecycle contracts (15.353 s). The focused state
and API builds retain original selected test declarations and helpers via
outside-repository test-only overlays, with all production files included.
Independent SQLC generation matched the committed generated files. A broader
production build was attempted and exhausted the shared disk; scoped test
builds then compiled the changed production paths successfully. No live
provider resources were mutated. Full repository, native KVM and live provider
acceptance remain outstanding.


### Brokered upload URLs participate in source fencing (2026-10-02)

New API PUT and multipart-part signed URLs now authorize requests through the
configured Gregale S3 endpoint. They no longer expose provider-native write
URLs. A private operational grant records only a random token's SHA-256 hash,
the exact account/app/bucket placement, object key, declared byte count,
portable metadata/tag headers and, for a part, its upload/part/provider ID.
Tokens use 256 bits of randomness and are returned only in the upload URL.
The API response keeps its existing URL/method/headers/expiry shape. Download
URLs remain provider-native read capabilities. Public SigV4 S3 requests retain
their existing authentication and routing.

Grant issuance authenticates the ready bucket placement and shares the source
row lock with writer admission/fencing. It rejects an active fence. Expiry is
chosen by the database clock (default 300 seconds, maximum 900 seconds), and
part expiry is capped by the active upload's expiry. Each redemption rechecks
the current placement and session, exact method/path/size/metadata, and rejects
mixed authentication, additional operation parameters and duplicate tokens.
No grant can authorize GET, DELETE, CopyObject or a different multipart part.
The gateway then uses its normal bounded upload/part proxy. The provider URL
stays private, and durable writer admission occurs before actual provider IO,
including when the customer's URL was issued before the fence was acquired.

A brokered grant itself does not count as a native writer: it cannot modify
provider storage outside Gregale. An already admitted provider request has an
independent writer receipt. Expiry and bounded background pruning remove only
request-admission grants; they cannot remove or drain active/unknown writes.
Known legacy native-grant receipts are preserved and still require actual
provider revocation/drain evidence. Native URLs issued before writer tracking,
external provider credentials, PostgreSQL writers and a coordinated application
checkpoint remain unresolved. This change does not open full clone admission,
select a capture timestamp or publish a stage.

Deployment order for this protocol is migration and S3 gateway first, then API
URL issuance. Older gateways cannot redeem the new broker grants. API and
gateway registries must share the configured public endpoint and immutable
backend placements. Clients must send the returned upload headers, as required
by the existing signed-request contract. Multipart upload creation/completion
still use their provider lifecycle, and staging snapshot/retention work still
requires independent provider evidence.

Verification: the complete S3 gateway suite passed (3.679 s). Sixteen selected
state contracts passed (15.960 s), including actual PostgreSQL expiry/pruning,
source fencing, multipart binding/expiry and blocked lifecycle tests. Twenty-four
selected API contracts passed (1.395 s), including API-to-gateway PUT and part
uploads, blocking replay of earlier URLs after fencing, exact capability
substitution rejection and existing accounting/recovery/lifecycle regressions.
State and API runs used original selected test declarations and helpers via
temporary outside-repository test-only overlays with all production files
included; the verbose runs contained no skips. Independent SQLC regeneration
matched the generated files. No live provider resources were mutated. Complete
repository, deployed protocol, native KVM and live provider acceptance remain
outstanding.


The writer inventory also found the public policy-controlled
`POST /uploads/{route}` path. It now reserves the same durable source writer
before creating a new idempotency intent and invoking `ObjectWriter`. A fence
rejection creates no pending upload intent, so the same idempotency key can be
retried after checkpoint capture. Early local exits can release their receipt
only with concrete non-dispatch evidence from the current request. Once the
provider has been called, error/cancellation/panic cannot clear the receipt.
Observed success records a bounded completion even if the caller disconnects.
The upload route uses a local wrapper over the shared state primitives because
importing the objectstorageactivity wrapper would create a package cycle.
Eleven targeted upload-route contracts passed (0.826 s), including
active/unknown writer coverage and retry after fence rejection. The complete object-storage provider
suite has not been rerun in this increment.

### Clone-owned object barriers and abandonment recovery (2026-10-02)

Object write fences now optionally reference their owning clone with a
restrictive foreign key. The durable operation owns the fence, rather than the
current worker token. Its stable fence token is the operation UUID. An expired
or relinquished worker lease never reopens writes. The private leased acquisition
writer authenticates the frozen binding catalogue, locks the exact ready source
placement, and commits ownership together with the fence. It cannot adopt an
unrelated generic fence, including one with an identical token. Generic read,
acquire and release operations cannot take ownership of a clone's fence.

Replacement workers can read and reacquire the same owned barriers using their
current operation revision, status and lease token. Counts remain observations
of instrumented requests and native grants, not claims that all writers have
drained. Successful synchronous requests can still record completion. The
source placement and ownership are rechecked after source-row lock waits;
fresh SQL clock checks reject workers whose leases expired during the wait.

Abandonment is a separate private transaction allowed only in `compensating`.
It releases all of this operation's object fences atomically after authenticating
every source placement. It does not delete unknown request/native-grant receipts
or another owner's fences. Repeated abandonment after a lost commit reply is
idempotent. The coordinator invokes this recovery before provider snapshot
cleanup, so unavailable provider cleanup cannot keep an already abandoned
object capture's source admission closed. Other cleanup remains outstanding.
Ordinary source lifecycle deletion still rejects an active fence. Operation
advancement into copying, publishing or terminal states and direct release-set
publication reject remaining owned object fences; the operation cannot discard
recovery authority while holding source writes closed.

There is deliberately no successful-capture release path yet. It must require
the verified coordinated checkpoint and retained immutable source identities.
Pending/capturing coordinator dispatch still returns
`data_checkpoint_unavailable` before acquiring these barriers. Complete public
clone admission remains closed. This increment implements barrier ownership and
abandoned-capture recovery, not PostgreSQL drainage, writer coverage, a common
application/data point, snapshot restorability, or complete compensation.
Workers using this protocol must all run compatible fence ownership queries
before capture activation; older generic release queries do not enforce the
new ownership condition.

Provider review also confirms why the current Neon controls cannot by themselves
establish the missing barrier. The pinned `EndpointUpdateRequest.disabled`
description states that `check_availability` operations periodically reenable
disabled computes. Neon documents that roles created with its API receive
`neon_superuser` membership, including the credentials issued by this adapter;
its session termination privilege only covers roles outside that membership.
See [Neon role privileges](https://neon.com/docs/manage/roles#the-neonsuperuser-role)
and [endpoint update](https://api-docs.neon.tech/reference/updateprojectendpoint).
A durable database connection closure, privileged maintenance/recovery access,
existing-session drainage, background writer control and preservation of original
access settings still need an implemented and provider-verified strategy.
Endpoint suspension, default read-only settings and cooperative advisory locks
are insufficient evidence on their own.

Verification: 15 selected original state contracts passed (25.909 s), including
the memory/PostgreSQL ownership and takeover contracts, placement substitution,
three confirmed source-lock waits with lease expiry, object writer/lifecycle
regressions and migrated-schema coverage. Twelve selected API contracts passed
(31.490 s), including real PostgreSQL coordinator recovery across failed and
lost abandonment replies, source admission reopening with an unknown writer
still tracked, and existing coordinator/native snapshot regressions. There were
no skips. The focused overlays keep all production files and original selected
tests/helpers. Independently regenerated SQLC output matches exactly. Full state,
API/repository suites, deployed mixed-version protocol and live-provider/native
acceptance have not been established by these checks.

### Private PostgreSQL connection closure and abandonment (2026-10-02)

The SQL component of a database barrier lives in
`pkg/managedpostgres/connectionfence`. It requires a private maintenance database
on the exact source cluster and a private role that owns every selected source
database. Its installer rejects public/other-role connection grants, login-role
membership that exposes maintenance authority, foreign maintenance sessions,
and schema/function grants. PostgreSQL infrastructure superusers remain trusted
operators; their active maintenance sessions still invalidate isolation checks.
The maintenance schema is not a control-plane migration or a cloned application
configuration input. SQLC owns the installer, catalogue reads and transactional
functions, and `make sqlc-check` verifies both generated packages.

Closing admission transactionally records the durable operation UUID, immutable
source identity supplied by the caller, exact database OIDs/names/owners, and
original connection flags before setting `ALLOW_CONNECTIONS false`. One active
owner is allowed per maintenance database. Recovery reuses those exact pins;
another owner or a renamed/replaced/reowned/reopened database is rejected. No
worker lease expiry automatically reopens admission. Release restores only the
recorded flags and cannot reopen a database that was originally closed. Names
are quoted inside SQL-owned functions, including punctuation and quote
characters; Go does not build production SQL.

Admission closure is distinct from session drainage. Existing sessions can
continue writing. Observation independently rechecks the selected identities and
counts all sessions for their OIDs; `Drained` requires a closed selected set with
zero observed sessions. The controller neither terminates sessions nor attests
complete cluster/background writer coverage. PostgreSQL documents the admission
setting in [ALTER DATABASE](https://www.postgresql.org/docs/16/sql-alterdatabase.html).
Its backend initialization code also permits specially initialized background
workers to override this setting; a provider coverage qualification must account
for that separately. See
[PostgreSQL connection initialization](https://github.com/postgres/postgres/blob/REL_16_STABLE/src/backend/utils/init/postinit.c).

Client cancellation after dispatch is an unknown commit outcome. A disconnected
client can leave a SQL statement waiting for a database lock, and it can commit
after the lock becomes available. Recovery authority must remain durable. The
separate abandonment function serializes against close in the maintenance
ledger. If no closure is visible yet, it records an `abandoned` terminal marker;
otherwise it restores the exact original settings. Both outcomes reject a later
close from the same operation. A lost abandonment reply is recovered without
changing the terminal timestamp or releasing another operation's barrier.
Terminal records must not be deleted while old dispatch attempts can arrive.
Successful release requires an existing closure and does not accept an
abandonment marker as proof of completed capture.

This component is not yet wired to the clone worker or the Neon adapter. Private
maintenance resource bootstrap/ownership/cleanup, exact provider placement
attestation, complete database inventory and writer closure, control-plane
lease authority for remote dispatch, application drainage, and the common
database/object checkpoint remain required. Neither a local session count nor
this provider-local ledger establishes those properties. The existing capture
and public complete-clone admission gates remain closed for data-bearing
operations until the coordinated protocol is implemented and qualified.

Verification: all eight connection-fence top-level contracts and their nine
identity/privacy subcases passed against isolated databases and roles on a real
PostgreSQL 16 cluster (7.833 s), with no skips. They include existing-session
writes after admission closure, independently observed drainage, replacement
controller recovery, exact original-flag restoration, concurrent-owner rejection,
a confirmed database-lock wait with server timeout and transactional rollback,
abandonment before delayed dispatch, and recovery of a client-cancelled in-flight
close. This does not establish Neon behavior, full repository acceptance or the
complete coordinated checkpoint.

### Control-plane ownership of PostgreSQL barriers (2026-10-02)

The private PostgreSQL barrier store reserves source recovery authority before
any maintenance resource or connection-closure IO. The reservation derives its
configuration hash, backend placement, lifecycle resource identity and exact
dataset identity from the authenticated frozen binding catalogue. It does not
require or select a data point. `held` denotes durable recovery authority,
not an acknowledgement that admission is closed or sessions have drained.
One active owner is allowed for a source catalogue row and for an exact
backend/fingerprint/dataset identity. A reservation cannot adopt another
operation's hold or rebase itself to a different source placement.

Lease handoff preserves the operation owner and all original pins. Reservation,
active-hold reads and abandonment mutations lock the same source catalogue row
as lifecycle deletion, recheck its ready placement, and reject stale worker
tokens/revisions/statuses. Fresh SQL clock checks also reject lease expiry
during source or receipt lock waits. `ClaimDelete` checks these recovery holds
after acquiring its source lock, including when ordinary binding dependencies
have been removed. The final clone advancement and release-set publication
queries reject remaining active database holds, so copying, failure or terminal
cleanup cannot discard their recovery authority.

Abandonment has a committed `abandoning` intent before remote recovery. The
source hold is released only after a trusted worker supplies an independently
observed `released` or `abandoned` terminal record for the exact operation owner
and dataset. Missing remote state, lease expiry, cancellation and arbitrary
future timestamps are not evidence. The source placement is checked again
before the control-plane write. A lost commit reply can be recovered using the
same immutable terminal record even after source lifecycle legitimately
advances; a replay cannot rewrite its terminal state or timestamp. These are
private worker methods, not public endpoints or general-purpose hold-release
operations. There is still no successful-capture release seam.

The clone coordinator and Neon maintenance/bootstrap adapter do not yet dispatch
these barriers or supply those terminal observations. The qualified adapter
must authenticate the exact remote source and maintenance ownership before
using this seam. Complete provider inventory, background/external writer
coverage, application drainage, the common retained database/object checkpoint,
successful-source release, and full compensation remain outstanding. Existing
data capture and public complete-clone admission gates remain closed. All clone
workers and managed PostgreSQL lifecycle workers must run the new ownership and
deletion queries before remote barrier dispatch is activated.

Verification: a focused run of 21 original state contracts passed (23.193 s),
including PostgreSQL barrier ownership, source deletion protection, replacement
worker authority, terminal-record substitution, placement substitution, four
confirmed source-lock waits with lease expiry, direct publication rejection,
existing memory/PostgreSQL object-barrier contracts, native PostgreSQL snapshot
ownership/source holds, and migrated-schema inventory coverage. A follow-up run
of all six new PostgreSQL barrier contracts passed (14.618 s), adding another
operation's acquisition/abandonment rejection and explicit hold-state checks
after expiry. Both runs used the opt-in migrated private-database harness and
outside-repository overlays retaining all production files and original selected
test declarations/helpers; neither run contained skips. Independent SQLC
regeneration matches both generated packages. Full state/API/repository suites,
deployed mixed-version behavior and live-provider/native acceptance remain
unverified.

### Owned native snapshot restore evidence (2026-10-02)

A private provider/service protocol now restores an operation-owned retained
snapshot into a separately named native fork. The source backend fingerprint,
spec, lifecycle and exact dataset, snapshot owner and capture point remain
frozen. Recovery discovers the original name until its opaque identity is
persisted, then reads only that identity. Discovery cannot create a replacement;
a missing pinned target cannot authorize another POST. Creation requires the
caller's already committed target ownership and first-dispatch receipt. The
control-plane dispatch receipt is supplied by the private durable fork worker
described below; public clone dispatch remains separately gated.

The [Neon snapshot restore API](https://api-docs.neon.tech/reference/restoresnapshot)
is explicitly called with `finalize_restore: false` and the captured source
branch as `target_branch_id`. Exact native snapshot lineage (`restored_from`)
and independently read target `current_state: ready`, empty pending state and
`restore_status: restored` establish native storage restoration. Snapshot
metadata has no readiness field; creation timestamps, retention and POST
acknowledgements alone cannot establish this completion. The target's creation
time must follow the retained snapshot, and project organization, region and
PostgreSQL major are independently rechecked. An optional `restored_as` must
agree with the captured source when supplied. Branch discovery follows every
page, rejects duplicate exact names and cursor cycles, and uses the documented
API page bound rather than introducing a plan quota.

Storage restoration is not application readiness, a common database/object
checkpoint or publication authority. Copied maintenance resources, SQL roles,
admission flags, endpoint configuration, credentials, target catalogue adoption
and cleanup still require isolation and durable worker integration. The public
data-bearing capture gate remains closed. No provider resource was changed.

Verification: the managed PostgreSQL/Neon package run passed 111 top-level
unit/memory/mock contracts; 16 opt-in PostgreSQL store contracts and one live
Neon lifecycle contract were skipped. After adding future-time rejection and
invalid-selector checks before provider IO, all six snapshot-restore contracts
passed again. They cover asynchronous observation, lost replies and delayed
visibility, identity and placement substitution, paginated discovery and pinned
missing targets. `go vet` for both packages and `git diff --check` pass. These
are service and HTTP fixture proofs; live-provider and full clone acceptance
remain unverified.

### Recoverable maintenance bootstrap and exact Neon connection (2026-10-02)

The SQL bootstrap now separates private owner reservation, closed database
creation, and activation. Its caller must durably reserve a private UUID for
the exact dataset before IO. This identity is distinct from a public stage name
or a transient worker lease. The reserved NOLOGIN role carries a versioned
ownership marker with the UUID, exact source identity, actor/owner OIDs and
state. Recovery checks both this marker and PostgreSQL's creator grant
provenance; a role created by an application administrator cannot be adopted
by copying the marker or granting membership to the platform administrator.
Unrelated databases at the reserved name are never adopted or overwritten.

All production SQL is generated by SQLC. Bootstrap functions exist only in the
dedicated connection's temporary schema. A session advisory lock, acquired in
the authenticated source SQL database, spans the nontransactional CREATE and
all state changes. Every worker must use that same source database. CREATE
itself specifies `ALLOW_CONNECTIONS false`, with the private nonce role as
owner. Recovery can identify a committed creation after losing its response.
The resulting database OID must be persisted before activation. Activation
atomically revokes PUBLIC access, opens the database, records its OID and removes
the nonce role's CREATEDB privilege. The platform's private source owner inherits
the nonce role's database privileges; applications receive no such membership.
The connection fence controller accepts this inherited ownership and can check
both independently supplied bootstrap OIDs on every maintenance operation.

Reserved bootstrap retirement serializes with creation and leaves a permanent
role tombstone, even when reservation has not arrived yet. It never removes
resources or retires an activated installation. Late reserve/create attempts
cannot restore the owner or its CREATEDB privilege. Failed/cancelled bootstrap
calls close their dedicated session; cancellation and a missing early catalogue
observation are not permission to drop control-plane recovery authority. This
retirement proof is separate from the connection fence ledger's operation
abandonment record and is not yet accepted by the clone coordinator.

This role isolation implementation requires PostgreSQL 16 or later. Older
CREATEROLE semantics need a separately qualified strategy; this does not reduce
the complete-stage feature's required provider/version coverage. The relevant
creator and role-management checks are visible in
[PostgreSQL role creation](https://www.postgresql.org/docs/16/sql-createrole.html)
and in Neon's pinned PostgreSQL 16 source:
[role creation and alteration](https://github.com/neondatabase/postgres/blob/a42351fcd41ea01edede1daed65f651e838988fc/src/backend/commands/user.c),
[role membership authority](https://github.com/neondatabase/postgres/blob/a42351fcd41ea01edede1daed65f651e838988fc/src/backend/utils/adt/acl.c).
Source review does not establish deployed Neon behavior.

The Neon adapter exposes the separate bootstrap worker operations. It resolves
the exact `project/branch` identity, rejects default aliases, checks the project
organization/region, branch/project, unique direct read/write endpoint and
pending operations, requests that exact branch's private `gregale_owner` URI,
and independently rechecks placement after receiving it. The returned role,
database, host and port must match; the PostgreSQL major is pinned across both
observations and checked again on the SQL connection. It rebuilds a minimal
connection with verified TLS and a fixed application name, discarding supplied
startup/session options.
Connection errors are redacted. Bootstrap observations use the source database
before source admission is closed; recovery of an installed barrier must use
the private maintenance database and its durable ready receipt instead.

Still required: the control-plane maintenance registry and leased dispatch
authority, ready-maintenance connection/recovery wiring, qualified activated
cleanup and cloned auxiliary-resource handling, complete database/background
writer inventory, application drainage, the common retained database/object
checkpoint, successful source release and compensation. Data capture and public
complete-clone admission remain closed. No live Neon resource was mutated.

Verification: all 13 connection-fence top-level contracts passed against real
isolated PostgreSQL 16 databases/roles (35.570 s, no skips), including the new
bootstrap recovery, 13 ownership
substitution cases, three retirement orderings and a confirmed cancelled
advisory-lock wait. Four connection identity/version/privacy substitutions are
rejected before owner creation. All 30 Neon unit/mock top-level contracts passed
(0.800 s), including exact branch selection, 23 placement/credential failures and
rejection before IO of invalid/default-alias requests. The opt-in live Neon
lifecycle test was skipped because its environment was not configured. SQLC
regeneration and `go vet` pass for both affected packages. An earlier regression
run was interrupted by a local disk-full PostgreSQL restart; its verified owned
fixture leftovers were removed before the successful rerun. No test databases
or roles remain, and the shared bootstrap database remains unmigrated. Full
repository, live-provider and native acceptance remain required.

### Durable maintenance ownership registry (2026-10-02)

`managed_postgres_checkpoint_maintenance` now retains a private maintenance UUID
for each exact source dataset. It records the initial reserving operation,
backend fingerprint, lifecycle resource, dataset identity, owner/database OIDs,
and each bootstrap dispatch intent before remote work. Its lifetime is separate
from an operation's source writer hold. A later clone may reuse a ready owner
only after acquiring its own authenticated source hold; it cannot rebase the
owner, placement, initial operation or dispatch timestamps.

The private store requires a live capturing lease and held source fence, or a
compensating lease and persisted abandonment intent. It locks the operation,
source fence, source catalogue and maintenance receipt in that order, then
rechecks SQL-clock lease authority after lock waits. Source placement must still
match the frozen capture and receipt. Bootstrap progresses through role,
database and activation request/acknowledgement states. Replacement workers
recover the original dispatch receipt. OIDs are immutable once observed; the
trusted adapter must authenticate them independently before recording them.
The OID checks use PostgreSQL's unsigned 32-bit identity representation.

Ready means maintenance readiness only. It cannot move an operation to copying,
select a data point, or release writers. Compensation retains the source hold
while an owned maintenance bootstrap is unresolved, then still requires the
separate authenticated remote terminal barrier record. The registry may finish
an owned bootstrap during compensation so that the remote abandonment ledger
can become available. Activated cleanup, registry retirement and project/account
deletion integration remain required. No provider bootstrap is dispatched by
the coordinator in this increment, and data capture/admission remain closed.

Verification: 28 selected original state contracts passed (29.469 s, no skips),
including the first six maintenance contracts and existing PostgreSQL/object
barrier, source snapshot and migrated-schema inventory contracts. All eight new
maintenance contracts subsequently passed (31.294 s, no skips), adding placement
substitution and four independently confirmed maintenance-row lock waits with
lease expiry. They cover private owner recovery, stale workers, phase ordering,
immutable observations, compensation and reuse across operations. Observed OIDs
in these state contracts are metadata fixtures; the real PostgreSQL bootstrap
contracts above provide separate SQL evidence. Both focused runs retain all
production files and selected original tests/helpers through an external AST
overlay and use the opt-in private migrated PostgreSQL harness. Independent
SQLC regeneration and `git diff --check` pass. Full state/API/repository suites,
live-provider and native acceptance remain unverified.

### Leased maintenance recovery and PostgreSQL abandonment wiring (2026-10-02)

The worker now consumes the maintenance registry through a provider-neutral
reconciliation service. Every bootstrap phase renews the clone lease, reserves
or recovers the original private owner, authenticates its frozen plan and
persists dispatch intent before SQL IO. Idempotent SQL bootstrap recovery uses
the same source advisory lock and ownership receipt after an unknown reply.
Acknowledgements are recorded under fresh lease authority before another phase
starts. Ready receipts are independently observed and then reread under the
control-plane source lock; readiness still provides no data checkpoint.

The service resolves the captured backend fingerprint, region and spec. Neon
checks that the frozen lifecycle resource and exact dataset belong to the same
project, and to the same branch when the lifecycle resource names a branch. A
project-only lifecycle identity never substitutes a new default dataset. The
captured PostgreSQL major must agree with independently rechecked endpoint
placement and the SQL server. These checks also apply during compensation.
Owned recovery continues when new database provisioning is disabled.

A ready owner can now be authenticated through `gregale_checkpoint` after
source connections are closed. The private SQL owner marker, creator grant
provenance, source identity, actor/session identity, database/owner OIDs and ACL
are rechecked. The temporary bootstrap function permits only ready observation
from this database; reservation, activation and retirement remain source-only
operations. The Neon adapter requests the exact branch's private maintenance
database URI, retaining verified TLS and minimal startup parameters. Its
connection fence controller checks the persisted OIDs and private installation.
No complete writer-coverage capability is advertised by this recovery seam.

Coordinator compensation now persists PostgreSQL source abandonment intent
before maintenance recovery and remote IO. It finishes an already owned
bootstrap when needed, authenticates the ready maintenance owner, and records
the operation-specific remote barrier tombstone. The maintenance nonce and
barrier operation UUID remain separate. A subsequent independent provider read
must return that exact remote terminal record before the source hold is
released. Missing rows, substituted identities, cancelled calls and unknown
responses retain recovery authority. After a lost committed release reply, a
replacement worker recovers the stored terminal receipt without dispatching
another abandonment. Object barrier and native snapshot cleanup follow; complete
compensation and publication still require their remaining resource proofs.

Data-bearing capture still waits before provider dispatch. Still required are
qualified closure and complete database/background/external writer coverage,
application drainage, the common retained database/object checkpoint, successful
capture release, asynchronous snapshot/restorability proofs, cloned auxiliary
resource handling, activated maintenance retirement, older PostgreSQL versions,
complete configuration strategies, secret substitution, promotion and rollback,
and provider/native acceptance. No live provider resource was changed.

Verification: all 15 connection-fence contracts passed against real isolated
PostgreSQL 16 resources (28.036 s, no skips). The two ready-maintenance contracts
passed again after adding portable version pins and actual session-role
substitution (2.168 s). The managed PostgreSQL and Neon packages reported 106
passing top-level unit/memory/mock contracts; 16 opt-in PostgreSQL store contracts
and one live Neon lifecycle contract were skipped because their test environments
were not configured for that run. All 14 selected original coordinator contracts
passed (13.398 s, no skips), including maintenance phase recovery, lost replies,
worker handoff, independent terminal proof and existing capture/snapshot/object
regressions. The two new worker contracts passed again after the final phase
guards (3.952 s). Coordinator runs retain all 441 production files and selected
original test declarations/helpers through an external test-only AST overlay.
Their provider observations are metadata fixtures; SQL recovery is separately
verified by the real PostgreSQL contracts. SQLC regeneration, `go vet` for the
three managed PostgreSQL packages and `git diff --check` pass. Local disk
exhaustion interrupted an intermediate final check; the existing test server
recovered, unused verified test templates were removed, and the final worker
rerun passed. Full repository, live-provider and native acceptance remain
unverified.

### Durable native snapshot fork dispatch and worker recovery (2026-10-02)

`project_environment_clone_postgres_snapshot_restores` now reserves one private
target owner per operation and captured database. Its foreign key retains the
original snapshot receipt. Account, backend fingerprint and target owner cannot
be selected by the caller; they derive from the authenticated capture. New
reservation checks the live source placement under its lifecycle row lock and
requires an independently retained snapshot. It does not consult a finite PITR
window after retention. A capturing resource must still have its original
captured state and no catalogue target.

Reserved targets count against the existing managed database account quota.
The same account row lock and combined count protect ordinary customer database
creation, older clone reservations and these private native forks. Recovery
reuses an existing reservation when limits or new provisioning admission change.
The future adoption/retirement lifecycle must transfer or release that quota
without double counting; it is not implemented by this increment.

The private receipt advances from reserved to requested before provider IO,
then to restoring or restored through exact independent observations. Its
snapshot, source dataset, capture point, snapshot creation time and first target
identity/creation time are immutable. Ready observations cannot regress, and
replays preserve the first completion time. SQL-clock authority is rechecked
after account/source/snapshot/receipt row waits. Future target creation times
and times that cannot be represented exactly at PostgreSQL microsecond precision
are rejected.

The capture worker renews its lease and bounds native calls by that lease.
Only a worker receiving the first committed dispatch may call restore. An
unknown commit, lost provider reply, delayed visibility or lost observation
acknowledgement recovers through discovery, using the reserved owner and then
the exact persisted native identity. An unknown dispatch that never becomes
visible retains its recovery intent; this seam cannot authorize another POST
or prove remote retirement from absence alone.

Native storage completion cannot publish a target or advance to copying.
Operations with these receipts retain capture/compensation authority until a
qualified adoption or retirement lifecycle exists. Snapshot cleanup likewise
refuses to discard a snapshot with an unresolved fork. No public capture path
invokes the new worker yet, and the existing data-bearing admission gate remains
closed. Required next work includes native fork cleanup and catalogue adoption,
copied SQL auxiliary/credential/admission isolation, endpoint configuration,
complete writer coverage and coordinated database/object capture, all remaining
configuration strategies, promotion/rollback, and provider/native acceptance.
No live provider resource was changed.

Verification: the final state run passes all 13 selected original contracts
against the private migrated PostgreSQL harness (27.500 s, no skips), including
the four new fork contracts, existing snapshot lifecycle contracts and the
migrated schema inventory. They cover durable dispatch, quota interoperability,
source placement changes, immutable observations, worker takeover, cleanup and
phase guards, and four independently confirmed row-lock waits with expired
leases. The final coordinator run passes 14 selected original contracts
(24.637 s, no skips), including the three new native fork worker recovery
contracts and existing configuration/capture/snapshot regressions. State and
coordinator runs retain all 523 and 442 production files respectively through
external test-only AST overlays. Native observations in these runs are metadata
fixtures; the preceding service/Neon HTTP contracts provide separate adapter
evidence. SQLC independently regenerates both packages byte-for-byte, `go vet`
and `git diff --check` pass. Local disk exhaustion interrupted an intermediate
state run; only reconstructible task cache objects and inactive verified
task-owned test templates were removed before the final passing rerun. The
original snapshot expiry tests now set explicit short fixture deadlines,
preserving their independently observed row-lock waits; ordinary lease renewal
only extends a lease and had kept their minute-long initial deadlines.
Full repository, live-provider and native acceptance remain unverified.

### Owned native snapshot fork retirement (2026-10-03)

The private fork receipt now preserves deletion intent, exact provider target,
target creation time and a canonical immutable set of provider operation IDs.
Only a live compensation lease can begin cleanup. An undispatched reservation
can retire locally; a requested restore with an unknown outcome must first
recover its native identity through the existing authenticated discovery path.
An absent name cannot retire that intent or release its quota.

The managed PostgreSQL deletion seam authenticates the frozen backend, source
dataset, snapshot and fork times and known physical target before provider IO.
Owned recovery remains available while new provisioning is disabled. The Neon
adapter rechecks project organization, region and PostgreSQL major, and verifies
the exact branch's generated owner name, snapshot lineage, creation time and
unfinalized, nondefault state before DELETE. It never substitutes a branch found
by display name for a missing pinned identity.

A DELETE acknowledgement cannot finish retirement. The adapter requires an
authenticated `delete_timeline` event for the exact project and branch, finished
status for every pinned operation, and a separate exact branch read confirming
absence. Operation IDs returned by DELETE are persisted before independent
observation. A lost or empty reply can recover references through paginated
operation history; the worker persists those references and repeats observation
using the pinned set before terminal mutation. Missing operations, failed or
unfinished statuses and changed identities retain recovery authority. These
requirements follow the [Neon branch deletion API](https://api-docs.neon.tech/reference/deleteprojectbranch)
and [operation API](https://api-docs.neon.tech/reference/getprojectoperation).

The adapter can attempt the same exact branch DELETE again only after fresh
ownership/lineage/time checks and a complete current operation listing showing
no deletion event. This is scoped to the persisted physical identity; Neon
does not promise generic DELETE retry safety. Provider qualification remains
required. Root/default branches and branches with children can be undeletable,
and old operation history can expire. Such refusals or missing evidence keep
the receipt; no project deletion, unprotection or absence-only fallback is
authorized by this protocol.

Only verified retirement releases the private fork's managed database quota.
Snapshot cleanup continues to hold a source snapshot while any native fork
remains unresolved. Compensation runs source abandonment, fork retirement and
then snapshot cleanup. Failed/compensated operation transitions also refuse to
discard lease authority while source snapshots remain unretired. A replacement
worker reuses committed identities and operations, and skips provider IO after
a lost committed terminal reply. Deleted fork receipts remain readable after
their source snapshot is retired. All mutations recheck SQL-clock authority
after owned row-lock waits. The migration refuses downgrade when retirement
rows cannot be represented in the old protocol.

This increment does not adopt the fork into the target database catalogue or
publish a stage. Native fork adoption, copied SQL auxiliary/credential/admission
isolation, successful snapshot disposal, complete writer coverage, coordinated
database/object capture, all remaining configuration strategies, promotion and
rollback, and live-provider/native acceptance remain required. The public
data-bearing capture gate remains closed. No live provider resource was changed.

Verification: all 11 selected service/Neon HTTP contracts pass without skips
(0.846 s and 0.779 s). All 16 selected state contracts pass against the private
PostgreSQL harness (33.363 s, no skips), including quota release, source snapshot
holds, immutable identity/operation recovery, worker takeover and four confirmed
fork-row lock waits with expired leases. All 19 selected original coordinator
contracts pass (24.934 s, no skips), including native fork cleanup, lost replies,
pending deletion, independent pinned terminal reads and source abandonment
regressions. State and coordinator runs preserve all 524 and 443 production
files through external test-only AST overlays. Their native observations are
metadata fixtures; service/Neon contracts separately exercise HTTP authentication
and asynchronous operation/absence proof. SQLC independently regenerates both
packages byte-for-byte (nine files), `go vet` passes for state, API and the two
managed PostgreSQL packages, and `git diff --check` passes. A private schema
Down/Up round trip with no retirement rows also passes and is rolled back.
An intermediate state fixture incorrectly discarded its resource inventory;
the corrected test proves the snapshot terminal guard using valid compensated
resources. Local disk exhaustion interrupted the first API build and an
inactive task-template cleanup; the private test server recovered and the final
API run passed. Full repository, live-provider and native acceptance remain
unverified.

### Private native capture catalogue ownership (2026-10-03)

A restored native snapshot fork now transfers its existing owner into a private
managed database catalogue row in the same transaction as its adoption receipt.
The row keeps the exact native provider identity and frozen source definition,
source dataset and common capture point. Adoption does not consult live PITR
retention or reselect today's source configuration. The account quota lock,
operation lease and snapshot/fork ownership are rechecked before commit. A lost
commit reply recovers the same catalogue owner without another provider restore.

This row has `clone_resource_role='checkpoint'`, a separate deterministic private
name, no qualified data-resource identity and observed generation zero. It is
not the final stage database. The ordinary target and capture have separate
reservation keys for the same operation and captured source. Customer get/list
and customer database locks exclude captures even if a broad or forged operation
receipt names one as a published target. Ordinary provisioning, deletion and
background reconciliation cannot claim the capture owner. Its frozen metadata
and still-unready state are checked whenever the fork receipt is read or reused.

The quota charge transfers from the native receipt to its catalogue row without
release or double counting. Pending retirement retains that charge and the
source snapshot. Independent native deletion proof retires both the capture
catalogue row and fork receipt atomically under the compensation lease. Deleted
receipts remain recoverable after source snapshot cleanup. Downgrades refuse to
remove adoption or capture ownership that the older protocol cannot represent.

Neon's `history_retention_seconds` and network access configuration belong to
its provider project; a same-project fork cannot isolate changes to those
settings. Its snapshot finalization operation also reassigns the original
branch's computes and names. Therefore the native preview fork is an immutable
capture resource for this workflow, not a qualified complete stage database.
The final stage requires an independently configured provider project and a
verified copy from the owned capture (or a provider operation with equivalent
independent configuration and immutable data proof). Native source finalization
and source-project setting changes cannot implement stage activation or
promotion. See the [project update API](https://api-docs.neon.tech/reference/updateproject)
and [snapshot finalization API](https://api-docs.neon.tech/reference/finalizerestorebranch).

The private capture worker adopts completed native forks, retaining an unready
catalogue owner on failure and skipping remote discovery after a committed
adoption. The public data-bearing capture gate remains closed. Remaining work
includes independent target-project materialization, copied SQL admission,
roles/credentials and background-resource isolation, complete writer coverage,
coordinated database/object capture and successful disposal, all configuration
strategies, promotion/rollback, and provider/native acceptance. No live provider
resource was changed.

Verification: all 20 selected state contracts pass against the private migrated
PostgreSQL harness (38.142 s, no skips), including the four adoption contracts,
existing fork/snapshot lifecycle and schema inventory contracts. They cover
frozen configuration after PITR expiry, exact owner reuse, quota transfer and
retirement, metadata substitution, capture/final-target coexistence, customer
visibility even with a forged publication receipt, and independently confirmed
account/fork/catalogue lock waits with expired leases. All 22 selected original
API contracts pass (20.074 s, no skips), including the two adoption recovery and
cleanup contracts and the ordinary captured PITR worker regression. State and
API runs retain all 525 and 443 production files through external test-only AST
overlays. Provider observations are metadata fixtures; this increment does not
qualify provider data/configuration isolation. Independent SQLC regeneration
matches both packages byte-for-byte (nine files). Vet for state, API and the
managed PostgreSQL/Neon packages and `git diff --check` pass. Both new migrations
also pass a rolled-back private schema Down/Up round trip without owned rows.
An initial SQLC boolean-expression type mismatch was corrected by a single
EXISTS over the two hold sources. Disk exhaustion interrupted intermediate
state/API runs; only verified inactive task test databases were removed before
the final passing sequential runs. Full repository, live-provider and native
acceptance remain unverified.

### Independent PostgreSQL copy target preparation (2026-10-03)

An adopted native capture can now reserve a separate, still-private final
database owner during the capturing phase. The transaction authenticates the
frozen source, retained snapshot, adopted capture and catalogue identities,
then reserves quota and the deterministic final target name together. It does
not consult live source PITR or desired settings. Source, native capture and
independent target each retain their own quota charge until qualified retirement.
A foreign or legacy reservation cannot be adopted into this protocol.

The independent target has its own durable first-dispatch marker. Unknown create
outcomes use discovery without another POST; known provider IDs and creation
times remain pinned. Observed provider identity commits with the unready
catalogue row. Preparation completion is separate from database data readiness:
the row remains provisioning, with observed generation zero, no qualified data
identity, no credentials or customer publication authority. Generic PITR
provisioning, reconciliation, deletion and customer reads cannot claim it.

The Neon implementation authenticates the exact native capture and snapshot
with read-only API calls, then creates a separately configured provider project.
It never finalizes the source restore or edits its project. Creation copies the
frozen managed PostgreSQL specification. Independent observations check project
name/organization/region/version/creation time, root branch, endpoint ownership,
actual and default compute settings, storage quota and history retention, and
operation status. Search follows every bounded page, rejects duplicate owner
matches, cycles and unavailable/incomplete project lists. A creation reply is
followed by independent reads, including when the reply claims completion.
Pinned missing projects cannot trigger replacement creation. See the
[project creation API](https://api-docs.neon.tech/reference/createproject) and
[project listing API](https://api-docs.neon.tech/reference/listprojects).

Active copy targets hold their native input and capture-phase authority. An
undispatched reservation can retire locally under the compensation lease,
atomically retiring its catalogue owner and releasing only that charge. A
dispatched project cannot use this shortcut. Its verified remote retirement
protocol is still required. Downgrade refuses to erase any target ownership.

The private worker prepares targets with renewed clone leases, durable admission
and dispatch receipts, and exact discovery after failures or handoff. It does
not advance the operation, publish a stage, release source writers, or place the
intermediate capture in the stage resource map. This helper remains separate
from public data capture until independently verified import/export, copied SQL
and background-resource isolation, dispatched-project retirement and successful
capture disposal are qualified. Configuration independence alone does not prove
a complete data copy. Object/common-point consistency, full configuration
coverage, promotion/rollback and provider/native acceptance also remain open.

Verification: all 24 selected state contracts pass against the private migrated
PostgreSQL harness (35.550 s, no skips), including the four new independent
reservation/dispatch/observation/retirement contracts, metadata substitution,
confirmed account/receipt/catalogue lock waits with expired leases, and native
snapshot/fork/schema regressions. All 24 selected API contracts pass (21.794 s,
no skips), including the two independent target worker contracts, original
native capture/adoption/cleanup, coordinator and ordinary PITR worker regressions.
External AST overlays retain all 527 state and 444 API production files and
select only original test declarations. All seven service/Neon HTTP contracts
pass (0.546 s and 0.668 s); their provider observations are metadata fixtures,
not independent data/configuration qualification against Neon. Independent SQLC
regeneration matches both packages byte-for-byte (nine files). State/API and
managed PostgreSQL/Neon vet, whitespace checks and a rolled-back empty private
schema Down/Up round trip pass. Tests caught and fixed a shadowed reservation
error; a target-creation fixture was also corrected to report its actual time
instead of a future timestamp that the store correctly rejected. Disk exhaustion
interrupted intermediate setup, provider builds and one state run. Only verified
inactive task test databases were removed. Full repository, live-provider,
complete clone and native acceptance remain unverified.

### Independent PostgreSQL copy target retirement (2026-10-03)

Compensation now retires independently owned copy projects before the adopted
native capture and retained source snapshot. A committed cleanup intent precedes
provider mutation. Dispatched targets retain their original request, provider
identity, creation time and preparation metadata, and acquire separate deletion
start/observation times. Receipt and catalogue retirement commit together after
an exact terminal observation; source and capture quota remain reserved until
their own cleanup completes. Undispatched targets keep the existing local-only
retirement path. Generic lifecycle workers cannot take over active copy owners.

Unknown creation outcomes require discovery of the original private owner across
both active and recoverable-deleted provider project lists. Every page must be
complete and unambiguous. The current Neon wire format reports incomplete lists
with `unavailable_project_ids`; this is now checked in preparation and cleanup,
alongside the older unavailable marker. Missing names, incomplete lists, duplicate
owners or changing creation identities cannot authorize retirement or another
project creation. Once pinned, cleanup uses the exact provider ID and creation
time, with no name-based replacement. Cleanup authenticates frozen ownership and
does not read or mutate the live source or native capture through provider APIs.
Mutable compute/storage/retention drift does not prevent an otherwise exactly
owned failed project from being cleaned up.

The Neon adapter requires both a matching entry from
[`GET /projects?recoverable=true`](https://api-docs.neon.tech/reference/listprojects)
and absence of the exact active project, rechecked after reading the deleted
descriptor. The entry must match the owner name, provider ID, organization,
region, PostgreSQL major and creation time. A
[project DELETE acknowledgement](https://api-docs.neon.tech/reference/deleteproject)
is never terminal proof. A separate worker observation supplies retirement
authority, including recovery after a lost DELETE reply. Recovery remains
available when new provisioning is disabled. Uncertain observations retain the
target charge, immutable input and operation recovery authority.

The current official OpenAPI supplies no project-deletion operation action or
deletion timestamp in the project descriptor. The adapter therefore does not
reuse branch-deletion operations as project evidence or invent a deletion event
time. Neon documents deleted projects as recoverable for seven days; after the
descriptor expires, a bare 404 is insufficient and cleanup remains pending.
Live qualification of the recoverable-list filter, exact active GET behavior,
recovery races and repeated DELETE following fresh ownership authentication
remains required before public admission. The reviewed
[Neon OpenAPI](https://neon.com/api_spec/release/v2.json) had SHA-256
`54dfd27c22f64fb31f100497885cb2465a512028aa3ca5e17ac574d554fb20e5`.

Verification: all 27 focused state contracts pass on the private migrated
PostgreSQL harness (32.885 s, no skips), including cleanup of an unknown dispatched
owner, atomic identity/retirement, exact substitution rejection, preparation
retention, replay after input cleanup, and confirmed receipt/catalogue lock waits
with expired leases. All 27 focused API contracts pass (16.092 s, no skips),
including lost intent/identity/DELETE/retirement replies, handoff, pending quota
and input holds, undispatched retirement without provider IO, and coordinator
cleanup ordering. Original snapshot/fork/schema, coordinator and ordinary PITR
worker regressions remain in these runs. External test-only AST overlays retain
all 528 state and 445 API production files. All ten service/Neon HTTP contracts
pass (0.426 s and 0.686 s); these are metadata fixtures, not live-provider data
qualification. Independent SQLC generation matches both packages byte-for-byte
(nine files). State/API/service/Neon vet and whitespace checks pass. An empty
private-schema Down/Up round trip, rolled back without changing the migration
version, caught and fixed removal of a temporary constraint after its referenced
columns; the passing round trip restores all sixteen receipt columns. Downgrade
refuses rows with deletion intent or observation. Full repository, live-provider,
independent SQL/data import, successful capture disposal, object/common-point
consistency, full configuration coverage, promotion/rollback and native acceptance
remain open. This increment does not enable complete stage cloning.

### PostgreSQL shared-catalogue copy inventory (2026-10-03)

Database materialization now has a private SQLC reader for cluster-wide metadata
on an independently authenticated capture connection. A single database archive
does not cover these resources: PostgreSQL documents
[`pg_dump`](https://www.postgresql.org/docs/current/app-pgdump.html) as a
per-database export and directs global objects to a separate cluster export.
The new inventory is input to that copy plan; it is not an export/import or
checkpoint-completeness receipt.

All database catalogue entries are read, including templates, provider/system
databases, closed databases and copied private maintenance resources. The reader
records ownership, ACLs, encoding, collation/locale/version, connection admission,
connection limits and tablespace identity. It also reads roles and their flags,
role memberships including grantor and ADMIN/INHERIT/SET authority, tablespace
ownership/ACL/options, prepared transactions, and all database/role/role-in-database
settings from
[`pg_db_role_setting`](https://www.postgresql.org/docs/16/catalog-pg-db-role-setting.html).
The subsequent plan must explicitly classify every entry, including provider and
maintenance resources; none is filtered out merely because it is not a usual app
database. Prepared transactions cannot be recreated by a database dump and must
remain a named qualification blocker unless a qualified strategy handles them.
Physical tablespace paths require target-specific mapping.

The inventory reflects the capture's actual catalogues, including temporary
writer-barrier admission changes. Copy planning must recover original admission
from the authenticated fence receipt when deriving logical target configuration;
it cannot treat temporary closure as the customer's intended connection policy.

The reader checks frozen SQL database/role names and OIDs and the expected server
major. It rejects a changed session role or an already active caller transaction.
Every read uses one read-only repeatable-read transaction and transaction-local
`pg_catalog` search path, which cannot be hijacked by application objects or
persistently change the source's settings. Borrowed session state is restored on
commit or rollback. Catalogue references are checked against the complete role,
database and tablespace inventory. OIDs are source replacement detectors and
must never be reused as target physical identities. Provider branch/endpoint and
snapshot-point authentication remain separate caller obligations; SQL OIDs alone
cannot establish the correct fork or common data point.

Role/database configuration can contain customer secrets. The private payload
uses a domain-separated HMAC-SHA256 fingerprint with an operation-scoped key;
ordinary JSON and formatted output expose counts and the fingerprint only.
The explicit payload export must go through authenticated encryption with the
operation's scope before persistence. Recovery requires authenticated decryption,
the original fingerprint/key and SQL identity pins; it validates the payload
shape and recovers it without rereading today's source. Passwords and password
hashes are not read. New target credential generation and explicit role/binding
substitutions are still required, rather than silently omitting login roles.

Version-dependent catalogue fields are read through JSON probes. Membership
INHERIT/SET authority follows the actual per-grant fields on PostgreSQL 16+
([catalogue definition](https://www.postgresql.org/docs/16/catalog-pg-auth-members.html));
older membership catalogues lack those fields
([PostgreSQL 14 definition](https://www.postgresql.org/docs/14/catalog-pg-auth-members.html)),
so the reader records member-role INHERIT and permitted SET behavior. Locale
fields likewise tolerate supported older catalogue layouts. This does not
qualify the still-open PostgreSQL 14/15 private-maintenance bootstrap or any
unrun version's complete copy path.

Verification: three contracts pass against the private PostgreSQL 16 server
(1.002 s, no skips), using fresh databases and unprivileged login roles. They
exercise a database with connections disabled, database ACL/limits/locale,
cluster-role and closed-database settings, membership authority, stable keyed
fingerprints and config drift, secret/password redaction, malicious search paths,
wrong identity/major pins, caller transactions and cancellation. Private payload
recovery succeeds after the source connection closes and rejects changed config,
identity, key, fingerprint, version, unknown fields and trailing JSON. Real
catalogue reads caught and fixed the database encoding column name and the JSON
representation of PostgreSQL OIDs; numeric OID casts now decode without losing
identity. Vet and whitespace checks pass. Independent SQLC regeneration matches
all three packages byte-for-byte (thirteen files), preserving the prior generated
state and connection-fence packages. All owned test roles/databases were removed
and the bootstrap remains unmigrated. Provider IO, other PostgreSQL versions,
per-database schema/data/background inventory, sealed durable capture integration,
export/import, final dataset verification, full repository and native acceptance
remain unverified. Complete clone admission remains closed.

### Private sealed PostgreSQL copy inventories (2026-10-03)

The cluster inventory now has a private authenticated envelope and a write-once
control-plane receipt. `copyinventory.SealInventory` keeps SQL database/role
names and OIDs, scoped config values, the original inventory payload and its
random fingerprint key inside age encryption. A purpose namespace and envelope
version are authenticated with the payload. Ordinary JSON/formatted output
exposes receipt metadata without SQL names, config values, keys or ciphertext.
Passwords/password hashes remain absent from the inventory; importing roles
still requires new credentials and explicit substitutions.

The encrypted scope binds account, project, clone operation, source database,
frozen source version/backend/fingerprint, source provider/dataset, retained
snapshot identity and creation time, adopted native capture owner/provider and
creation time, common capture point and PostgreSQL major. Recovery compares
both stored and encrypted scope against scope derived from the locked frozen
receipts/catalogue. It also verifies the keyed inventory fingerprint and SQL
identity pins. A ciphertext checksum detects storage corruption; it supplies
neither source authenticity nor data-copy authority. Current/previous host age
identities recover the original envelope during key rotation. Missing keys,
tampering, another namespace/version or changed scope fail recovery without
falling back to today's source or replacing the original fingerprint key.

`project_environment_clone_postgres_inventories` stores only that ciphertext and
non-secret scope/receipt metadata. Production SQL remains generated by SQLC.
First insertion requires the capturing operation's fresh revision/status/token
and unexpired lease, the retained snapshot and the validated adopted native
catalogue. Reads require worker authority as well; authority is rechecked after
row-lock waits. Exact replay preserves the original ciphertext and timestamp.
Different metadata or randomized re-encryption cannot overwrite a committed
capture. Receipts survive authenticated snapshot/native-capture cleanup during
compensation, and the migration refuses downgrade while any receipt exists.
The clone schema registry classifies the table as operational state.

The private apid helper recovers a committed receipt before invoking a reader.
Only a missing receipt during capture can generate a new random fingerprint key,
read, seal and commit. It requires a usable sealing/decryption key before the
read, bounds the call by the worker deadline and lets state recheck ownership
before commit. Lost committed replies and worker handoffs therefore recover the
same sealed input. No live provider reader is installed: the reader must first
authenticate the exact owned capture and compute independently. Scope alone
does not select or authorize a SQL endpoint.

Private metadata safeguards live in `pkg/api/limits.go`: a complete inventory is
limited to 4 MiB, its envelope to that plus 16 KiB, and ciphertext to the envelope
plus 64 KiB. Oversized metadata returns a quota error and is never truncated.
The reader checks aggregate returned catalogue bytes and canonical payload size;
these client bounds do not qualify provider-side catalogue aggregation cost.
These are structural metadata bounds, independent from customer storage/data
allowances. SQL catalogues are still capture input, including temporary fence
settings; applying the original connection policy needs the authenticated fence
receipts and an explicit projection during import.

Verification: six inventory/envelope contracts (1.017 s), thirty-one focused
state contracts (37.529 s) and thirty-one focused apid contracts (23.760 s) pass
without skips. Four final state inventory contracts also pass after the quota
classification change (4.271 s). Real private PostgreSQL 16 reads exercise
encryption and recovery after the SQL connection closes. Envelope contracts
reject substitutions of every scope field, malformed namespaces/versions,
unknown/trailing JSON, changed SQL identities/key/fingerprint, tampered
ciphertext and oversize metadata; rotation recovers with the previous identity.
State/worker contracts exercise committed reply loss, exact replay, worker
handoff, missing keys without recapture, catalogue replacement, cancellation,
lease expiry after a receipt lock, unchanged ciphertext/timestamps, compensation
cleanup recovery and rejection of metadata as data-readiness authority. The
provider receipts in worker/state fixtures are synthetic; their actual SQL
inventory reads use the private test cluster and do not qualify provider SQL
placement. The test provider now advertises PostgreSQL 16 as well as its
existing 17 to exercise that real reader without weakening version pins.

Vet passes for inventory/state/apid; independent SQLC generation matches all
thirteen generated files across the three packages. The empty inventory table
round-trips Down/Up in the task-owned schema database; a populated downgrade
is rejected. Test overlays retain all 530 state and 446 apid production files
and replace only test files. Local disk exhaustion initially prevented apid
recompilation; final verification used a task-owned temporary RAM build/cache
workspace and stripped test linker debug symbols. No source files, SQL checks
or production declarations were removed for compilation. No provider resource
IO, public admission, deployment or native acceptance ran.

This protocol grants no source-writer release, independent target readiness,
export/import completion or public complete-clone admission. Owned capture
compute creation/recovery/cleanup and qualified SQL access, all-database export
and import, background/extension coverage, original-policy projection, final
dataset verification, PostgreSQL 14/15 bootstrap qualification, common object
capture, successful disposal, full repository/provider/native acceptance and
the remaining configuration/promotion strategies remain open.


### Private owned capture readers and retirement protocol (2026-10-03)

A provider-neutral private reader protocol now creates or discovers temporary
compute on the exact owned native capture. It retains a separate reader owner,
first dispatch time, endpoint ID and creation-time pin. Creation requires the
frozen backend/spec, retained snapshot, independently authenticated preview
fork and enabled account admission. Discovery remains available with creation
disabled and never repeats POST. These methods currently have no clone-worker
caller; a durable ownership/dispatch receipt must precede any installed caller.
They supply endpoint metadata, not SQL qualification or complete-copy authority.

The Neon adapter creates a deterministically named `read_only` endpoint on the
owned preview fork, with the frozen class, region and compute settings. It
explicitly requests passwordless access off. No source endpoint update or
snapshot finalization is involved. Unknown identity discovery requires the
current API's complete project endpoint array; absent arrays, unexpected
pagination, duplicate IDs/names and foreign placement cannot authorize creation.
An acknowledged or list-discovered endpoint is independently read by exact ID
with its creation time pinned before adoption. Lost POST replies get one
read-only discovery; unresolved absence retains uncertainty without another POST.
Known-ID absence never falls back to a mutable display name. A present endpoint
is considered available only with independently observed active/idle state,
no pending transition, enabled compute, passwordless access off and a direct
host for its exact ID. Availability is rechecked before credential use.

Discovery authenticates the original native fork from committed snapshot/fork
pins even if the retained source snapshot has since been disposed. A changed
fork creation time, restoration lineage, default/finalized branch, organization,
region or major is rejected. Creation still requires the actual retained source
snapshot. This lets compensation find uncertain reader creation without
selecting today's source or changing the common data point.

The private connection-config helper explicitly sends `endpoint_id` when
requesting an unpooled URI for `gregale_checkpoint` and `gregale_owner`. Neon's
[connection URI API](https://api-docs.neon.tech/reference/getconnectionuri)
otherwise selects a branch's read-write endpoint by default. The helper verifies
exact role/database/host/port and independently rechecks provider placement after
credential retrieval. It rebuilds a minimal verify-full TLS configuration,
stripping returned hostaddr/startup/session overrides and enforcing a read-only
session default. It does not connect to SQL, resolve OIDs or export data; the
credential-bearing configuration stays private and is not logged or stored.

Private cleanup consumes the exact reader ID/time and committed cleanup intent.
It independently authenticates the known native capture from retained receipt
pins and works without rereading a disposed source snapshot. The
[endpoint deletion API](https://api-docs.neon.tech/reference/deleteprojectendpoint)
returns a chain that may contain `suspend_compute`; autosuspend uses that action
too. Cleanup therefore retains only IDs obtained from its DELETE reply and reads
each exact operation, validating project, branch, endpoint, action and intent
time. Returned finished statuses, 204 responses and a single 404 never retire
ownership. Terminal exact operations plus independently repeated endpoint
absence are required. Discovery never dispatches DELETE or creates resources.

A lost endpoint DELETE reply cannot adopt an unrelated autosuspend from an
operation list. It can recover through independently qualified deletion of the
exact owned native capture plus repeated absence of the exact known endpoint.
[Branch deletion](https://api-docs.neon.tech/reference/deleteprojectbranch)
places computes idle; it does not promise endpoint descriptor removal. A
remaining endpoint therefore continues to hold ownership even after terminal
capture deletion. Reader deletion chains are bounded at 128 operations in
`pkg/api/limits.go`; oversized chains fail closed without truncation. Unknown
reader creation still needs discovery and durable cleanup coordination; this
increment does not prove retirement of an unobserved endpoint by name alone.

A normal managed PostgreSQL test build exposed a dependency cycle from the
inventory package back into its parent service through state. Shared errors
now live in `managedpostgres/pgerrors`; the existing service variables alias
the same identities, preserving errors.Is and quota classification. Inventory
and state depend on those sentinels without importing the service lifecycle.
No test/production files were hidden or overlaid to repair the cycle.

Verification: all 102 managed PostgreSQL contracts, 56 Neon mock-provider
contracts and six inventory/envelope contracts passed (25.360 s, 2.038 s and
3.573 s respectively), with the separately opt-in paid live lifecycle explicitly
excluded. Twelve final reader provider contracts also passed after the receipt
recovery change (1.282 s); four service reader contracts passed before it.
There were no skips in these runs. The broader suite exercises real private
PostgreSQL 16 lifecycle/clone visibility, lease expiry and encrypted inventories;
HTTP provider tests use local fixtures and make no Neon resource calls. An
initial broader run failed because the private PostgreSQL server had stopped
during interruption; authoritative pg_ctl status confirmed it was down before
restart, and the rerun passed. Vet and whitespace checks pass. No provider
credentials, paid resources, deployment or native acceptance were used.

Durable reader ownership/first dispatch/cleanup integration, quota and compute
metering, qualified SQL identity/session access, per-database export/import,
closed-database admission-policy projection, final dataset proof and the wider
configuration/object/promotion implementation remain required. No reader
provider methods or connection helper are installed into the public clone path;
complete clone admission remains closed.


### Durable temporary reader ownership (2026-10-03)

`project_environment_clone_postgres_copy_readers` now retains a reader owner
before dispatch, bound to scope derived from the locked frozen snapshot and
adopted native capture. Its owner and capture catalogue identities are unique;
ordinary metadata contains no SQL credentials or inventory plaintext. The
schema registry classifies it as operational state. Reservation requires fresh
capture lease authority, uses the existing account quota lock order and counts
all unretired readers. The caller's allowance cannot exceed the structural
64-reader account ceiling in `pkg/api/limits.go`. This is not a plan entitlement
or compute billing promise; the native capture also keeps its existing database
quota charge until qualified disposal.

Creation and deletion have separate durable dispatch claims. Only the worker
that first commits each claim receives dispatch authority. Committed reply loss,
worker handoff and unknown provider outcomes recover the original owner and
first dispatch times without another POST/DELETE. Independent discovery pins
endpoint ID and creation time; subsequent observations cannot replace them.
Availability can change with fresh endpoint metadata and grants no SQL session,
writer release, data-copy completion or resource target/readiness authority.
State rechecks account/project/revision/status/token and lease expiry after
locks, including unchanged receipt locks. Provider observation times must fit
the committed request and the control-plane clock.

Compensation first records reader cleanup intent. An undispatched reservation
can retire locally. A dispatched unknown identity cannot retire by absence or
release the native capture needed for discovery. Discovery during cleanup can
pin the original endpoint while keeping availability false. Known cleanup
retains the exact endpoint/time and bounded operation IDs, requires a separately
recorded proof before terminal retirement, and preserves retired receipts for
replay. Endpoint operation IDs require a committed DELETE dispatch. Native
capture deletion fallback IDs must exactly match the retained native cleanup
chain; the reader's trusted provider observation still needs terminal native
deletion plus exact endpoint absence. State alone does not verify remote
operations or infer endpoint absence from branch deletion.

Native cleanup may begin after a known reader enters deletion, allowing its
lost DELETE reply to recover through capture deletion. The adopted capture's
catalogue retirement and database quota release remain blocked until every
reader retires. This avoids treating an idle/missing branch or DELETE reply as
compute disposal. Unknown creation still retains discovery input and quota;
qualified retirement of a never-observed endpoint remains required before the
complete clone workflow can guarantee all uncertain-outcome cleanup cases.

Verification: thirty-two focused state contracts pass against private
PostgreSQL 16 (139.810 s, no skips), including six new reader contracts and the
existing snapshot/copy-target/inventory/registry contracts. One additional
account-quota contract passes (4.265 s), racing distinct source reservations
against one ceiling. New contracts exercise concurrent owner reservation,
first dispatch and worker handoff, unknown outcomes, immutable scope/endpoint
pins, changing availability without readiness, stale authority, lease expiry
after a receipt lock, local retirement, stored operation proof requirements,
retained native-chain fallback, capture quota holds and replay after native
catalogue retirement. Provider observations in these state fixtures are
synthetic; they do not qualify actual remote compute or SQL placement.

State vet and normal builds of state/apid pass. Independent SQLC regeneration
matches all thirteen generated files across state/inventory/connection-fence.
The new empty table round-trips Down/Up in the task-owned schema database;
PostgreSQL rejects downgrade with retained ownership, including retired rows.
The focused test overlay retains all 532 state production files and replaces
only test files. An unused import and a quota-fixture workload identity collision
were fixed before successful verification. No provider mutation or public
admission ran.

The provider and durable state protocols are ready for private worker wiring;
no production reader dispatch or SQL connection callback is installed yet.
Compute metering, unknown-creation retirement, qualified SQL/OID/session access,
export/import and the wider complete configuration/object/promotion workflow
remain open. Complete clone admission remains closed.

### Private reader worker and compensation ordering (2026-10-03)

The private capture worker now prepares temporary readers from authenticated
frozen plans, retained snapshots and adopted native captures. Before reserving
or claiming an undispatched reader it checks account admission, the frozen
backend and both creation and cleanup capabilities. The reservation allowance
uses the central structural reader ceiling; compute metering remains separate.
Every provider call is bounded by a renewed clone lease. Only the first durable
creation claim dispatches POST; requested and observed owners use discovery,
including when provisioning or account rollout is disabled. An unknown absence
never grants another POST. Availability polling preserves the endpoint/time
pins and does not populate a resource target, publish an environment or create
an inventory receipt.

Native compensation now cleans adopted-capture readers before beginning branch
deletion and rechecks reader retirement before releasing the capture catalogue
and quota. Undispatched readers retire locally. Dispatched unknown identities
must be discovered before native deletion can start. Known reader cleanup
commits its own DELETE claim once and retains independently observed operation
proof before retirement. A lost DELETE reply or dispatch acknowledgement may
recover through the exact retained native deletion chain and independently
observed endpoint absence. Pending or unavailable known-reader deletion can
allow native deletion to progress; it cannot release its catalogue or source
snapshot while the reader remains unretired. A terminal branch alone is not
endpoint disappearance. Retired reader replay performs no reader provider IO.

Verification: thirty-five focused clone/coordinator contracts pass against an
isolated PostgreSQL 16 instance (42.242 s, no skips), including seven new reader
worker contracts. They cover frozen input, admission before first dispatch,
changing availability, endpoint replacement, unknown creation, committed
reservation/dispatch/observation/proof/retirement reply loss, disabled rollout
recovery, worker handoff, exact endpoint and native cleanup chains, absence
requirements, local retirement and native quota holds. Five reader service
contracts pass (0.547 s), including creation/cleanup capability admission.
Normal managedpostgres/apid builds and managedpostgres/focused APID vet pass.
The focused APID test overlay retains
all 448 production files and replaces only test files. Provider effects are
mocked; this is not acceptance evidence for actual remote compute or SQL.

One initial assertion used an older synthetic fixture without a valid public
configuration capture; it was removed in favor of the existing coordinator
checkpoint contracts. A disk-backed regression attempt then exhausted local
test-database space. Verified inactive task databases were cleaned and the
successful complete focused run used an isolated temporary RAM volume.

Reader preparation remains a private seam; the public coordinated capture
gate is unchanged. Qualified SQL/OID access, export/import, compute metering,
never-observed creation retirement and the complete configuration/object/
promotion workflow remain required.

### Private authenticated SQL inventory access (2026-10-03)

The service now borrows an already owned, endpoint/time-pinned reader through a
synchronous provider callback. Creation/account rollout gates do not disable
recovery reads. Frozen backend and source identity remain mandatory, and the
provider deadline is authoritative for the callback. Missing, repeated or
malformed callback authority, missing OIDs and mismatched PostgreSQL majors are
rejected. Callback failures cannot be discarded by a successful provider return.
This seam neither persists connection configuration nor supplies a dataset
completion receipt.

Neon's implementation uses the exact direct readonly endpoint and minimal
verify-full connection configuration, with platform-owned read-only and
pg_catalog startup settings. The identity query explicitly qualifies catalogue
relations and functions; a caller's search path cannot substitute them. Before
and after the trusted read it checks an idle, non-busy connection, database,
current/session role, PostgreSQL major, read-only mode and nonzero database/role
OIDs. It independently rechecks provider placement around the read and rejects
changed SQL identity. Connection failures are redacted, and normal, rejected
and partially established connections close under a bounded cleanup context.

The private APID inventory reader binds the callback to fresh reader/snapshot/
adopted-capture records and the exact inventory scope. A missing inventory reads
cluster metadata through the authenticated connection, then seals it under the
original capture scope and generated fingerprint key. A committed inventory
reply loss reopens that exact sealed receipt without another SQL/provider read.
Unavailable readers and wrong-major SQL cannot supply inventory. Cluster
inventory is still metadata input to export planning, never exported data,
target readiness or writer-release authority.

Verification: thirty-seven focused APID clone/coordinator contracts pass against
isolated PostgreSQL 16 (31.662 s, no skips), including two new SQL inventory
composition contracts. Twenty-nine normal provider/inventory contracts pass
with no skips: eight service contracts, fifteen Neon HTTP/local SQL contracts
and six inventory/sealing contracts. Real local SQL exercises identity, OIDs,
read-only mode, open/closed transactions, private settings and sealed recovery;
provider metadata is mocked. No successful live Neon SQL connection, remote
capture acceptance or copied-dataset acceptance is claimed. Independent SQLC
regeneration matches all thirteen generated files. Normal managedpostgres,
Neon, inventory and APID builds and provider/focused APID vet pass. The focused
APID test overlay retains all 449 production files; its versioned-package import
selection was repaired before the successful run.

Private capture preparation can now read a sealed cluster inventory through
its owned reader. Public complete clone admission remains closed. Per-database
export/import, configuration/role/extension/background coverage, temporary
closed-database admission projection, compute metering, unknown-creation
retirement and the wider common-point/object/configuration/promotion work remain
required.

### Private complete database export plan and encrypted dump (2026-10-03)

Cluster inventory now produces private export requirements for every catalogue
database, including both templates, closed databases and the reader/maintenance
database. Roles, memberships, database/role settings, tablespaces, ownership,
ACLs, locale and connection limits remain retained input. No name or provider
category is filtered out. An authenticated fence receipt may supply the original
ALLOW_CONNECTIONS value only when database name, OID and owner OID match a
closed captured entry. That projection changes logical admission only. The
captured inventory fingerprint remains a reference; it does not authenticate a
new projected payload. Caller mutations cannot change the retained plan. Any
prepared transaction blocks all ordinary database dump requirements until its
copy strategy is qualified.

The new private `copyarchive` prototype streams a custom-format `pg_dump`
directly into age encryption. `pg_dump` exports one database; cluster globals
need the separate retained catalogue input.
[PostgreSQL documentation](https://www.postgresql.org/docs/17/app-pgdump.html).
The envelope embeds the exact operation/capture scope,
inventory fingerprint, complete selected database metadata and reader role OID
inside the envelope. It returns ciphertext length/hash and dump byte count only
after successful dump execution, post-dump SQL identity verification and age
stream closure. A closed captured database remains an explicit unsupported
export requirement until an owned admission projection is qualified; it is
never silently skipped. The borrowed source connection must be idle, read-only
and match database name/OID, current/session role, reader role OID and PostgreSQL
major before and after the dump. Independent provider placement remains the
caller's responsibility.

The executable path is absolute and its PostgreSQL major must match the source.
Commands use fixed custom-format/no-password arguments with no schema, data,
owner or ACL filters. Database names are percent encoded literally. The child
receives a minimal libpq environment with ambient password-file lookup disabled;
passwords are absent from arguments and diagnostics. Any dump stderr, execution
failure, invalid dump magic, cancellation,
output failure or byte-budget overrun prevents a successful receipt. Central
structural limits are 1 TiB per plaintext dump, 64 KiB version output and a
10-second connection timeout, independent from actual storage admission or
billing. Network export currently requires PostgreSQL 16+ system-root
verify-full TLS; PostgreSQL 14/15 network trust and custom trust roots remain
unqualified. Custom TLS callbacks, client certificates and protocol constraints
are rejected rather than silently discarded. Unix-socket export supports local
qualification.

Opening an archive strictly compares every encrypted scope/database/reader pin,
rejects unknown/trailing header fields and checks custom dump magic. The stream
must be consumed to EOF for full age authentication before import authority.
Atomic artifact reservation/publication, receipt recovery, retention, metering,
private resource classification, credential regeneration, cluster-global import,
closed-database projection and a production importer are still required. This
package is not installed in the complete-clone worker or public command.

Verification: ten inventory/export-plan contracts and six archive contracts pass
against isolated local PostgreSQL 16 with no skips (2.215 s and 5.047 s).
The real export fixture uses a non-superuser owner and a database name containing
spaces, a slash, URI punctuation and Unicode. Its encrypted archive is consumed
to EOF and restored into a fresh independent database with normal `pg_restore`
owner/ACL behavior. Rows, bytea, JSON, an enum, view, index, function/trigger,
sequence state, default/table grants, comments, large-object bytes/grants and
target-only writes are verified, while source contents remain unchanged. Other
contracts cover all header substitutions, key rotation, malformed headers,
tampered/truncated age tails, source identity/transaction/admission rejection,
dump warnings, cancellation, storage errors and quota failure. One initial
verification query used an unavailable large-object privilege function; the
successful run checks its catalogue ACL instead. This is local dump coverage,
not live provider, common-point or complete stage acceptance. Normal inventory,
archive, Neon and APID builds and inventory/archive vet pass.

### Private retained archive streaming and readback (2026-10-03)

A neutral archive backend now streams the finalized encrypted dump directly to
an atomically consuming storage writer. The helper permits only an exact
operation/owner UUID key in `postgres-copies/<operation>/<owner>.age`. A failed
producer closes the stream with an error; the driver must not publish partial
input. The producer must honor cancellation. A write reply alone does not
supply a receipt: the helper independently reads the stored artifact to EOF,
authenticates every encrypted header pin and the age payload, enforces byte
budgets, and computes ciphertext length/hash and plaintext dump length. A lost
reply after storage commit can recover from those exact bytes. Any corrupt,
truncated, appended, unavailable or rebound artifact prevents a receipt.
Wrapped producer quota failures retain their stable error classification without
exposing private diagnostics.

Central structural bounds are 2 TiB per ciphertext artifact, 128 TiB of reserved
archive bytes per account and 4,096 archive owners per account. The existing
1 TiB plaintext bound remains. These are private safety ceilings, independent
of customer storage entitlement, billing or actual driver capacity; trusted
callers must supply smaller admitted budgets as appropriate. Driver selection,
frozen configuration/namespace authentication, create-only storage publication,
cleanup and metering still need qualification before public workflow wiring.

Verification: all ten normal archive contracts pass against local PostgreSQL 16
with no skips (23.890 s), including four new retained-artifact contracts. A real
local dump streams through storage, receives independent readback verification,
and recovers the exact receipt after its source connection closes. Existing
real dump/restore isolation coverage also passes. A subsequent focused contract
passes the added wrapped-quota case. The memory storage fixture models atomic
stream consumption and committed write reply loss; it does not qualify a live
remote driver. Archive vet passes. Full TOC/global import and dataset readiness
remain separate requirements; the public complete-clone gate remains closed.

### Durable private PostgreSQL archive ownership (2026-10-03)

The new archive ledger reserves one owner for each operation/source/database OID
before storage IO. It pins the original inventory scope/fingerprint, encryption
recipient, artifact driver identity/configuration fingerprint, exact owned key
and byte reservation. New reservations authenticate the adopted native capture;
recovery authenticates the retained inventory and operation without requiring
a live source or capture catalogue. Concurrent reservations serialize on the
account and retain one owner. Every reservation, including a retained or
uncertain upload, consumes count and byte admission until qualified cleanup is
implemented. Existing ownership can replay when admission is reduced, but its
key, storage or byte pins cannot change.

Only the first fresh-lease claim changes reserved ownership to uploading and
authorizes dispatch. A lost claim reply supplies no second dispatch. Verified
receipts record only after dispatch, bind the exact database/scope/fingerprint,
and remain immutable on replay. State rechecks lease authority after locks and
before transaction commit. Retained ciphertext metadata is a transfer receipt;
it does not prove cluster-global import or complete dataset readiness. Recovery
reads remain possible during compensation, which cannot dispatch new uploads.
Downgrade refuses any retained owner, including an undispatched reservation.
All twenty new columns have an explicit operational schema policy.

Verification: five new state contracts pass against isolated PostgreSQL 16 with
no skips (7.283 s). They exercise concurrent reservation, first dispatch and
worker handoff, stale authority and lease expiry after a receipt lock, immutable
pins and receipts, held count/byte admission, reduced-admission replay, retained
inventory authentication and recovery after capture catalogue retirement. The
migrated schema coverage contract separately passes (2.948 s). The empty
migration round-trips Up/Down/Up, and populated ownership blocks Down; complete
migrations run in the state fixtures. Independent SQLC regeneration matches all
thirteen generated files across state/inventory/connection-fence. Normal state
build and focused state vet pass. Test overlays replace only test files and keep
all production sources. Qualified retirement, retry generations, storage
entitlement and usage metering remain required before public admission.

### Private retained archive worker recovery (2026-10-03)

The private APID archive worker selects the database only from immutable export
requirements derived from the original sealed inventory. It uses the retained
owner's encryption recipient and storage pins on every retry. Only a first
durable upload claim calls the trusted export producer; uploading and retained
owners use independent readback without another dump or storage write. Retained
receipt replay can operate without a source connection or producer and survives
key rotation when the original decryption key remains available. Missing original
keys or changed artifact configuration prevent IO. An unknown claimed upload
with no stored artifact keeps its ownership and byte reservation; it cannot
infer absence as permission to overwrite or release admission. Only fresh
lease authority can commit the verified receipt.

Verification: all thirty-nine focused APID clone/coordinator contracts pass
against isolated PostgreSQL 16 with no skips (25.665 s), including two new archive
worker contracts. They exercise committed reservation, claim, upload and receipt
reply loss, original-key recovery after worker handoff, storage drift, corrupt
ciphertext and unknown write holds. Worker fixtures use real local storage and
SQL identity checks with synthetic dump output; the archive package separately
verifies actual PostgreSQL dump/restore data. Normal managedpostgres/Neon/archive/
state/APID builds and archive/focused state/APID vet pass. The APID test overlay
keeps all 450 production source files and replaces only test files. Initial
state/APID disk-backed attempts exhausted host disk space; successful reruns
used a task-owned RAM volume and isolated PostgreSQL instance. Missing schema
classification was corrected before the successful coordinator run.

This helper is not installed in the public coordinator. Selected-database SQL
access, closed-database admission, archive storage configuration freezing and
create-only publication (including the OCI namespace), qualified artifact
cleanup/retry/metering, all database/global imports and independent dataset
completion remain required. Source writer closure/common-point capture,
object/configuration coverage, production-preserving promotion/rollback and
native/provider acceptance remain part of the complete objective. No public
clone readiness or production stage promotion is claimed.

### Private selected-database reader SQL (2026-10-03)

Owned reader SQL can now borrow a selected catalogue database with its original
OID and reader-role OID. The private request comes from a retained immutable
export requirement. Before provider IO it checks original source/backend,
snapshot/capture identity and all capture times against the frozen definition
and durably pinned reader. Missing or substituted database/role identity,
invalid callback authority, a changed backend or unsupported provider rejects
the borrow. Existing-reader reads remain independent of creation/account rollout
and retain the provider deadline. A closed captured database still returns an
explicit unsupported requirement until admission projection is qualified.

Neon obtains credentials through the fixed maintenance database on the exact
direct readonly endpoint, then selects the retained database name literally in
pgx configuration. SQL names containing spaces, slashes, URI punctuation,
Unicode or a newline cannot become URL query/options or change the endpoint.
Connection establishment retains minimal verify-full trust and platform-owned
readonly/search-path startup settings. Before and after the synchronous callback,
SQL checks PostgreSQL major, name/OID, current/session role, original reader-role
OID, read-only mode and idle transaction state. Provider placement is rechecked
around the borrow, including comparison with the originally connected endpoint
host. Successful, rejected and partially established connections close under a
bounded cleanup context. Neither this request nor its callback proves imported
data or complete stage readiness.

Verification: six normal service contracts pass with no skips (0.574 s), including
three new selection contracts and the original SQL callback contracts. Six normal
Neon contracts pass against local PostgreSQL 16 with no skips (0.982 s), including
three new contracts. They check placement before connection, literal name
selection, exact OIDs, post-read read-write/transaction/host drift, callback
failures, credential redaction and connection closure. HTTP/provider metadata is
mocked and the successful SQL connection uses an isolated Unix socket; remote
Neon SQL placement/trust acceptance is not claimed. The test administrator's
read-only setup was corrected before successful fixture creation. Normal
managedpostgres/Neon builds and vet pass. Closed-database
projection, provider privileges across every database/global, private-resource
classification and PostgreSQL 14/15 network export remain unqualified.

### Private owned-reader database archive composition (2026-10-03)

The archive worker can now compose its durable ownership protocol with the
selected-database reader. It authenticates fresh reader/snapshot/adopted-capture
records, original inventory scope, source version and capture identity before
borrowing SQL. The selected requirement is supplied only by the immutable export
plan. Dump encryption finalizes inside the synchronous borrow, but the producer
returns success only after the provider's post-dump SQL and placement checks.
If those checks reject a completed dump, the upload pipe ends with an error and
atomic storage cannot publish its bytes. The uncertain owner and byte reservation
remain held. Retained archive recovery invokes neither this producer nor reader
SQL and can succeed without the managed PostgreSQL service or dump executable.

Verification: all forty-one focused APID clone/coordinator contracts pass against
isolated PostgreSQL 16 with no skips (33.192 s), including two new composition
contracts. One streams a real dump of a database distinct from the inventory
connection, with a literal space/slash/URI punctuation/Unicode/newline name.
Full encrypted readback verifies its original pins; `pg_restore` renders the
custom dump and confirms the selected table/row. Recovery uses the exact
retained bytes with no new SQL or storage write. The other injects a provider
rejection after a successful real dump and verifies absent publication, retained
upload ownership and no repeat dump on retry. Actual reader permissions and
local SQL are tested; provider metadata/native capture are synthetic, and remote
compute, immutable common-point or complete-stage acceptance is not claimed.
The local trusted-socket fixture was given nonempty connection material matching
the provider contract after a passwordless fixture caused a libpq password-file
warning. Passwordless export qualification remains separate.

Normal state/managedpostgres/Neon/archive/APID builds and provider/focused APID
vet pass. The test overlay keeps all 451 APID production source files and replaces
only test files. Complete clone admission remains closed. Closed-database
projection, all database/global imports, final independent dataset completion,
frozen/create-only artifact storage and qualified cleanup/retry/metering,
source writer closure/common-point capture, object/configuration coverage,
production-preserving promotion/rollback and native/provider acceptance remain
required for the full one-command stage workflow.

### Private authenticated archive staging (2026-10-03)

Retained database archives can now be fully authenticated into a private local
spool before any SQL restore. Staging checks the original encrypted header,
complete age stream and exact retained ciphertext hash/length and plaintext
count. It reads storage once and retains an immutable requirement copy, so a
later key replacement or caller metadata mutation cannot change the input.
The spool is mode 0600 and unlinked before its first plaintext write. Failure,
close or process termination releases it without leaving a named plaintext file.
The caller must freeze storage and reserve local capacity within the existing
central archive ceilings; this primitive supplies no durable dispatch authority.

Verification: three staging contracts and all existing archive contracts pass
against isolated PostgreSQL 16. Tests cover corrupted/truncated/appended tails,
receipt and SQL identity drift, declared and actual byte budgets, cancellation,
storage/output failures, immutable input and plaintext cleanup. A failed storage
read also closes a returned body. Stage admission remains closed.

### Private target restore execution (2026-10-03)

An authenticated staged archive can now be restored into a separately pinned SQL
target with the matching PostgreSQL client major. The target descriptor binds
original scope, independent owner/project/data/endpoint identities and creation
times, selected SQL name/OID and role name/OID. SQL authentication requires an
idle read-write connection and checks current/session identity before and after
the write. A mandatory synchronous callback separately authenticates provider
placement and durable dispatch authority. Numeric OIDs may coincide in different
clusters; SQL identity alone cannot establish physical isolation. A canonical
descriptor digest pins private names without writing them into the import ledger.

The fixed restore preserves object ownership and ACLs and uses one transaction
with exit on error. It has no object filters or destructive cleanup. Database
creation is a separate step: PostgreSQL's create option can select the archived
database name and also handles database comments, database/role settings and
database grants. Those original catalogue/global requirements still need their
own qualified materialization. See the [PostgreSQL 16 restore documentation](https://www.postgresql.org/docs/16/app-pgrestore.html).

Each staged object allows one attempted write. Process failure, cancellation,
diagnostics or a rejected post-write SQL/provider check returns no execution
receipt. A rejection after commit does not prove rollback; the caller must retain
uncertain ownership. Successful execution also supplies no independent dataset,
global configuration or complete-stage readiness evidence.

Verification: all nineteen archive/staging/restore contracts pass against isolated
PostgreSQL 16 with no skips (8.612 s), including six restore contracts. An ordinary
database owner restores rows, bytea/JSON/enum values, sequences, views, indexes,
functions/triggers, comments, default/table/large-object grants and large-object
contents. Target-only writes leave the source unchanged. A late large-object OID
collision rolls back earlier schema writes and preserves the existing target
object. A post-commit placement rejection proves that committed SQL cannot become
a completion receipt or a second attempt. Other checks reject target/role/scope
drift, read-only/open-transaction connections, placement mutation, wrong/missing
tools, secret diagnostics and cancellation. Local SQL placement is qualified;
provider identities are synthetic and remote provider acceptance remains required.

### Private durable per-database import ownership (2026-10-03)

Each retained archive can now reserve one import owner bound to its exact archive
owner/hash and the independently prepared target reservation. The immutable
target descriptor digest includes SQL and provider pins; raw SQL names and
credentials are absent from this ledger. Original inventory/archive and native
capture/target/catalogue ownership are authenticated under the operation lease
and shared transaction locks. Reservation requires retained input and a prepared
independent target. One import per already charged archive inherits its count
bound, and target admission remains charged separately.

The state transitions are reserved → importing → executed. Only the first
committed claim grants SQL dispatch. Concurrent claims, worker handoff and lost
claim replies cannot repeat an uncertain write. Failed or unknown outcomes retain
the importing owner. A trusted actual execution receipt must match the complete
retained input and target digest before the executed transition. A committed
receipt can replay without changing its timestamps or granting another dispatch.
These rows do not set database data-resource identity, observed generation,
resource completion, target visibility or stage readiness. Independent recovery
of uncertain SQL commits and qualified ownership retirement remain required.

Verification: seventeen focused state archive/target/import/schema contracts
pass against isolated PostgreSQL 16 with no skips (162.848 s), including four new
import contracts. They cover concurrent reservation/claim, worker handoff and
stale leases, exact input/SQL/provider descriptor substitution, immutable source
configuration, prerequisite rejection, private-name exclusion and absence of
publication. The migration round-trips an empty table with identical columns and
constraints and refuses rollback with a reserved owner. The migrated schema
registry recognizes the new operational table. SQLC regeneration matches all
thirteen generated files. Normal state/managedpostgres/Neon/archive/APID builds
and state/archive vet pass. A host disk-space failure made the first state log
incomplete; the complete successful rerun uses a task-owned RAM volume.

The restore primitive and durable ledger are still private and are not connected
to public clone admission. Owned target SQL/provider borrowing, all database/global
and closed-database materialization, complete dataset verification, frozen artifact
storage and cleanup/metering, writer closure/common-point capture, complete object
and configuration coverage, production-preserving promotion/rollback and native/
provider acceptance remain required for the full one-command stage workflow.

### Private owned-target SQL borrowing (2026-10-03)

A trusted worker can now borrow authenticated SQL for an already prepared,
independently owned target. The request binds the original source/capture scope,
target owner/project creation time, root branch, direct read-write endpoint and
creation time, and selected database/role names and OIDs. The service requires one
completed synchronous callback, preserves callback failures and enforces its
deadline even when a provider supplies another context. Repeated, late, missing
or unfinished callbacks cannot produce successful borrowing. Existing ownership
recovery remains independent of new-resource rollout and account admission.

Neon authenticates native capture and independent project topology around SQL
access. Credentials come from the fixed provisioned bootstrap database and role;
the selected database name is assigned literally after rebuilding a minimal
verify-full connection configuration. Provider URI options cannot choose another
host, endpoint or startup policy. SQL requires an idle read-write session with
exact major, database/role OIDs and current/session role. Provider placement and
SQL identity are checked again after the callback, and every opened connection
is closed. Shared reader/target closure preserves the existing bounded cleanup.
Only the existing platform bootstrap role is supported at this boundary.

Verification: all ten focused service contracts (0.558 s) and fourteen Neon
contracts (4.896 s) pass with no skips, including seven new target SQL contracts.
They cover authority/scope substitution, callback protocol and cancellation,
provider metadata/credential/endpoint faults, literal unusual database names,
ordinary owner writes, SQL identity/transaction drift, post-write placement
rejection and partial-connection cleanup. Original target preparation/discovery/
cleanup and reader SQL contracts pass. Normal state/managedpostgres/Neon/archive/
APID builds and provider vet pass. HTTP/provider metadata is synthetic and actual
SQL uses an isolated PostgreSQL 16 Unix socket; remote Neon trust/placement and
native acceptance remain unqualified. Bootstrap discovery, durable SQL identity
pins and all database/global provisioning remain required. Public clone admission
is still closed for database/object copies.

### Private owned-target database import composition (2026-10-03)

The private single-database worker now composes authenticated archive staging,
durable import ownership and provider-owned target SQL. It selects requirements
only from the immutable every-database export plan, checks fresh retained storage
and original snapshot/adopted-capture/independent-target records, and authenticates
the complete encrypted input before borrowing SQL. The first durable claim is
made inside the authenticated callback. Restore boundaries recheck the exact
borrowed connection, immutable target descriptor and live importing owner under
the lease. The execution receipt is recorded only after the enclosing provider's
post-write SQL and placement checks succeed.

A lost claim reply or unknown post-write outcome holds the importing owner and
cannot re-dispatch SQL. A previously committed execution receipt can replay
without SQL or another storage read, including after managed-service/tool removal.
Replay is a historical command fact; it supplies no current dataset or placement
proof. The caller must still qualify target SQL pins, provision all original
databases/globals, freeze artifact storage and reserve local spool capacity.

Verification: all forty-four focused APID clone/coordinator contracts pass against
isolated PostgreSQL 16 with no skips (86.312 s), including three new composition
contracts. They retain a real encrypted selected-database dump, restore it into
an isolated target and verify that target-only changes leave source rows intact.
Committed-but-lost record replies replay without another dump/storage/SQL call.
Provider rejection after a real SQL commit retains uncertain import ownership;
retry performs no staging or SQL. Damaged input, insufficient spool capacity and
committed-but-lost claim replies cannot execute target writes. The composition
fixture uses an administrator; ordinary-owner privileges are qualified separately
by archive and provider SQL contracts. Provider/native metadata remains synthetic.

Normal state/managedpostgres/Neon/archive/APID builds and focused APID/provider
vet pass. The APID overlay keeps all 452 production source files and replaces
only test files. Together with target SQL service/provider verification, 68
focused contracts pass, including ten new contracts. Public clone admission and
coordinator wiring remain unchanged. Complete database/global materialization,
independent dataset proof and uncertain-commit recovery, qualified cleanup,
writer closure/common-point capture, object/configuration completeness,
production-preserving promotion/rollback and native/provider acceptance remain
required for the full one-command stage workflow.

### Private independent-target bootstrap SQL discovery (2026-10-03)

An already prepared independent target can now be inspected to discover its
fixed provisioned bootstrap database/role names and OIDs, root data-resource
identity and direct endpoint identity/creation time. The request requires a
pinned independent project and original adopted capture. It supplies no
customer-selected SQL name or OID. The service uses the frozen backend and
provider deadline and validates the observation before returning it. Owned
inspection remains available when new provisioning/admission is disabled.
Formatted and ordinary JSON observations expose neither SQL names nor physical
identities; durable names must be stored only in sealed private metadata.

Neon performs only GET discovery and authenticated SQL catalogue reads. It
authenticates the native capture, independent root topology, endpoint creation
time, readiness and password-only direct access before connecting. It shares
the selected-target minimal credential configuration, so untrusted URI options
cannot alter endpoint or startup policy. The fixed provisioned database/role,
major, OIDs, current/session identity and idle read-write SQL state are observed
around a fresh provider placement check. All opened connections are closed,
including partial connection failures. Inspection performs no DDL or restore and
grants no durable SQL dispatch or dataset readiness.

Verification: twelve service contracts (0.569 s) and seventeen Neon contracts
(2.624 s) pass with no skips, including five new discovery contracts and all
previous target/reader SQL and target preparation/discovery/cleanup contracts.
The new contracts cover missing ownership, frozen-backend substitution, malformed
or substituted private pins, credential/topology/time drift, cancellation, real
ordinary-owner OID discovery, read-only/open/closed sessions, wrong database/role/
major, post-SQL drift and partial-connection cleanup. The first SQL fixture used
a customer-selected slash-containing name as a fixed provider bootstrap name;
the successful fixture now uses a valid task-owned provisioned bootstrap name.
Literal customer-selected database names remain qualified by the restore-borrow
contracts. Actual SQL runs on isolated PostgreSQL 16; HTTP/provider/native
metadata is synthetic. Normal state/managedpostgres/Neon/archive/APID builds and
provider vet pass. SQLC and migrations are unchanged.

These observations still require durable sealed ownership and independently
qualified role/database/global provisioning before writes. Public clone admission
remains closed. Full common-point capture, independent dataset completion,
uncertain-commit recovery and cleanup, complete object/configuration copying,
production-preserving promotion/rollback and remote/native acceptance remain
required for the full one-command stage workflow.

### Private encrypted target SQL pins (2026-10-03)

The independently observed bootstrap target descriptor can now be sealed in a
separate authenticated age namespace. SQL names/OIDs and endpoint pins remain
inside encryption; the outer original source scope, target owner/project/time,
descriptor fingerprint, recipient and ciphertext hash bind durable ownership.
Recovery verifies the entire receipt, strict versioned envelope, namespace and
canonical all-field target digest before returning private pins. Only identities
matching the committed recipient are tried, so a substituted recipient cannot
borrow another available rotation key. Existing central copy envelope/ciphertext
ceilings bound this metadata; no new quota or credential material is introduced.
The descriptor supplies neither SQL dispatch nor current placement/dataset proof.

Verification: all 22 archive contracts pass against isolated PostgreSQL 16 with
no skips (11.765 s), including three new sealing contracts. They qualify exact
private-name/OID recovery, rotation, timestamp normalization, redacted output,
outer scope/owner/project/recipient/hash substitution, damaged/truncated ciphertext,
wrong namespaces, malformed/trailing envelopes, inner SQL/provider pin drift and
both ciphertext and decrypted-envelope budgets. Existing actual dump/stage/restore
contracts pass. Public complete clone admission remains closed.

### Private durable target SQL pin ownership (2026-10-03)

One write-once encrypted bootstrap identity is now retained for each prepared
independent target. The record binds the original inventory fingerprint/scope
and exact target owner/project creation time. Operation, native capture, frozen
reservation, target catalogue and retained inventory are locked together under
the live worker lease. The first committed bytes and key win; even an equivalent
re-encryption cannot replace them. The existing charged target reservation bounds
record count, and existing central encrypted-metadata ceilings bound each record.
SQL names and SQL OIDs are absent from plaintext ledger columns. These records
set neither data-resource identity nor observed generation/resource/stage readiness.

Verification: all 21 focused archive/import/target/schema state contracts pass
against isolated PostgreSQL 16 with no skips (55.368 s), including four new pin
ownership contracts. They cover concurrent first capture, exact receipt replay,
worker handoff/stale leases, scope/source-inventory/physical/SQL descriptor and
ciphertext/key substitution, prepared-target prerequisites, byte budgets, damaged
committed bytes, immutable source configuration and absent target publication.
The new migration round-trips an empty table with identical columns/constraints
and refuses rollback with any committed pins. The schema registry classifies the
new table as operational. The schema snapshot is regenerated from real migrations;
independent SQLC regeneration matches all thirteen generated files. Normal state/
managedpostgres/Neon/archive/APID builds and state/archive vet pass.

The first database verification run was refused by the test harness because
schema generation had migrated its shared bootstrap database. The successful
complete run uses a fresh unmigrated bootstrap and isolated migrated test
databases. Full-repository/lint/native/provider acceptance is not claimed. Pin
retirement and account/project deletion integration remain required along with
complete database/global provisioning and independent dataset completion.

### Private target SQL pin worker composition (2026-10-03)

The private worker now composes retained source inventory, authenticated target
bootstrap inspection, encryption and durable pin ownership. It validates the
frozen source/version/backend against the original inventory, then checks fresh
snapshot/adopted-capture/prepared-target records before inspection. Shared SQL
preparation also checks the exact retained snapshot ID, point and creation time
against sealed inventory scope; the selected-database import worker uses that
same preparation. No provider observation can replace a committed pin receipt.

A committed-but-lost record reply recovers the original encrypted descriptor
without another inspection. Rotation uses the original retained recipient key
and requires neither the managed service nor a current encryption recipient.
Missing decryption keys or damaged committed bytes preserve ownership and return
an error. In-flight target catalogue changes cannot become a committed receipt.
This operation performs identity capture only: complete database/global
provisioning, durable DDL dispatch and final dataset proof remain separate work.

Verification: all 47 focused APID clone/coordinator contracts pass against
isolated PostgreSQL 16 with no skips (106.269 s), including three new worker
contracts. They read real target SQL OIDs, encrypt/recover exact pins, inject a
committed lost reply, hand off the worker, rotate keys and recover with provider
inspection disabled. Other cases reject absent/unreadable keys, source-version
and stale-lease substitution, provider/observation failure, damaged ciphertext and
in-flight catalogue ownership drift without another capture or publication.
Physical provider/native capture metadata is synthetic; the composition fixture
uses administrator SQL. Ordinary-owner/fixed-bootstrap privileges and local
provider SQL placement are qualified separately by their existing contracts.

The APID overlay keeps all 453 production source files; the state overlay keeps
all 538 production source files. Both replace only test declarations. Together
with archive and state verification, 90 focused contracts pass, including ten new
contracts. Normal state/managedpostgres/Neon/archive/APID production builds,
state/archive vet and focused APID vet pass. Complete verification explicitly
provides the fixed PostgreSQL dump executable; earlier exploratory runs omitted
that test input and their skipped results are excluded. Public clone admission
and coordinator wiring remain closed/unchanged. Full database/global
materialization and dataset verification, unknown-write recovery and ownership
retirement, writer closure/common-point capture, object/configuration completeness,
production-preserving promotion/rollback and remote/native acceptance remain
required for the full one-command stage workflow.

### Private transactional target role seeds (2026-10-03)

The immutable every-database export plan now supplies a deeply copied, private
role catalogue. A role seed plan explicitly accounts for every captured role,
including predefined, provider and private maintenance roles. Existing resources
require pinned target OIDs and identical names/attributes/configuration; matching
a name cannot adopt ownership. Different provider/private semantics require a
qualified mapping strategy. Source and target majors and bootstrap SQL pins must
match. The original role plan, including the pre-write target catalogue, is sealed
in a separate age namespace and recovered with its original recipient, inventory
fingerprint, source scope and target fingerprint. Today's source or target cannot
rebase that encrypted input. Existing central metadata ceilings apply.

New roles retain captured attributes, expiry, connection limit and role settings,
with LOGIN deferred and PASSWORD NULL. Existing mapped roles remain unchanged.
Role memberships/grantors, database-scoped settings, comments/security labels,
parameter ACLs and fresh stage credentials/activation require subsequent global
materialization. No source password or password hash is copied. Unavailable role
or parameter authority fails the whole transaction; settings are never omitted.
PostgreSQL 16 requires explicit SET authority for custom placeholders, including
when an ordinary CREATEROLE owner can create the role. PostgreSQL's own dump code
also treats list settings such as search_path specially; the temporary SQL helper
parses their stored quoted lists and individually quotes each element. This
preserves comma-containing, quoted, long and empty entries.
See [PostgreSQL GUC permission checks](https://github.com/postgres/postgres/blob/REL_16_STABLE/src/backend/utils/misc/guc.c)
and [dump configuration handling](https://github.com/postgres/postgres/blob/REL_16_STABLE/src/bin/pg_dump/dumputils.c).

SQLC owns all statements; dynamic identifiers and literals are quoted only inside
fixed SECURITY INVOKER temporary functions. An advisory transaction lock serializes
workers on the bootstrap database, and live dispatch authority is checked again
after waiting and before commit. Role creation and a private target receipt commit
atomically. Exact retries verify the original plan, baseline and created role
identities/attributes, retaining the original OIDs and timestamp without role DDL.
A lost reply after commit returns no successful receipt; a fresh authenticated
session can recover the exact sealed plan and committed target receipt. Catalogue
drift, shared protocol storage and substituted receipts fail closed. This is role
seeding only, not a credentials, complete globals, dataset or stage-ready receipt.

Verification: seven new role contracts pass against two independent local
PostgreSQL 16 clusters using ordinary CREATEROLE/CREATEDB owners (1.966 s), including
explicit narrow SET authority for the custom setting fixture. They qualify private
encryption/rotation and strict recovery, complete role dispositions, exact settings
and password reset, unchanged source roles, concurrent first commit, stale waiting
workers, lost committed replies, permission/precommit rollback, catalogue drift and
receipt/ACL substitution. All ten inventory and twenty-two archive contracts also
pass with no skips (1.004 s and 5.369 s), including real encrypted dump/restore.
An existing inventory test import cycle was fixed by using the shared pgerrors
identities directly. Normal production builds and inventory/role/archive vet pass.
The SQLC check now stages and compares every configured generated package; the
actual make gate passes, including all seventeen generated files.

This private PostgreSQL 16+ primitive is not wired into public cloning. Durable
control-plane ownership of the sealed role plan and provider worker composition,
role/global completion and retirement, database creation/import/verification,
PG14/15 qualification and remote provider acceptance remain required. Public
database/object clone admission stays closed. Complete writer closure/common-point
capture, object/configuration coverage, production-preserving promotion/rollback
and native acceptance remain required for the full one-command stage workflow.

### Private durable role plan ownership (2026-10-03)

Each independently prepared target can now retain one write-once encrypted role
plan before role DDL. The ledger binds the original inventory fingerprint and
ciphertext hash, original bootstrap descriptor fingerprint and ciphertext hash,
source scope and exact target database owner. It locks the live operation, native
capture, frozen reservation, target catalogue, inventory and bootstrap pin record
before accepting input. The actual stored operation must still be capturing and
the independent target must be prepared. The first ciphertext, recipient and
capture timestamp win; equivalent re-encryption cannot replace them. The existing
charged target bounds record count and central encrypted-metadata ceilings bound
each record. Plaintext SQL names, SQL OIDs and role settings are absent.

Lease-bound recovery reads the original encrypted record. Substituting either
committed prerequisite's ciphertext fails even when its logical fingerprint is
unchanged. Live source desired configuration cannot rebase the input. Recovery
and capture check current authority again before commit. This ledger records
private plan ownership only: it performs no provider IO, role DDL or dataset
publication and sets no target data-resource identity or observed generation.

Verification: all 28 focused state archive/import/independent-target/pin/role-plan
and schema coverage contracts pass on isolated PostgreSQL 16 with no skips
(65.819 s), including four new role-plan contracts. They qualify concurrent first
capture, original encrypted plan recovery/key rotation, worker handoff and stale
leases, source/target/scope and ciphertext/recipient replacement, prerequisite
ciphertext replacement, fresh stored-phase rejection, original-pin prerequisites,
metadata bounds, damaged committed bytes and absent publication. The fixture uses
real catalogue/encryption data with synthetic provider/capture placement; it does
not execute target role DDL. Actual isolated role creation is qualified by the
separate role contracts. The migration round-trips an empty ledger with identical
columns/constraints and refuses rollback with committed ownership. The prerequisite
pin migration test now follows the child-before-parent rollback order.

The test overlay preserves all 540 production state files and selects original
internal/external test declarations by their dependency closure and package scope.
Earlier exploratory failures from a shared test error variable are excluded from
this complete run. Normal state/managedpostgres/Neon/role/APID production builds,
focused state vet and the actual SQLC check pass. The schema snapshot is regenerated
from live migrations on the temporary UTF8 verification database; all seventeen
generated files match. The schema registry classifies role-plan ownership as
operational. Full-repository/lint/native/provider acceptance is not claimed.

Inventory recovery now restricts age decryption to identities matching the
committed recipient. A relabeled recipient cannot borrow another available
rotation key. All eleven inventory and twenty-two archive contracts pass with no
skips (2.959 s and 9.279 s), including the new recipient-substitution regression.
The stored-phase role-plan test additionally restores and successfully reads its
original ciphertext before changing the operation phase; that isolated contract
passes again (2.192 s), separating phase rejection from damaged-byte rejection.

Worker capture/seeding composition, complete role/database/global materialization,
credential activation and ownership retirement remain required. Public database/
object clone admission stays closed. Complete writer closure/common-point capture,
independent dataset verification, object/configuration coverage,
production-preserving promotion/rollback and native/provider acceptance remain
required for the full one-command stage workflow.

### Private role plan capture and seeding worker (2026-10-03)

The private APID worker now composes role-plan ownership with authenticated target
SQL borrowing. It recovers source roles only from the originally sealed inventory
and bootstrap identity only from the originally sealed target pins, checking the
frozen source definition and operation scope. Its role-only export-plan view does
not project temporary database admission and must never be used for database
exports. Existing target roles are classified by their independently observed
catalogue OIDs; the role planner additionally requires identical source logical
attributes and settings. Every source role remains required, including predefined
and provider/private roles. Unsupported differences fail without mutation.

When no role plan is retained, the worker reads the independently owned target
catalogue through the provider borrower. Only successful provider postchecks
permit sealing and committing the original role plan. A lost control-plane commit
reply performs no role DDL; retry first recovers the original ciphertext. Committed
plan recovery requires no target SQL/provider IO, current encryption recipient or
source reread. Missing original keys and damaged ownership remain errors rather
than recapture permission.

Role seeding starts only after durable plan recovery and live ownership checks.
The provider borrower checks target placement around the SQL callback, while the
role primitive rechecks the original plan owner and lease before dispatch, after
the advisory-lock wait and before/after transaction commit. The worker checks
ownership again after provider postchecks. Any error returns no successful role
receipt. Exact retries authenticate the original target transaction journal and
retain original created role OIDs and timestamp after uncertain committed replies.
New roles remain NOLOGIN with PASSWORD NULL, preserving original settings and
holding captured login intent for later credential activation. This does not grant
database dataset readiness or publish a stage.

Verification: all sixteen focused APID role/import/bootstrap-pin/inventory worker
contracts pass with no skips (77.202 s), including four new role-worker contracts.
The new fixture compares PostgreSQL system identifiers to require genuinely
independent local source and target clusters. It exercises real role creation,
original source metadata despite later source role changes, explicit target role
identity recovery, original-recipient rotation, control-plane handoff, committed
provider reply loss, stale/phase/owner drift at dispatch, unreadable plans and
provider postcheck rejection before plan retention. Borrowed connections close on
success and failure; target data-resource identity and observed generation remain
unpublished. Provider/capture placement is synthetic, and the worker fixture uses
an administrator; ordinary CREATEROLE owner privileges remain qualified by the
separate role primitive contracts. The focused overlay preserves all 454 original
production APID files and selects original test declarations by dependency closure.
An initial final-assertion failure used a nonexistent pg_authid column and is
excluded from the corrected complete run. Normal production builds and focused
APID vet pass. Full-repository/lint/native/remote-provider acceptance is not claimed.

This worker remains private and is not installed in public database/object clone
admission. Complete role memberships/grantors, database-scoped globals and creation,
dataset import/verification, credentials/activation and ownership retirement remain
required. Common-point writer closure, complete object/configuration coverage,
production-preserving promotion/rollback and native/provider acceptance remain
required for the full one-command stage workflow.

### Private complete role membership materialization (2026-10-03)

The private role module now freezes and materializes every captured membership,
including predefined/provider/private roles, the exact grantor and ADMIN/INHERIT/
SET options. Membership catalogues return detached, redacted worker views of the
original inventory. Each immutable membership plan binds that original inventory,
the original role seed plan and its committed OIDs/timestamp, bootstrap target
pins and an authenticated post-seeding target grant baseline. Encryption uses a
distinct plan type and age namespace, recovering only with the original recipient.
Source changes and different role seeds cannot rebase it.

All grants, option changes, removals and the private target receipt commit in one
transaction. The role and membership protocols share an advisory lock and live
authorization checks before dispatch, after lock waiting and before/after commit.
Fixed SQLC functions quote identifiers on the server. A fixed-point reconciliation
resolves grant dependencies and temporary cycles, preserving existing ADMIN until
dependent removals/replacements permit its reduction. Revocations use RESTRICT;
no CASCADE can silently remove desired grants. Final verification compares the
entire target graph and every seeded role identity/attribute. Missing authority
to reproduce a grantor rolls back the whole transaction. This follows PostgreSQL's
[grantor and membership rules](https://www.postgresql.org/docs/16/sql-grant.html)
and [dependent revocation rules](https://www.postgresql.org/docs/16/sql-revoke.html).

Exact retries require the original target journal and current grant graph, retaining
the first application timestamp without GRANT/REVOKE. A lost committed reply returns
no successful receipt and can recover the original journal with the sealed plan.
This final graph step follows operations needing temporary creator grants and
precedes login activation; NOLOGIN and PASSWORD NULL remain unchanged. It grants
no database dataset readiness or public stage publication.

Verification uses two independent local PostgreSQL 16 clusters. Seven new
membership contracts cover an ordinary CREATEROLE/CREATEDB owner, exact grantor/
option retention, multiple grantors and dependent grants, conflicting reverse
target edges, sealed key/namespace/scope/seed substitution, source and target
baseline drift, concurrent first commit, stale lock waiters, committed reply loss,
precommit rollback and unavailable grantor authority. All fourteen role/membership
contracts pass with no skips (3.966 s). An exploratory concurrent run shared global
source role mutations with other packages and is excluded; role qualification now
runs independently. Production builds, normal role/inventory/archive vet and the
actual SQLC gate pass, including all eighteen generated files. No provider/native
or full-repository acceptance is claimed.

All eleven inventory and twenty-two archive contracts also pass with no skips
(1.082 s and 14.785 s), run sequentially after role qualification. Task-owned older
verification logs were losslessly compressed with original and compressed hashes
recorded when local disk pressure rejected a file write; shared caches and unrelated
files were retained. The corrected test files and final verification runs are the
acceptance evidence for this increment.

The membership primitive was initially delivered before durable control-plane
ownership and worker composition; that integration is recorded below. Database
creation/scoped globals, comments/security labels, parameter
ACLs, credential preparation/activation, archive imports/verification and retirement
remain required. Public database/object clone admission stays closed. Common-point
writer closure, complete object/configuration coverage, production-preserving
promotion/rollback and native/provider acceptance remain required for the full
one-command stage workflow.

### Private durable membership ownership and worker recovery (2026-10-03)

`project_environment_clone_postgres_membership_plans` retains one original encrypted
complete grant plan per operation/source and independently prepared target. Its
lease-authorized capturing transaction locks the original inventory, bootstrap pins
and role plan. It retains all three exact prerequisite ciphertext hashes, scope,
fingerprints, original recipient and first ownership timestamp. The worker supplies
the exact role-plan ciphertext hash it used, so parent replacement during first
capture is rejected. Re-encryption or rotation cannot replace an occupied plan.
Existing ciphertext caps and target reservations bound this private ledger. Raw SQL
names/OIDs, grantors/options, seeded role identities and seed timestamp remain in
authenticated ciphertext. Occupied migration rollback refuses ownership loss.

`RecoverMemberships` opens that original encrypted graph using the original role
plan and source inventory without SQL/provider IO or a current recipient. It checks
the complete source graph, exact embedded role plan and matching original key.
The embedded seed OIDs/time are subsequently checked against the independently
authenticated target's original role-seed transaction journal before any grants.
Recovering metadata supplies no proof that target mutation committed.

The private APID worker first recovers immutable role and membership ownership.
For an absent membership plan, it prepares/recovers roles, reads the target's
post-seeding catalogue through authenticated SQL borrowing and requires provider
postchecks before committing the sealed graph. A lost control-plane record reply
dispatches no GRANT/REVOKE. Retried recovery uses the original target baseline and
seeded identities; a changed graph or unavailable original key cannot authorize
recapture. Live membership ownership is checked before dispatch, after lock waiting,
before/after SQL commit and after provider postchecks. Errors return no successful
membership receipt. A lost committed target/provider reply can recover the original
target graph journal and application timestamp without repeated grants.

Capture and final reconciliation follow database/credential preparation needing
temporary creator grants and precede LOGIN activation. This private composition
publishes no data-resource identity, observed generation or stage readiness. Public
database/object clone admission stays closed. Database creation/scoped globals,
comments/security labels/parameter ACLs, credentials/activation, independent import
verification and private ownership retirement remain required. Common-point writer
closure, complete object/configuration coverage, production-preserving promotion/
rollback and native/provider acceptance remain required for the full stage workflow.

Verification for this increment: all sixty-seven contract roots pass with no skips:
thirty-two focused state/archive/import/target-pin/role/membership/schema contracts
(230.189 s), twenty focused APID inventory/pin/import/role/membership worker
contracts (343.527 s), and fifteen complete role/membership primitive contracts
(15.429 s). Four new state roots cover concurrent original ownership, handoff,
all prerequisite ciphertext substitution, first-capture parent mismatch, metadata
caps/damage, stored phase changes, private output and empty/occupied rollback.
Four new worker roots cover lost control-plane and target/provider replies,
SQL-free original-key recovery, changed live source metadata, stale/phase/owner
dispatch, target graph drift and recipient/provider rejection before retention.
The additional primitive root proves that SQL-free recovery cannot substitute
the authenticated target's original seed journal before grants.

State metadata fixtures require a private database for the fixed SQL journal
namespace, use existing-role mappings and synthetic provider placement, and perform
no role DDL. New worker fixtures compare independent cluster system identifiers,
use real role/grant SQL and an administrator bootstrap; ordinary owner authority
remains qualified by the dedicated primitive cluster pair. An initial schema-only
state fixture shared its journal and is excluded. An exploratory worker run omitted
the pg_dump setting and skipped existing archive contracts; a subsequent primitive
run encountered cleanup timeouts and residual fixture roles. Both are excluded.
The final worker and primitive runs use separate fresh cluster pairs and complete
without skips. Normal production builds, focused state/APID vet, normal role/
inventory/archive vet and the actual eighteen-file SQLC gate pass. The focused
overlays preserve all 542 state and 455 APID production files. Full-repository,
lint, remote-provider and native acceptance are not claimed.

### Private complete database creation planning and recovery (2026-10-03)

`copydatabases` retains a complete original logical database catalogue, including
closed databases, templates, private/provider entries, scoped GUCs, encoding,
locale provider/locale, ICU rules, collation version, admission intent, ACLs,
ownership and tablespace mappings. Every database and tablespace requires an
explicit disposition. Existing database adoption requires an observed target OID
and matching immutable encoding/locale/tablespace semantics. New target OIDs are
preallocated outside PostgreSQL's reserved range, distinct from all source database
OIDs and the target baseline. Tablespaces require observed target identities and
matching logical name, owner, ACL and options; provider-owned physical locations
remain independent placement work. No source database or configuration is skipped.

The encrypted plan retains the authenticated bootstrap pins, complete pre-write
target database/settings/tablespace baseline and original seeded role plan, target
role OIDs and first seed timestamp. Recovery uses the original matching recipient,
source inventory and bootstrap pins without SQL, a live source reread or a current
recipient. Sensitive names, scoped settings and SQL identities remain private.
Database catalogue getters detach all nested mutable values. ICU rules are now
captured using portable catalogue JSON; their optional encoding leaves older v1
inventory bytes and keyed fingerprints unchanged when rules are absent.

PostgreSQL [CREATE DATABASE](https://www.postgresql.org/docs/16/sql-createdatabase.html)
runs outside a transaction and supports explicit database OIDs in PostgreSQL 16.
The target protocol therefore holds the same **session** advisory lock used by
role/membership preparation across journal transactions and top-level creation.
A private bootstrap-owned journal retains the complete original plan and every
source/target OID pair. New entries progress through checked reserved, creating
and created states. Existing pinned entries retain their first ownership time.
The creating claim commits before CREATE; lost claim replies dispatch no DDL.
New databases are bootstrap-owned, non-template and closed, using original
encoding/locale/ICU rules/collation version/connection limit and mapped tablespace.
Desired ownership, ACLs, settings, template status and admission remain retained
for subsequent final materialization after data and credential preparation.

All statement generation stays in SQLC. PostgreSQL's server-side `format` quotes
identifiers and literals in a fixed SQLC query. Go transmits that returned utility
command whole because CREATE DATABASE cannot run inside the temporary SQL functions
or transactions used by the role/membership protocols. Go neither builds nor logs
SQL strings. The query inherits template0's tablespace when it matches the pinned
mapping; explicitly naming that same tablespace would require an additional CREATE
ACL. Other mappings retain the explicit tablespace and its authority precondition.

Retries hold the shared lock, verify the actual original role seed journal and
complete target role/database/settings/tablespace catalogues, and require fresh
lease/dispatch authorization at mutation and commit boundaries. A creating entry
can recover only its planned OID, exact initial database metadata and original
claim. If no database exists after the prior backend released the session lock,
the same claim can retry CREATE with that OID. A created entry whose database is
missing or changed refuses recreation. Extra databases, changed baseline/scoped
settings, substituted seed journals or shared/private journal damage reject
mutation. Errors and lost committed replies return no successful receipt; exact
retries retain the first completion time. Session lock uncertainty closes the
dedicated connection. Child SQL pins retain the original independent provider
owner, endpoint and scope, but prove only database preparation.

This primitive does not authorize imports, activate credentials or admission,
materialize final scoped globals/ACLs/owners, prove data readiness or publish a
stage. Durable control-plane database-plan ownership and worker composition were
initially absent; their integration is recorded below. Per-database SQL pins,
PostgreSQL 14/15, remote provider behaviour,
comments/security labels/parameter ACLs, final global materialization, independent
import verification and ownership retirement remain required. Public database/
object clone admission stays closed. Common-point writer closure, complete object/
configuration coverage, production-preserving promotion/rollback and native
acceptance remain required for the full one-command stage workflow.

Verification: all fifty-seven contract roots pass with no skips, using two
independent local PostgreSQL 16 clusters and ordinary CREATEROLE/CREATEDB target
owners: eight new database creation/recovery roots (8.601 s), twelve inventory
roots including legacy-MAC/deep-copy regression (1.447 s), twenty-two archive
roots (8.226 s), and fifteen role/membership roots (4.913 s). New contracts cover
all database/template dispositions, ICU rules and scoped config retention,
independent OIDs and closed/bootstrap-owned creation, exact retries, key rotation/
relabeling/namespace/source/owner substitution, lost creating and completed
replies, precommit claim rollback, missing-create recovery, actual advisory-lock
waits, stale leases, concurrent first completion, target drift, shared journals,
missing completed databases and substituted role-seed timestamps. Exploratory
fixture/codec failures and a build interrupted by disappearing shared cache
entries are excluded; final qualification uses a task-owned isolated build cache.
Normal production builds (state, managed Postgres/Neon, inventory, roles,
databases and APID), normal core vet and the actual twenty-two-file SQLC gate
pass. Full-repository, lint, remote-provider and native acceptance are not claimed.

### Private durable database ownership and worker recovery (2026-10-03)

`project_environment_clone_postgres_database_plans` retains one original encrypted
complete database plan per operation/source and independently prepared target.
Its capturing transaction locks the original inventory, bootstrap pins and role
plan, retains all three exact ciphertext hashes, and requires the worker's original
role-plan hash before first ownership. Scope, fingerprints, matching recipient,
ciphertext and first ownership timestamp cannot be replaced. Existing ciphertext
caps and target reservations bound this private ledger. SQL names/OIDs, database
configuration and the original role seed remain encrypted. Occupied rollback
refuses ownership loss; dependent rollback fixtures drop this child before parents.

The private APID worker opens existing ownership with the original key without
target SQL or a current recipient. It checks the complete captured inventory and
the exact embedded original role plan. Database preparation requires an explicit
original projected export plan: role-only inventory recovery cannot reconstruct
pre-fence admission intent. The comparison retains every captured byte and scope
while allowing that authenticated admission projection; once owned, recovery must
match the retained logical database plan. Durable original fence/admission recovery
and common-point caller integration remain required before public dispatch.

For first ownership, the worker prepares/recovers the original roles, verifies
their actual target seed journal, and reads the target catalogue through authenticated
SQL borrowing. All databases receive observed existing OIDs or preallocated distinct
new OIDs; every source tablespace requires a mapping. Provider postchecks precede
control-plane retention. A lost retention reply dispatches no CREATE DATABASE.
First-capture parent substitution is rejected. Retried planning retains the original
baseline, recipient, dispositions and logical configuration despite live source edits.

The worker prepares every database on one borrowed bootstrap connection from that
durable complete plan. Fresh live ownership checks apply before SQL, at the primitive's
lock/mutation/commit boundaries, and after provider postchecks. Partial physical progress
remains privately owned and recoverable; errors return no successful complete receipt
set. A lost creating/completed or provider reply recovers the planned OIDs and original
completion timestamps. New databases remain bootstrap-owned and closed. These receipts
grant neither import authority nor data-resource identity, observed generation or stage
readiness. Public database/object clone admission stays closed.

Per-database durable SQL pins were initially absent; their integration follows below.
Maintenance access, final database globals/ACLs/
owners, credential preparation/activation, independent import verification and private
ownership retirement remain required. Common-point writer closure, complete object/
configuration coverage, production-preserving promotion/rollback and native/provider
acceptance remain required for the full one-command workflow.

Verification: all 118 contract roots pass with no skips: 36 focused state/archive/
import/target-pin/role/membership/database/schema roots (118.866 s), 24 focused APID
worker roots (195.806 s), and 58 complete primitive roots across database creation
(7.832 s), inventory (1.166 s), archives (8.947 s) and roles/memberships (6.152 s).
Four new state roots cover concurrent first ownership, handoff, every prerequisite
ciphertext substitution, metadata damage/caps, changed stored phase, private output
and migration roundtrip/occupied rollback. Four new worker roots cover lost retention
and target/provider replies, original-key metadata recovery, original admission and
configuration retention, stale/phase/owner dispatch, target drift, changed parents
and recipient/provider rejection before retention. The inventory regression checks
full capture identity independently of its original admission projection.

Independent local PostgreSQL 16 cluster pairs qualify target mutation. State metadata
fixtures use existing-role mappings, private database journals and synthetic provider
placement. A privacy fixture now uses a distinct SQL database name because the shared
name `postgres` also appears as its legitimate backend ID. Exploratory runs with a
migrated shared public ledger, incomplete dependent rollback fixtures, the ambiguous
privacy assertion, and a subprocess timeout under compiler pressure are excluded.
The final state and sequential primitive runs succeed. Normal production builds,
focused state/APID vet, normal core vet and the actual 22-file SQLC gate pass. Focused
overlays replace only test files and preserve all 544 state and 456 APID production
files. Full-repository, lint, remote-provider and native acceptance are not claimed.

### Private durable per-database preparation pins (2026-10-03)

`copydatabases.SealedPreparation` retains the original source database mapping,
child SQL identity and first creation completion time under its own authenticated
namespace. Its encrypted body binds a fingerprint of the complete original database
plan, including dispositions for other databases and the original role proof.
Recovery requires that exact plan, original export projection and matching original
recipient. A bootstrap-pin envelope, another source OID, a changed complete plan,
relabeled key or changed scope/owner/identity cannot substitute a child receipt.
Metadata recovery performs no SQL or source reread and proves no current dataset.

`Receipt.VerifyForWorker` authenticates the original bootstrap identity and role
seed, holds the shared session advisory lock, and reads the existing private
database journal. It requires the exact original completed/existing entry and
completion time, and compares the complete current database/settings/tablespace
catalogue with the retained baseline and owned creations. Fresh dispatch authority
is checked around lock waiting and verification. A missing journal, pending entry,
changed timestamp, catalogue drift or missing completed database is rejected.
Verification installs no durable journal and never retries CREATE DATABASE.

`project_environment_clone_postgres_database_sql_pins` retains one original
encrypted receipt per source database and prepared target. Its foreign keys bind
both the complete database plan and an already charged archive reservation. This
bounds children to the existing archive quotas, including closed databases and
templates; no source database is omitted. The source OID reuses the existing archive
index. Target SQL names/OIDs and preparation timestamps remain encrypted. Scope,
inventory fingerprint, target provider identity/time, exact database-plan ciphertext,
all immutable archive reservation fields and first pin ownership time are retained.
Archive upload/retention can progress while its original reservation fingerprint
remains fixed. First capture requires both original prerequisite hashes. Ciphertext,
recipient or fingerprint replacement is rejected. Occupied rollback refuses
ownership loss; parent rollback fixtures remove this child before dropping parents.

The APID capture worker checks the requested database and charged reservation
before parent preparation. It recovers original child metadata without SQL or a
current recipient. For an absent child it uses the original complete plan on the
authenticated bootstrap connection, checks live parent/archive ownership, and
requires provider postchecks before sealing and retention. Lost target/provider or
control-plane replies retain owned physical progress; errors return no usable child
receipt. Exact recovery retains the original SQL identity and creation timestamp.

The separate verification worker requires existing durable child, database and role
ownership and opens their original encrypted inputs without invoking capture helpers.
It checks the actual target journal through authenticated SQL borrowing, checks live
child ownership at verification boundaries and after provider postchecks, and returns
no receipt when authority or placement is uncertain. It performs no provisioning
when a preparation is absent. All newly created databases remain bootstrap-owned,
non-template and closed; this increment supplies no import dispatch, credential
activation, data-resource identity, observed generation or stage readiness.

Qualified maintenance access and closure for every database, final globals/ACLs/
owners, credential preparation/activation, unknown-import recovery and independent
data verification remain required before dataset publication. Common-point source
writer closure/admission recovery, complete object/configuration coverage, ownership
retirement, production-preserving promotion/rollback and native/provider acceptance
remain required for the full one-command workflow. Public database/object clone
admission stays closed.

Verification for this increment: all 78 contract roots pass with no skips: 40
focused state/archive/import/bootstrap-pin/role/membership/database/child-pin/schema
roots (80.825 s), 28 focused APID worker roots (159.321 s), and all 10 database
primitive roots (7.129 s). Two new primitive roots cover encrypted original plan/
mapping/time recovery, key/namespace/body/scope substitution and damaged actual
journals without repair. Four new state roots cover concurrent original ownership,
handoff, first-capture parent/reservation mismatch, every parent ciphertext and
archive reservation substitution, legitimate upload progress, bounds/damage/stored
phase, private output and migration roundtrip/occupied rollback. Four new worker
roots cover lost retention/provider replies, original-key recovery, handoff,
validation before provisioning, absent-preparation verification, journal/authority
drift and complete original coverage of closed databases and templates.

Qualification uses separate local PostgreSQL 16 cluster pairs for primitive,
state and worker contracts. Ordinary CREATEROLE/CREATEDB owners qualify the primitive;
state metadata fixtures map existing roles and use private databases with synthetic
provider placement. Worker fixtures use real target SQL and synthetic provider
pre/postchecks. Earlier green worker runs before the validation and metadata-only
verification corrections are excluded. The final worker run succeeds. Normal
production builds, normal core vet, focused state/APID vet and the actual 22-file
SQLC gate pass. Focused overlays replace only test files and preserve all 546 state
and 457 APID production files. No full-repository, lint, remote-provider or native
acceptance is claimed.

### Private closed-database maintenance windows (2026-10-04)

`copydatabases.Receipt.WithMaintenance` admits one synchronous restore callback
on an originally prepared closed database. The caller must first durably reserve
its dispatch owner (the import's original UUID), authenticate the independently
owned provider and borrow the original bootstrap connection. The window binds
that UUID, the complete original plan fingerprint, source/target database OIDs
and original preparation completion time. It cannot create a preparation, mint
an import owner or attest to an imported dataset. `MaintenanceClosure` is opaque
and redacted; it attests only to restored original closed admission/configuration.

The original shared bootstrap session advisory lock spans opening, the child
callback and closure. Roles, memberships and database creation use that same lock.
A separate private `gregale_copy_database_maintenance.windows` journal preserves
the existing two-table creation namespace. Each database has one immutable window,
with `open`, `closing`, `closed` states and checked finite timestamps. A partial
unique index permits one active window per complete target plan. A crashed window
on another database prevents new admission until its original owner closes it.
Private schema/table ownership, sharing, relation/column shape, active index and
every window's original mapping/plan/preparation timestamp are checked on recovery.

Opening and recording the original window commit atomically. The temporary
connection limit is two: the identity-checking child connection and serial
`pg_restore` connection. The database's template flag is temporarily false.
The bootstrap role must have actual owner and CONNECT authority. Every other
non-superuser LOGIN role must lack both effective CONNECT and owner membership.
Original role-seed verification keeps newly seeded customer roles NOLOGIN with
no copied source password. Existing unsafe login/membership admission fails before
mutation. Provider superusers remain an administrative trust boundary, requiring
independent provider isolation checks around the enclosing borrow.

No database ACL is changed, including its original NULL representation. PostgreSQL's
ordinary grant/revoke implementation writes an explicit ACL, so revoking and
regranting defaults would not restore the captured raw catalogue. See the
[PostgreSQL 16 ACL implementation](https://raw.githubusercontent.com/postgres/postgres/REL_16_STABLE/src/backend/catalog/aclchk.c).
Original encoding, locale/ICU rules/collation version, owner, tablespace, ACL and
scoped settings remain exact. Maintenance catalogue comparisons allow only the
specific admission/template/limit changes authenticated by the original window.
Ordinary preparation/verification retains its original strict catalogue checks.

After callback success, failure, cancellation or expired dispatch authority,
bounded cleanup first atomically disables connections and records `closing`.
It then verifies the complete original catalogue and role seed, requires every
child session to have drained, restores the original template flag/connection
limit and records the first closure timestamp. Cleanup does not terminate sessions.
A leaked or foreign session, role drift or another database's drift leaves the
selected database closed with its original window still owned; no closure receipt
is returned. Exact later recovery may finish after the independent cause is resolved.
The two-connection limit and ten-second cleanup bound live in `pkg/api/limits.go`;
they are internal protocol bounds, not new plan or billing allowances.

Live dispatch authority is checked before/after lock waiting, before opening,
at the opening commit boundary, around the callback and before returning success.
The original owned window permits close-only cleanup under the held lock even
when its dispatch context/lease expires. An uncertain lock closes the dedicated
connection. `CloseMaintenance` authenticates a fresh close-only borrower and
recovers the exact original window; it neither opens admission nor imports SQL.
Missing/damaged ownership is rejected without installation or repair. A lost
opening/closing commit reply supplies no usable result. Any retry of
`WithMaintenance` closes/observes its original window and rejects callback replay.
Terminal close-only retries verify the actual original catalogue/seed and return
the original completion time.

Existing closed templates are supported only with qualified original owner
rights and private admission. Open baseline databases and the bootstrap database
holding these journals still require separate closure/relocation protocols;
provider entries without sufficient owner authority fail unsupported. Every such
entry remains required by the complete immutable plan. None is omitted to claim
complete copy support. Additional verification access after uncertain imports,
final database globals/ACLs/owners, credential preparation/activation and provider
qualification remain required.

The APID import coordinator has not yet composed this window with its durable
child SQL pins and import dispatch/closure recovery. This increment supplies the
private PostgreSQL 16 primitive, not public one-command stage cloning. Public
database/object clone admission stays closed. Common-point source writer closure
and admission recovery, complete object/configuration coverage, independent data
verification, ownership retirement, production-preserving promotion/rollback and
native/provider acceptance remain required for the full workflow.

Verification: all 72 primitive contract roots pass without skips: 22 database
roots (15.224 s), 13 inventory roots (1.056 s), 22 archive roots (7.004 s) and
15 role/membership roots (5.204 s). The 12 new database roots cover NULL ACL and
zero-limit restoration, new databases/existing closed ICU templates, unauthorized
logins/owner assumption, failed/cancelled/expired dispatch, committed opening and
closing reply loss, crashed borrowers, leaked/foreign sessions, role/database
drift, damaged/shared/missing-index ownership, stale lock waiters, serialization
across databases, input/scope/borrower rejection and required unsupported provider
entries. The actual encrypted dump/stage/restore test preserves original object
ownership and verifies a stage-only write does not change its source data.

Qualification uses two independent local PostgreSQL 16 clusters and ordinary
CREATEROLE/CREATEDB workers. Fixture-only explicit role authority permits restoring
original object ownership; synthetic provider pins do not qualify a remote provider
or credential activation. Normal production builds for state, managed PostgreSQL
and APID, normal vet for all four primitive packages, and the actual 23-file SQLC
gate pass. Exploratory runs with a non-comparable test result, incorrect utility
boolean rendering, an unqualified fixture search path and empty libpq credentials,
and the green run preceding the active-window index/reply-loss cases are excluded.
No full-repository, state/APID test rerun, lint, PostgreSQL 14/15, live provider or
native KVM acceptance is claimed.

### Private imports bound to original database preparations (2026-10-04)

The private APID importer now consumes the first retained per-database SQL
preparation, its complete original database plan, and its original charged
archive reservation. Its target argument is an exact assertion against the
opened child receipt; callers cannot select a new database or rediscover one.
Metadata recovery uses existing child/role/database/bootstrap pins and matching
original age identities. It neither captures missing parents nor prepares or
recreates a database. The strict closed-catalogue verification seam remains
available for ordinary preparation verification, while maintenance recovery
opens metadata before quiescing an uncertain original window.

A new append-only migration adds three nullable digest columns to each import:
the exact child preparation ciphertext, complete database plan ciphertext, and
archive reservation. A CHECK requires the complete tuple or all NULL; a composite
foreign key retains the original child identity. First reservation validates all
parents and the exact child target under the same durable operation/target/archive
locks. Bound import reads, claims and execution records validate the original
child and its complete prerequisites at every boundary. An occupied import cannot
be re-encrypted, rebound or silently adopted. Rollback refuses every occupied bound
owner, including reservations and uncertain imports. Legacy all-NULL records
remain owned for their existing private ledger protocol; the maintenance importer
rejects them in every state. These columns are operational ownership in the clone
schema registry and do not become copied customer configuration.

For a new attempt, the worker authenticates and stages the original retained
archive before target SQL. It borrows the original bootstrap endpoint, verifies
the original closed preparation, and commits the sole durable import claim before
opening admission. The original import UUID owns the SQL maintenance window. The
bootstrap session lock spans opening, the nested child borrower, and closure. The
child borrower runs the real authenticated restore and closes all its connections
before maintenance restores original closed admission/configuration. Both child
and bootstrap provider postchecks, successful original closure and fresh durable
authority are required before recording execution. Lost reservation/claim replies
return no successful result and cannot authorize an unobserved dispatch.

An `importing` retry borrows only the original bootstrap and closes/verifies the
original window. Archive storage may be unavailable during this recovery. It never
fetches/stages the archive, borrows the child, opens admission or runs restore
again. It still returns an unavailable command outcome;
closure alone cannot determine whether SQL data committed. This also covers a
committed claim with no maintenance window: ownership stays uncertain and no new
window/import is fabricated. A recorded `executed` command is replayed only after
original window closure/catalogue verification and provider pre/postchecks; changed
admission, missing/substituted windows or failed placement cannot be bypassed by
stored command metadata. Original closure timestamps survive handoff. Leaked child
sessions leave admission closed with the window `closing`; after those sessions
exit, the same owner can finish closure without repeating restore.

This increment applies to qualified original closed, non-bootstrap database
preparations. Open provider/bootstrap entries remain required and unsupported by
this protocol. Ordinary pin/import authority still requires the actual durable
`capturing` phase. Recovery in compensation/retirement needs separately qualified
close-only lineage authority; mutation authority is not generalized to other
phases. Independent dataset equivalence/unknown-command verification access,
complete database globals/final ownership and credentials, ownership retirement,
common-point database/object writer closure and complete public clone orchestration
remain required. Public database/object clone admission stays closed.

Qualification passes 44 focused state/schema/prerequisite roots and 32 focused
APID roots, with vet enabled and no skips. Test-only AST overlays preserve all
546 state and 457 APID production Go files. The actual encrypted selected-database
export/stage/restore preserves original object ownership and demonstrates isolated
target writes; tests exercise committed reservation/claim/execution reply loss,
provider postcheck failures, lost opening/closing replies, close-only handoff with
archive storage unavailable, leaked child sessions, original-key recovery,
substituted prerequisites, legacy owners, changed phases/stale workers and executed
replay rejection after admission/window/placement changes. Empty migration round
trips and occupied rollback refusal include the new preparation foreign key.

The source and target are two independent private local PostgreSQL 16 clusters;
fixture SQL administrators permit original object ownership restoration. Provider
pins/postchecks are synthetic and do not qualify live provider authority or
credential activation. Normal production builds for state, managed PostgreSQL and
APID and the actual SQLC gate pass. The public-ledger fixture-root attempt, missing
schema registry/fixture updates, wrong-database journal assertions and regression
run preceding archive-outage recovery are excluded from final evidence. No full
repository test/lint, PostgreSQL 14/15, live provider or native KVM acceptance is
claimed. Owned test servers and their RAM volume are retired after qualification;
the pre-existing private server and reusable build cache are preserved.

### Separate access for independent database verification (2026-10-04)

A private `copydatabases.Receipt.WithVerificationAccess` protocol now opens a
separately owned inspection window after the exact original import window is
closed. It consumes the original complete preparation and source export plan,
original import UUID, and a distinct verification UUID. It does not infer that
restore ran or committed. This permits inspection after an uncertain command
outcome without adopting or redispatching the retained import owner. Public
clone admission remains closed; independent dataset comparison and durable
control-plane verification ownership are still required before worker wiring.

The dedicated `gregale_copy_database_verification.windows` journal binds every
row to the original preparation time and plan fingerprint, exact source/target
OIDs, original import UUID and first opening/closure timestamps. Its immutable
owner is distinct from import and provider/scope owners. All import parents must
be closed. One active verification window is allowed across the complete plan;
import and verification share the existing bootstrap session lock. Schema/row
privacy, column types, finite chronological timestamps and the exact active
index expression/predicate are checked before use. Missing/damaged journals,
changed parents, substituted owners or changed preparation bytes are conflicts;
close-only recovery never installs or repairs them.

Only a never-opened verification owner runs its callback. An existing window is
recovered close-only and returns conflict, including after loss of a committed
opening or closure reply. `CloseVerificationAccess` returns the first retained
closure timestamp after full original catalogue and role-seed checks. It leaves
the import journal and its timestamps unchanged. A successful `VerificationClosure`
attests only to restored closed admission/configuration; it is opaque and redacted,
and cannot attest to callback execution, matching data, final globals or readiness.
Unknown inspection outcomes currently require separately qualified durable retry
or retirement ownership; this primitive does not fabricate a new attempt.

Opening atomically retains ownership and temporarily admits the trusted worker
using the same centralized two-connection ceiling as import. Customer roles stay
NOLOGIN and other non-admin login/CONNECT/owner capabilities must remain isolated.
No database ACL is changed, preserving NULL ACLs. The bootstrap lock spans the
callback and bounded closure. Quiescing closes admission before full catalogue
and role checks; leaked child connections are never forcibly terminated and leave
an owned `closing` window. After the cause exits or original drift is resolved,
the exact owner can complete closure. Cancellation/authority loss uses the same
bounded cleanup timeout. Import dispatch/closure and strict preparation/create
readers reject every active verification owner, including a closing window whose
settings happen to equal its original two-connection baseline.

`VerificationTarget.WithReadOnly` authenticates the exact borrowed child identity,
checks fresh window/worker authority and provider placement, then runs inspection
inside a `REPEATABLE READ, READ ONLY` transaction. It rolls back before fresh
identity/provider/window checks and returning the borrowed connection. Callback
failures are sanitized; ended transactions, changed identity/read-only state,
cancellation, stale authority and failed provider postchecks return no successful
inspection. PostgreSQL read-only semantics protect ordinary inspection from data
writes; the bootstrap role remains a trusted worker capability, not a privilege
boundary against platform code that already has that role. The enclosing borrower
must close every child connection before the outer callback returns. A retained
access target cannot authorize reads after its original window closes.

This is required access infrastructure for a complete verifier. An ordinary
`pg_dump` archive comparison is insufficient: [PostgreSQL 16 documents that stored
materialized-view rows are not dumped and foreign-table contents require explicit
inclusion](https://www.postgresql.org/docs/16/app-pgdump.html). Complete original
stored-data coverage, external/foreign/extension resources, unambiguous catalogue
projection and bounded independent row comparison remain required. This change
introduces no equality or readiness proof based on command exit, row counts or
archive bytes. Open provider/bootstrap entries remain required and unsupported
by the closed non-bootstrap window protocol.

Qualification includes the full copydatabases contracts with vet enabled: original
archive export/stage/restore and isolated writes followed by separate read-only
inspection; zero limits, templates and NULL ACL restoration; write rejection;
unchanged original import receipts; owner/parent/privacy/index substitution;
failed/cancelled/stale callbacks; child/placement/transaction failures; leaked
sessions; role/unselected-catalogue drift; different-database serialization;
concurrent and stale waiters; and loss of committed opening/closure replies.
Focused APID import/preparation regressions and normal production builds qualify
compatibility; generated SQLC output includes the new fixed SQL source and gate.
Independent local PostgreSQL 16 clusters use ordinary CREATEROLE/CREATEDB workers
and fixture administrators. This does not qualify PostgreSQL 14/15, live provider
permissions, complete clone orchestration, full repository lint/test, or native
KVM acceptance. Owned qualification resources are retired after testing; existing
private infrastructure is preserved.

### Independent qualified stored-data comparison (2026-10-04)

The private `copycontents` package now captures an independent contents manifest
from the exact authenticated immutable source reader and compares it with actual
SQL data read through the separately owned target verification window. Its
opaque `Match` binds the original manifest fingerprint to the exact target pins.
This is contents evidence: source writer closure/native snapshot coverage,
complete DDL/globals/extensions, original archive/preparation/import ownership,
window closure, final credentials and stage readiness remain separate required
proofs. No public clone admission or APID dataset publication is enabled here.

The source manifest includes every required local stored user relation, including
extension-owned tables, inheritance/partition leaves, partition parents and
materialized views. `ONLY` prevents double counting inherited/partitioned data;
zero-column rows and empty/unpopulated materialized views retain explicit state.
Sequence inspection records `last_value` and `is_called`; PostgreSQL's internal
sequence WAL cache counter is operational, not copied logical state. Large-object
OID membership and logical byte streams are independently read with transaction
owned `INV_READ` descriptors, including objects larger than one read block.
Physical indexes/TOAST and built-in catalogues are not separate logical data
sets; values from TOAST are included through their owning user rows. Catalogue
metadata and large-object permissions/ACLs still require their separate complete
schema/global strategies. User data and private names never appear in ordinary
manifest, configuration, match, error or formatted output.

Reads use authenticated READ ONLY, REPEATABLE READ transactions and fresh
source/target identity, provider placement and durable-authority assertions around
data objects. Fixed transaction-local output settings make date/time, interval,
bytea, floats, currency and client encoding comparable without changing persistent
source configuration. `row_security=off` raises an error if policies would hide
rows; it does not grant a bypass privilege. [PostgreSQL documents COPY's type
output and escaping behavior and its row-security handling](https://www.postgresql.org/docs/16/sql-copy.html).
Only server-quoted SQLC-generated COPY commands execute; Go never builds SQL from
identifiers. Source capture rolls back and rechecks placement before returning;
target comparison consumes the existing verification transaction and never opens
admission, restores SQL, or rewrites data.

COPY text directly invokes type output, avoiding application-defined casts to
text. A reviewed builtin-output registry and recursively qualified enum, domain,
array, composite, range and multirange dependencies define this initial comparison
profile. Type/function identities and logical names/ordered columns are checked;
source relation and custom-type OIDs are not compared with target OIDs. Foreign
and temporary relations, SQL_ASCII, arbitrary extension/custom base output, and
unqualified object-reference types such as `regproc` return explicit private
coverage blockers. These are required strategies, not silently omitted objects.
`CoverageError` exposes a stable reason; its SQL identity is available only to the
owning worker for mapping to the authorized logical database resource. An
extension's other stored tables remain inventory input even when their types are
ordinary builtins. External writers/background resources still require complete
source closure and extension/provider qualification.

Every row is streamed into an HMAC over unambiguous canonical field/row framing.
NULL, empty strings, literal NULL markers, Unicode, escaped tabs/newlines and
zero-column rows remain distinct. Keyed row digests are externally sorted as a
multiset and streamed into a second HMAC with the exact row count. Duplicates are
retained; row order, heap layout, source/target OIDs and merge layout cannot rebase
the result. Equal counts, XOR/sum checksums and command exit status are never data
proofs. Row payloads never enter spools: only keyed 32-byte digests do. A bounded
binary merge uses immediately unlinked private files and releases every owned
file/directory on success, error or cancellation. Large objects use separate
keyed byte-stream digests and retained OID identities.

Structural caps live in `pkg/api/limits.go`: 65,536 relations/types/large objects,
1,048,576 catalogue columns, type depth 64, an 8 MiB digest chunk, 32 merge levels,
64 GiB of live digest data, 1 MiB large-object read blocks and ten-second bounded
capture cleanup. The existing 1 TiB archive bound limits streamed canonical data;
private catalogue/manifests use the existing 4 MiB inventory bound. Lower worker
budgets may be supplied; changing budgets does not change equality. Fixed small
merge buffers and filesystem metadata are additional bounded overhead. These
private limits are not storage entitlements. Durable reservations, charging and
worker placement for the extra manifest/spool work remain required before wiring.

The complete original source descriptor, comparison key, logical type/column
projection and contents are authenticated under an operation-scoped HMAC and
sealed under `gregale-postgres-copy-stored-contents-v1`. Private serialization
explicitly bypasses the source descriptor's ordinary redaction. Recovery consumes
the original source requirement and retained ciphertext/recipient; a current key
cannot replace an unreadable original manifest or its comparison key. Header,
source-role/OID/scope/inventory, recipient, fingerprint or ciphertext substitution
returns no manifest. The worker must retain and verify the first durable manifest
owner alongside its exact native/archive/preparation lineage; that control-plane
ledger and verifier integration remain pending. An uncertain restore may be
verified from actual matching data without inventing a successful command receipt.

The real independent-cluster qualification path captures typed rows and large
objects, seals/recovers the original manifest, performs encrypted custom archive
export/stage/restore, and independently compares target data inside the original
verification window. It covers duplicate multiplicity, zero-column/inherited/
partitioned rows, nested enums/domains/arrays/composites, escaped/private values,
sequence state, multi-block large objects, old-key handoff, and application-defined
casts that attempt to hide changed enum values. Changes to rows, duplicate count,
sequence state, materialized population and large-object bytes/membership return
no match. An uncompressed 256 KiB text value confirms actual TOAST storage is
present: comparison follows its owning logical row, excludes physical `relkind=t`
storage, and detects a one-byte value change after restore. Policy-filtered rows,
unreachable foreign data, unqualified extension
output, object-reference types, stale placement, changed SQL identities,
cancellation and exhausted byte/spool budgets return no captured proof and retire
owned resources.

A source materialized view whose stored rows differ from its current query is
explicitly exercised: pg_restore's refresh produces different contents and the
independent comparison rejects it. Complete support still needs a qualified
materialized-row copy strategy; this change prevents false completeness but does
not replace that strategy. Local PostgreSQL 16 qualification and the actual SQLC
and normal production-build gates do not qualify PostgreSQL 14/15, live provider
permissions, complete clone orchestration, or native KVM acceptance. The public
full database/object clone gate remains closed while those and the earlier full
scope requirements are unfinished.

### Original contents manifest ownership and recovery (2026-10-04)

`project_environment_clone_postgres_contents` now reserves private ownership for
each original SQL database before contents capture. Its `(operation, source
database, SQL database OID)` index binds the exact retained inventory ciphertext,
original archive owner/reservation and original reader owner/endpoint identity.
The reader identity includes its immutable scope, request time and endpoint birth
time; availability observations and cleanup may progress without changing those
pins. Foreign keys enforce the original inventory/archive/reader tuples. The
schema coverage registry classifies this control-plane ledger as operational;
these rows are not customer configuration copied into another stage.

New reservations and first capture publication authenticate the native capture
and an observed available reader under the operation lease. Account holds are
serialized with the existing database account lock. Structural caps in
`pkg/api/limits.go` are 4,096 contents owners and 1 GiB of reserved ciphertext per
account, with each reservation bounded by the existing ciphertext cap. Lower
worker limits may be supplied. A successful replay retains its original charged
bytes; failed reads, cancellation, lost replies, compensation and reader cleanup
do not release the hold. This bounds retained manifest ownership, not row-read
CPU, digest spool placement, storage entitlements or billing. Qualified contents
owner retirement and worker resource charging remain required before full wiring.

The first captured fingerprint, ciphertext and encryption recipient are immutable.
Equivalent re-encryption cannot overwrite them. A read rechecks lease authority
after all receipt locks; stale/expired workers cannot obtain private contents or
publish a result. Recovery uses retained parents without a current source SQL
connection or live reader. Original archive/reader identity substitution or
damaged ciphertext returns no recovered owner. Authenticated reader retirement
preserves the contents receipt and its quota charge. Downgrade is refused for any
owned reservation, including one whose read was never dispatched. The generated
migration is replay-safe and its empty-table downgrade/upgrade retains the same
columns and constraint shape.

The private APID contents worker recovers committed ciphertext before invoking a
trusted reader. A missing owner is reserved with the current openable recipient;
an existing reservation retains its original recipient across rotation. It seals
the independent manifest and authenticates its complete original private source
descriptor before storage. A reservation that observes a concurrent first capture
opens that capture without another source read. Lost committed responses recover
the original manifest and comparison key. Missing old keys, tampering, stale
handoff, foreign source descriptors and exhausted reservations return no manifest;
none causes a current-source recapture or replacement of the first ciphertext.

The PostgreSQL control-plane contracts exercise concurrent owners, exact first
ciphertext, handoff, stale/expired leases, account count/byte caps, original parent
substitution, reader retirement, and actual migration round-trip/downgrade refusal.
The worker contracts use actual independent contents capture in a selected local
SQL database, with synthetic owned provider receipts and borrowed connections.
They exercise record/reservation reply loss, original-key recovery, post-read
provider failure, raced capture recovery, cancellation and substitution. Store
fixtures separately own opaque ciphertext; those tests alone do not authenticate
customer data. Existing inventory/archive/import/preparation regressions and the
schema coverage gate remain part of focused compatibility qualification.

No live source reader is installed by this private callback seam. Complete owned
reader composition, target verification ownership/publication, original writer
closure/native point coverage, stored materialized-row copying, all remaining
schema/globals/extensions/data strategies, provider qualification, ownership
retirement/metering and the complete stage coordinator remain required. Normal
production builds, local PostgreSQL 16 and actual SQLC checks do not qualify the
full repository, PostgreSQL 14/15, paid providers or native KVM acceptance. The
public full database/object clone gate remains closed.

### Owned reader composition for original contents (2026-10-04)

The private APID contents path now composes the original manifest reservation
with the retained provider reader. Captured manifest recovery still happens
before this path: it needs no provider connection, current source definition,
creation admission or spool directory. A missing manifest uses its original
charged owner and encryption recipient, and derives a request from the exact
retained native snapshot, adopted capture and observed reader. The selected SQL
database and authenticated role come from the original immutable export plan.

During capture, the worker rechecks that owner's original inventory ciphertext,
archive reservation, reader identity, recipient, bytes and creation time. It
authenticates the exact provider reader before borrowing SQL, around transaction
ownership and every data object, and after the provider's final SQL/placement
postchecks. Each provider lookup is surrounded by durable lease and original
request checks. Handoff, cancellation, unavailable/replaced input, a concurrent
first manifest or failed postcheck returns no new manifest. Actual Capture owns
its read-only repeatable-read transaction and private digest spools; the provider
owns and closes the borrowed SQL connection. Failures retain the original charged
reservation and native inputs for recovery and separately qualified retirement.

The focused local contracts use actual selected PostgreSQL 16 data with synthetic
owned provider receipts. They exercise committed response loss and original-key
handoff without another source read, pre-read substitution, authority loss during
actual row capture and after successful provider postchecks, concurrent first
publication, and read/sort budget exhaustion with closed SQL and empty spools.
These checks do not qualify live provider permissions or PostgreSQL 14/15.

This is private composition, not complete stage coordinator wiring. Repeated
provider/receipt checks are bounded by the worker lease and provider deadlines;
production work admission, digest spool placement and CPU/storage charging still
need qualification. Target verification ownership/publication, original writer
closure, complete schema/globals/data strategies, stored materialized-row copying,
retirement/metering and all other stage requirements remain pending. The public
full database/object clone gate remains closed.

### Durable target contents verification (2026-10-04)

The private target verification worker now compares actual qualified logical data
to the original retained source manifest. It never dispatches another restore or
recaptures current source data. Verification has its own immutable owner, bound
to the captured contents owner and first ciphertext, original import dispatch and
start, child SQL pins and preparation, archive reservation, target fingerprint,
scope, encryption recipient and held byte budget. Reads and mutations authenticate
those parents and recheck the worker lease after receipt locks. The schema registry
classifies the private ledger as operational. It grants no catalogue readiness.

The state sequence is `reserved -> verifying -> compared -> verified`. A first
claim is durable before target SQL access. The native journal opens at most one
verification window for that original database and import. The worker borrows the
exact selected child, owns a read-only repeatable-read transaction, compares the
original typed logical rows, sequences and large objects, rolls back, and completes
the child's provider postchecks. It then seals and stores the first match while
that original native window remains open. Provider placement and durable authority
are checked around data reads and provider lookups. Private digest spools and child
SQL connections are closed on success and failure.

The encrypted comparison envelope binds both SQL database OIDs, import and
verification owners, original native opening time, scope, manifest and target
fingerprints. Its MAC uses the original manifest's private comparison key; age
encryption and plausible public metadata alone cannot invent a data comparison.
Authenticated opening produces an opaque retained-match capability bound to the
exact first ciphertext. A zero capability, a foreign native closure or equivalent
re-encryption cannot publish `verified`. Native closure must authenticate the
original preparation, both owners and opening, preserve the first SQL closure
time, and complete bootstrap provider postchecks. Only then is the control-plane
verification timestamp recorded. Native and control-plane timestamp chains are
checked separately; a native SQL timestamp is not treated as that worker's local
wall clock.

The envelope is capped at 8 KiB and ciphertext at 16 KiB in `pkg/api/limits.go`.
Each already charged contents owner can hold exactly one verification owner, so
the existing 4,096-manifest account ceiling bounds retained comparison ciphertext
at 64 MiB. Parent foreign keys prevent release of the contents hold while this
owner exists. This cap is retained proof storage only; it grants no additional
CPU, row-read, digest spool, object-storage or billing entitlement. The first
recipient and byte reservation remain immutable across handoff and rotation.
Missing old keys and damaged parent/proof data fail without a replacement capture.
Any owned reservation prevents downgrade, including an undispatched one. Empty
downgrade/upgrade qualification removes dependent children in reverse order and
restores their original columns and constraints. The new migration is replay-safe.

A lost committed comparison response recovers the first `compared` ciphertext and
performs close-only native recovery. A lost closure response recovers `verified`
with the original native and control-plane times. Neither recovery reads source
or target data again. A lost claim response can make the first native comparison
only if no SQL window was ever opened. An uncertain original import whose actual
data matches can be verified after its native admission is closed; its import
state remains `importing`. Data equality does not manufacture a restore-execution
receipt or mark that dispatch `executed`.

Local qualification uses real PostgreSQL 16 source data, an encrypted `pg_dump`
archive and restoration into an independent target cluster, with synthetic owned
provider receipts. It covers lost reservation/claim/comparison/closure responses,
lost native opening/closure responses, original-key handoff, uncertain imports,
actual row differences, provider postcheck failures, data/sort budget exhaustion,
lease handoff and cancellation during target reads, ciphertext/parent substitution,
zero/foreign closure capabilities, concurrent ownership, expiry while waiting for
a receipt lock, actual migration downgrade guards and the schema coverage gate.
Opaque state ledger fixtures qualify ownership only, not data authentication.

Failed comparisons before durable `compared` evidence close their original native
window and retain the charged `verifying` owner. Replaying that window is close-only
and fails; it never performs another comparison. Automatic retries still require
qualified control-plane attempt history, new-owner admission and retirement under
production budgets. Reopening an old window or redispatching its import is
not an allowed shortcut. Fully closed source writers, a common configuration and
data cut, complete schema/globals/data strategies, stored materialized-row copying,
final authority and settings, provider isolation, PostgreSQL 14/15, retirement and
metering, object copying and the complete stage coordinator remain pending. The
public full database/object clone gate remains closed.

### Bounded native verification retries (2026-10-04)

Native verification now supports a separate new attempt after authenticated
closure of its predecessor. The original verification and import journals retain
their first owners, rows and timestamps. Additional attempts use the private
`gregale_copy_database_verification_retries` namespace. Its table stores the exact
source/target OIDs, preparation fingerprint and time, original closed import
owner and opening/closure, new owner and ordinal, predecessor owner and exact
opening/closure, and this attempt's first opening/closure. Retained history is
validated as a consecutive chain; gaps, forks, reused owners, changed bindings or
damaged schema/permissions cannot become an access or closure capability.

`PostgresCopyVerificationAttemptsMax` caps the total at three, including the first
window. Thus there are at most two additional native history rows per original
database. The private retry API requires an opaque, authenticated predecessor
closure bound to the same preparation and import. It admits only the immediate
next ordinal and a new owner. The shared bootstrap session lock and both journal
checks serialize first and retry windows across all databases in that target.
Concurrent owners for one predecessor cannot both run their callbacks. Owner
reuse from another database's import, first window or retry is rejected before
opening admission. The original database configuration and ACL remain preserved.

A retry borrows the same opaque verification target and owns the same read-only
repeatable-read transaction as the first attempt. Data comparison and sealed
match authentication still bind the exact new owner and native opening. Successful
closure is separate from a data match. Failure/cancellation/handoff quiesces and
closes the selected new window; a leaked child session is not killed and prevents
closure until its borrower closes it. An existing owner is recovered close-only
and returns conflict from the callback API. Lost opening and closure responses do
not cause another data read. Close-only retry recovery authenticates the asserted
current and predecessor owners from the retained native chain, preserving the
first committed opening and closure times. A missing row yields no closure and
does not itself grant opening authority.

Ordinary preparation/create/import readers authenticate the complete original
and retry history. They reject any active or quiescing attempt, including a
quiescing catalogue whose settings already equal the original closed database.
An older verification owner cannot supply a publishable closure while another
attempt owns active access. The first journal's shape is unchanged; no foreign
key is added to it, since PostgreSQL's internal reference triggers would change
its qualified private shape. Retry parent identity is authenticated from both
journals under their shared lock. All install/read/mutation SQL is generated via
SQLC; the generation check includes the additional protocol file.

Local PostgreSQL 16 qualification includes real encrypted archive restoration
and independent contents comparison during a new attempt, exact original history,
read-only semantics, opaque predecessor/closure binding, a third-attempt ceiling,
stale/foreign parents, concurrent owners, cross-database owner reuse and access,
lost native opening/closure responses, failure/cancellation/authority loss, leaked
child recovery, active/quiesced entry fencing, and damaged history/permissions.
This is native protocol qualification. Control-plane failed-attempt recording,
durable retry intent, ciphertext reservations, CPU/spool/billing admission and
retirement, and APID/coordinator composition are still required before enabling
automatic retries. A native closure alone cannot justify retrying a compared or
verified owner. Mixed-version reader rollout also needs qualification before
production wiring. Provider isolation, PostgreSQL 14/15 and every remaining full
stage requirement remain pending; the public full database/object clone gate
remains closed.


### Durable verification failure and retry admission (2026-10-04)

The control plane now retains closed unmatched attempts in the subordinate
`project_environment_clone_postgres_verification_attempts` ledger. Its first row
records the original owner's exact native opening and closure, target SQL OID,
original recipient and reservation, and control-plane failure time. Recording
this row requires the original opaque preparation and an authenticated native
closure for that owner and ordinal. A zero, foreign or metadata-only closure
cannot admit a retry. The original verification remains immutable in `verifying`;
its row, timestamps and original manifest/import/child SQL pins are preserved.
Once failure history exists, original claim, comparison and closure mutations
are fenced. `failed` means closed without durable matched evidence, rather than
an attestation that data is equal or that the original import executed.

Each retry reserves a new owner before claiming dispatch. It binds the original
scope, contents ciphertext and manifest fingerprint, original import owner and
start, child SQL pins, database plan, archive reservation and physical target
fingerprint through its retained parent. The next ordinal stores its immediate
failed predecessor owner and exact native opening/closure. The table's ordinal
ceiling and self/parent foreign keys retain the consecutive lineage; every read
also authenticates the complete chain, timestamps, recipient and byte holds.
Operation and parent locks serialize competing owners. Lease token/revision and
the control-plane clock are checked after locks, in SQL mutations, and before
commit. Handoff retains the original owners and reservations. Reads remain
available for authenticated compensation; new admission and mutations require
capturing state and a prepared target.

A compared or verified owner cannot become failed or authorize another attempt.
A committed comparison whose response was lost therefore recovers its first
ciphertext, rather than being interpreted as retry permission. New matched
ciphertext is capped by that attempt's immutable hold and binds its owner, native
opening, original import, manifest and target. Verified publication requires an
actual authenticated retained match and exact opaque native closure for the
current ordinal. Existing proof and closure responses are idempotent and preserve
first timestamps; metadata envelopes alone cannot publish verification.

The maximum remains three total attempts, including the original, with at most
two new 16 KiB proof holds per charged contents owner. The existing 4,096 contents
owner account ceiling and restrictive parent foreign keys bound original plus
retry proof holds at 192 MiB. The derived ceiling is named
`PostgresCopyVerificationBytesPerAccountMax` in `pkg/api/limits.go`. The first
failed history row reuses the original reservation and does not duplicate its
ciphertext charge. This is retained proof storage admission only. It grants no
CPU, row-read, digest spool, object-storage or billing entitlement; native history
and all held proof/parent storage still require qualified retirement.

Local PostgreSQL 16 qualification includes concurrent reservation and claim,
immutable owner/key/budget and first ciphertext, changed parent/predecessor
rejection, stale leases and expiry while waiting on an attempt lock, the total
attempt ceiling, actual migration replay/empty round trip/owned downgrade guards,
and complete clone schema coverage. APID integration contracts use actual source
contents, an encrypted pg_dump archive restored in an independent cluster,
a failed child provider postcheck, authenticated close-only recovery, two newly
owned native attempts, successful independent data comparison and exact current
closure. The successful closure cannot be reused as failure/retry evidence;
retained original history and import dispatch are unchanged. Provider receipts
remain synthetic fixtures, not paid provider permission qualification.

These interfaces and composition contracts do not yet wire automatic APID worker
or stage coordinator retry dispatch. That worker still needs durable failed-owner
recovery, exact never-opened versus uncertain native dispatch classification,
original-key handoff, current-attempt provider checks, CPU/spool/billing admission
and complete cleanup. The original private worker remains first-attempt only.
Mixed-version rollout, PostgreSQL 14/15, provider isolation, the common config/data
cut and all source writer closure, complete data/schema/globals strategies,
final authority, object copying, full coordinator and production-preserving
promotion/rollback remain required. The public full database/object clone gate
remains closed.

### APID owned verification retry worker (2026-10-04)

The private `projectEnvironmentClonePostgresVerificationWithRetries` worker now
composes original verification, durable failed-attempt history, new-owner
reservation/claim and the native retry protocol. Each invocation makes at most
one new target comparison. A failed comparison returns its error after fresh
close-only recovery and, when lease/key/provider authority remains valid,
recording the exact native closure as failed. Subsequent invocations select the
retained head and reserve the next distinct owner. The original import dispatch,
source manifest, preparation, physical target, first owner and closed history
remain preserved. The total attempt ceiling is enforced by the control plane and
native protocol; exhausting it cannot create another SQL window or data read.

An original undispatched reservation or lost claim response resumes its original
first dispatch through the already qualified first-window protocol. An original
SQL window whose opening response was lost is recovered and closed without a
comparison, then retained as failed before any next owner is admitted. Existing
retry intent is inspected close-only first. A missing retry row is reported only
after complete bootstrap provider postchecks succeed. The worker separately
recovers the actual opaque predecessor closure, binds its ordinal and exact
opening/closure to retained failed history, and supplies that capability and the
fresh claimed owner to native first dispatch under the shared bootstrap lock.
Missing-row metadata alone never grants opening authority. A held retry window
is closed and retained as failed without re-running its callback; another
invocation may admit its successor.

Original and retry comparisons share a worker helper. It requires the actual
native access identity for the expected ordinal, verification owner and original
import, and the pinned SQL target. Borrowed child access uses the native read-only
repeatable-read helper, original retained manifest, fresh parent/lease/native
and provider placement checks, rollback and complete child provider postchecks.
Only then is its sealed match persisted while the native window remains open.
Each authorization checks the original owner, complete attempt history and exact
child SQL pins. After comparison publication it expects the new retained state.
Exact current native closure, bootstrap provider postchecks and final ownership
checks precede verified publication. Every failing worker path returns zero
proof metadata, including a store that returns an already committed row with an
error.

Recovery re-reads durable state before recording failure. Lost committed match
and closure responses therefore recover their first ciphertext and timestamps
close-only, with no source/target data read, import redispatch or new owner.
Compared and verified owners remain ineligible for failed-attempt admission.
Handoff retains the held recipient and requires the original keys for manifest,
preparation and matched proof recovery; changing the configured current recipient
cannot replace retained ciphertext. Cancellation or lease loss during reads
quiesces/closes native access but prevents control-plane publication. A later
fresh lease records its exact closed unmatched owner, and a subsequent invocation
can admit a new bounded attempt. Damaged manifests, ciphertext, original parents
or native lineage cannot become verified proof.

Local PostgreSQL 16 contracts exercise real encrypted archive restoration and
independent original-manifest comparisons, successful original/retry replay,
uncertain original import state, all original/retry reservation/claim/match/
closure response losses, lost failed-attempt publication, committed native
opening/closure reply losses, actual row differences and read-budget exhaustion,
provider failures including a missing row before failed bootstrap postchecks,
original-key rotation/handoff, damaged retained proof/parents, and cancellation
or handoff during actual retry reads. Original first-worker and native/store
composition contracts remain included. Provider receipts are synthetic owned
fixtures; this qualification does not establish paid-provider permissions.

The full stage coordinator still requires production CPU/row-read/spool/billing
admission, dispatch/backoff policy, source writer closure and a common config/data
cut, full schema/globals/data strategies and final authority, durable retirement,
object copying, mixed-version rollout, PostgreSQL 14/15 and provider isolation,
and production-preserving promotion/rollback. This private worker publishes only
subordinate verification evidence. The public full database/object clone gate
remains closed.

### Durable verification read credits (2026-10-04)

The control plane now retains an aggregate planned read reservation per original
verification owner and immutable debits for its consecutive native attempts.
Reservation must precede the first claim: an original owner that has already
started, failed or compared without a hold cannot receive retroactive credits.
The reservation authenticates original contents, import, database preparation,
physical target, scope and all retained attempt history. Existing reservations
recover their original identity, caps and creation time; they cannot expand or
reset their consumed credits.

Account admission locks the operation, account and then child ownership rows.
It charges every retained aggregate reservation, including uncertain or failed
work. The existing 4,096 contents-owner ceiling also bounds reservation count.
`PostgresCopyVerificationReadBytesPerDatabaseMax` allows at most three 1 TiB
attempts, while `PostgresCopyVerificationReadBytesPerAccountMax` limits aggregate
planned read holds to 128 TiB. These are temporary structural safety ceilings in
`pkg/api/limits.go`, separate from storage entitlements and monetary billing.
Callers may choose lower count and byte limits. Replaying a charged reservation
does not count it twice.

A new debit requires the current claimed verification owner and the next
consecutive ordinal. Original dispatch requires no failed history; retries must
be the latest claimed subordinate owner. Each debit consumes its complete planned
maximum before dispatch, up to 1 TiB, and must fit the aggregate remaining credits.
Its sort memory and disk caps must fit the original reservation and the existing
8 MiB memory and 64 GiB disk bounds. Lost debit responses recover the exact first
owner, quantities and timestamp, including after proof advancement or closure.
Changing those values, skipping a debit, retroactively charging an older failed
owner or spending beyond the retained aggregate is rejected. No uncertain debit
is refunded. Lease authority is checked again after lock waits and before commit;
failed calls expose no budget metadata.

Restrictive foreign keys retain the original verification and exact retry
owners. Budget and debit reads re-authenticate their parents, account/project,
consecutive ownership, time ordering and aggregate arithmetic. Empty downgrade
and replay are supported; any owned reservation prevents destructive downgrade.
Both tables are explicitly operational in clone schema coverage. The debit table
uses `project_environment_clone_postgres_verification_read_debits` to stay within
PostgreSQL's 63-byte identifier limit.

Local PostgreSQL 16 contracts qualify concurrent reservation/debit and immutable
replay, retained charges through three closed attempts, aggregate exhaustion,
fixed sort caps, legacy/advanced/unknown-dispatch refusal, corrupt parents and
ledger rejection, lease handoff/expiry after a budget lock, migration replay,
empty round trip/owned downgrade guards and complete clone schema coverage.
These ledger tests use explicit opaque ownership fixtures. The existing APID
contracts separately qualify actual native closures and data comparisons.

This foundation does not yet wire the private verification workers to these
reservations. Production readers must be fenced during rollout and every new
comparison must consume the corresponding immutable debit. Host/CPU admission,
actual aggregate spool capacity, placement, backoff, measured usage, billing and
qualified retirement remain required. The full stage coordinator, common
config/data cut and source writer closure, complete schema/data/globals and final
authority, object copying, provider isolation and PostgreSQL 14/15 qualification,
and production-preserving promotion/rollback remain incomplete. The public full
database/object clone gate remains closed.

### Verification worker read admission (2026-10-04)

Original and retry APID verification workers now consume the durable read-credit
ledger. The original worker reserves up to three times its normalized per-read
maximum before claiming the first owner. Existing holds keep their original
read and sort caps. Workers require the optional private budget-store capability;
a store without it cannot dispatch verification SQL or read target data.

Native `WithVerificationAccessAdmitted` and
`WithVerificationRetryAccessAdmitted` invoke a required admission callback only
after authenticating the never-opened owner, closed import and exact retry
predecessor under the shared bootstrap lock. The callback commits that owner's
planned debit before native SQL opening. Fresh authorization follows admission
and surrounds opening, read-only child comparison, provider postchecks and proof
publication. Existing windows recover close-only without invoking admission or
comparison, preventing retroactive funding of uncertain legacy work. The older
native primitives remain private composable capabilities; production rollout
must fence every older data-reading worker before relying on this policy.

The admission helper authenticates the original held prefix and exact successful
store extension. A committed debit whose response was lost retains its complete
planned charge without opening a native window. A subsequent fresh worker recovers
its first read/sort quantities and timestamps, uses those caps for the actual
comparison and does not debit again. Limits configured on a later worker cannot
replace a held debit. A newly selected attempt must fit the original remaining
aggregate and sort caps. Quota denial leaves it claimed but never opened, allowing
a subsequent bounded dispatch of that same owner after the caller selects limits
that fit. Exhausted or failed reads retain their debits; no failure, cancellation,
response loss or handoff refunds them. Ledger quota rejection is mapped to the
native protocol's stable managed-PostgreSQL quota error.

Every worker authorization also re-authenticates its held ledger. Changed scopes,
parents, debits or aggregate arithmetic prevent comparison/proof publication.
Legacy matched proof with no budget may still recover its actual retained match
and native closure without data access or reader configuration. An unmatched
legacy owner may recover its exact closed native failure, but cannot receive a
retroactive hold/debit or authorize a newly owned retry. Close-only proof recovery
spends no new credits and accepts an absent reader configuration.

Local PostgreSQL 16 qualification uses actual encrypted archive restore between
independent clusters and original-manifest data comparison. It covers lost
aggregate/first-debit/retry-debit responses, debit-before-opening assertions,
changed limits after handoff, immutable matched replay, aggregate and sort cap
rejection before SQL opening, charged failed attempts, unmatched and matched
legacy recovery, missing private store capability and ledger corruption during
actual reads. Existing original/retry worker and native/store composition
contracts also retain their gates for keys, provider failures, cancellation,
uncertain import, native response loss and comparison/closure publication.
Provider observations remain synthetic fixtures; paid-provider permission
qualification is still pending.

This wires planned target-verification read credits, not complete production
resource admission. Source capture admission, CPU/host placement, aggregate spool
capacity, backoff, measured usage, billing and qualified retirement remain
required, alongside the common config/data cut and source writer closure, full
schema/data/globals and final authority, object copying, PostgreSQL 14/15 and
mixed-version qualification, full coordinator and production-preserving
promotion/rollback. The public full database/object clone gate remains closed.

### Shared contents spool admission (2026-10-04)

Private source-contents capture and original/retry target verification now share
an explicit worker-owned `copycontents.ReadPool`. The server supplies one pool
across operations and accounts. Per-attempt configuration cannot substitute
another pool; every authorization authenticates that configured owner again.
Source capture reserves before provider placement or SQL borrowing. Verification
reserves only at the authenticated never-opened admission point under the native
bootstrap lock, before a new read-credit debit and SQL opening. Capacity refusal
leaves the claimed owner never opened and spends no new debit. Once capacity
returns, that same owner may dispatch under a fresh lease. A recovered debit
selects its original read and sort caps before physical admission.

The pool explicitly bounds simultaneous readers, aggregate sort memory and
aggregate sort disk. `pkg/api/limits.go` caps these at two readers, 16 MiB sort
memory and 128 GiB sort disk per private worker, with at least 1 GiB filesystem
free-space reserve. Operators must supply explicit limits and may lower the work
caps. Reservations charge complete planned sort caps against aggregate counters
and actual available filesystem space. Existing physical consumption is also
retained in this conservative admission check. These bounds cover sort work;
they are not total process/PostgreSQL RSS, a CPU quota, storage entitlement or a
monetary allowance. VM instance admission remains owned by schedd and vmmd.

A private directory owned by the worker UID and a persistent non-symlink lock
marker have one exclusive OS-locked owner. A second process or path alias cannot
start independent accounting for that directory. The marker must never be
unlinked, and every read and sort flush/merge rechecks the retained directory and
marker inode identities. Actual sort output checks available headroom before
each run or merge. This is a local spool policy, not a reservation against other
filesystem users; they can cause later work to fail its headroom check. All copy
contents work on a node must use the configured shared directory. Deployment
still needs to establish that owner and prevent older workers from bypassing it.

An opaque reservation binds the exact normalized configuration. It cannot be
expanded, reused by another worker invocation or fund parallel contents readers.
The worker retains capacity through read rollback, sort descriptor cleanup,
provider postchecks, native closure and verification publication. Early release
while a contents reader is active revokes further access but retains the capacity
until its sort cleanup ends. Closing the pool while reservations remain is
refused. Process exit closes unlinked digest descriptors and releases the OS
lock; empty private sort directories may remain, without retained row data.
Transient capacity release does not refund durable verification read debits.
Retained manifest and matched-proof recovery remains close-only and requires no
reader configuration or new physical reservation.

Local contracts exercise aggregate concurrency/memory/disk refusal, private
directory and marker policy, aliases and a competing process, process-exit lock
recovery with a real unlinked sort descriptor, immutable configuration, released
and shared-capability refusal, free-space exhaustion and release during a read.
PostgreSQL 16 contracts use real source contents and encrypted archive restoration
between independent clusters. They cover source refusal before provider/SQL
borrowing, original/retry refusal before debit or native opening and resumption of
the same owner after handoff, missing/substituted pool refusal, held capacity
during actual reads and provider postchecks, changed spool identity during reads,
and the existing manifest/verification ownership and recovery contracts. Provider
observations remain synthetic; paid-provider permissions are unqualified.

This supplies shared local contents-spool admission to the private workers. The
public full database/object clone gate stays closed. Production worker bootstrap,
CPU enforcement and host placement, archive/import/object work admission, source
read credits, dispatch/backoff, measured usage/billing and durable retirement
remain pending, as do the common config/data cut and writer closure, complete
schema/data/globals and final authority, PostgreSQL 14/15 and mixed-version/provider
qualification, full coordinator and production-preserving promotion/rollback.

### Resumable PostgreSQL data work composition (2026-10-04)

`processProjectEnvironmentClonePostgresData` composes the private archive,
contents-reader, database-preparation, import and verification workers for one
original projected database export. It authenticates the selected OID and frozen
source scope, then uses existing durable ownership to select the next step.
Archive retention, source-contents capture, preparation and import each advance
one step per invocation. The caller can checkpoint/relinquish its operation lease
between steps. No second progress ledger or publication state is introduced.
Successful verification returns only the exact current verified subordinate
owner for the expected scope/OID and bounded attempt.

Later ownership takes precedence over producer configuration. Existing
verification runs its qualified original/retry recovery directly. A started
import first recovers the original preparation and archive identity, closes its
exact native maintenance window under full provider checks, and only then invokes
independent contents verification. This recovery does not need an archive driver,
export/import tools or scratch configuration. A retained verified or compared
owner also recovers with no reader configuration. Earlier steps never recapture
source contents or redispatch an original archive import merely because a new
worker has different configuration.

Import close-only work reads existing ownership and cannot reserve or dispatch a
missing or undispatched import. Its successful result is deliberately an empty
command receipt. An actual closed uncertain import may be independently verified,
but closure does not assert that a restore command completed. The original
importer retains its stricter command-receipt semantics. Both paths bind import
identity to the original archive receipt, physical target and child preparation;
closure and complete provider postchecks precede new verification admission.
Failures return no progress or proof metadata, including lost committed replies.

Local PostgreSQL 16 contracts drive real encrypted archive export, original
source-contents capture, native preparation, restore and independent verification
with lease handoffs between steps. They qualify executed and uncertain imports,
recovery without producer/storage/tool configuration, immutable verified replay,
lost committed match recovery, failed bootstrap postchecks before target reads,
close-only recovery without a command receipt, missing/reserved import refusal
before SQL and stale-lease refusal. These fixtures begin with qualified retained
snapshot-reader/export and target-role/database-plan inputs. Provider observations
are synthetic and do not qualify paid-provider permissions.

The durable coordinator already runs in APID and polls retained clone intent.
Its data-bearing capture path still returns `errCloneCheckpointUnavailable`
until the common configuration/data point and writer closure are secured. This
composition is a private data-work entry point; it is not yet installed in that
coordinator or a separate bounded production process. CPU/host placement and
archive/import/object resource admission, source read credits, metering/billing,
retirement, complete schema/data/globals and final authority, object copying,
PostgreSQL 14/15 and mixed-version/provider qualification, graph publication and
production-preserving promotion/rollback remain required. The public full
database/object clone gate remains closed.

### Dedicated private APID clone worker (2026-10-04)

`apid --clone-worker --config /etc/faas/apid.toml` now has a separate boot path
that loads the same intent owner and durable coordinator without opening API,
advisory, bridge or metrics listeners. It starts no provider reconciler, mail,
status evaluator or notification subscriber. The optional generated
`faas-apid-clone-worker.service` runs as `faas-apid` in `faas-cp.slice` with
`MemoryMax=1073741824`, `MemoryHigh=768M`, `CPUQuota=100%` and `TasksMax=64`.
Those hard bounds cover the process and inherited subprocesses. The process
also sets `GOMAXPROCS=1` and `GOMEMLIMIT=768MiB`. The hard limits are declared in
`pkg/api/limits.go`; they do not admit provider PostgreSQL compute or count as
customer storage/billing entitlements. The existing parent slice ceiling still
governs total control-plane memory.

Boot requires a non-root Linux process in the exact cgroup-v2 leaf
`/faas-cp.slice/faas-apid-clone-worker.service`. It reads the kernel's actual
`cpu.max`, `memory.max` and `pids.max`, rejecting unlimited/missing/malformed or
wider caps. Membership is checked again after reading those limits. Kernel
authority and the one OS-locked private contents pool are checked before each
durable queue claim; contents read reservation and active read authorization
also recheck them. Losing that authority stops this queue loop or rejects the
read. This is a host boundary, not a replacement for native SQL ownership,
provider placement checks, durable read debits or common source checkpoints.

The worker requires an authenticated fleet identity/recipient pair and HMAC
credential before installing any key accessors. Current and previous host
identities remain available for original ciphertext recovery. Its systemd
credentials omit sessions and log-archive credentials. Its only writable host
path is the private `0700` spool at `/var/spool/faas/apid-clone-worker`.
`ReadPool.CheckForWorker` checks actual directory/marker identity and the free
space reserve without allocating a reader or durable read credit. The normal
API process keeps its existing coordinator for configuration-only work and
has no private contents pool.

The Ansible role ships the unit and private spool but does not enable or start
the optional worker. Existing active installations use `try-restart` after unit
updates. Optional credentials include its previous host identity; the object
storage role projects the same provider configuration into both APID modes
and refreshes active workers when registry/credentials change. PostgreSQL pool
capacity now includes four worker connections: the declared control-plane
budget is 46, including optional data planes. The existing admission calculation
therefore requires 170 connections for a two-compute-node fleet and 620 for a
twelve-node fleet, including its rollout/operator margin.

Local verification covers actual private pool ownership checks and kernel-file
fixtures for finite bounds, arithmetic overflow, legacy/foreign/moved cgroups,
missing controls and cancellation. It proves rejected admission precedes durable
claims and new/active reads, empty queues beat the owned loop, fleet rotation is
retained and failed key loads do not partially replace accessors. Generated
unit round trips/diff, provider/credential delivery, pool capacity and existing
configuration coordinator regressions are qualified separately from the 38
real PostgreSQL integration roots for the preceding data-work composition.
Native Linux systemd cgroup/process acceptance remains outstanding.

This boot path is installed for the existing durable coordinator. The new
PostgreSQL data-work composition still needs the common checkpoint and final
database/global authority before it can be installed there. Archive/import and
object admission, source read credits, measured metering/billing and owned
retirement also remain required. Enabling this private service does not enable
public full clones. PostgreSQL 14/15, mixed-version and paid-provider
qualification, complete configuration/object coverage, graph publication and
production-preserving promotion/rollback remain in the complete contract.


### Provider service for native PostgreSQL admission closure (2026-10-04)

The managed PostgreSQL service now exposes the optional private
`CheckpointConnectionClosureProvider` capability. Closing consumes the exact
operation owner, source dataset, authenticated ready maintenance owner and
selected database names. A separate observation consumes the same selection
and performs no installation or close dispatch. Backend fingerprint, region,
captured specification and separate maintenance/barrier owners are validated
before provider IO. Ownership recovery continues when provisioning is disabled.
The selection is bounded at 1,024 databases by
`api.PostgresCheckpointDatabasesMax`; oversized or invalid names/sets are refused
before IO and never truncated. PostgreSQL identifier lengths are measured in
bytes. Caller selection and returned database evidence have independent slices;
ordinary JSON and formatted output redact the private identities and names.

The Neon adapter uses one direct, verify-full connection to the ready private
maintenance database. It independently rechecks the captured endpoint/host and
major version after connecting and after native work. The original maintenance
owner, database OID, owner OID, bootstrap marker, SQL role and major version are
authenticated before dispatch and after observation. Existing abandonment and
terminal observation share these checks. A failed connector closes any pool it
returned and sanitizes connection errors. Cancellation or any failed postcheck
returns zero evidence, including after an actual closure committed; durable
source recovery authority must remain held for the same owner.

A valid observation requires the exact sorted database set, unique nonzero
OIDs, original owners, a finite PostgreSQL-precision closure time, nonnegative
session counts and a consistent drained flag. Existing sessions can continue
writing after admission closes. Closure retries recover the original timestamp,
OIDs and original connection flags; an observation cannot adopt another set or
owner. Released/abandoned records cannot supply closed admission evidence.
Neither `ClosedAt` nor an empty session count chooses a common capture point or
attests complete source/background writer coverage.

The data-bearing capture gate remains closed. The coordinator still needs a
qualified durable selection covering every source database, all application and
external/background writer barriers, the common frozen configuration/database/
object checkpoint, retained-source proof and successful barrier release before
it can install the native data composition. Paid Neon privileges and behavior,
PostgreSQL 14/15 and mixed-version operation, complete schema/data/globals and
final authority, resource admission/usage/retirement, object/configuration
coverage and production-preserving promotion/rollback remain required.


Verification: 208 original top-level contracts passed across managedpostgres,
Neon and connectionfence, without test or production overlays. The paid live
provider lifecycle contract was explicitly excluded. PostgreSQL 16 tests use a
fresh isolated native cluster, real private maintenance bootstrap and SQL
closure, synthetic provider HTTP placement and a private test connector mapping
the authenticated endpoint to that cluster. They qualify admission versus
existing-session writes, independent drainage, original closure recovery,
selection/owner substitution, observation without installation, refusal before
SQL for changed maintenance OIDs/owners/major or placement, and recovery after
committed SQL followed by failed native/provider checks or cancellation. Fourteen
relevant service/adapter roots passed again after adding final context checks
before returning evidence. The normal APID production build passed; 526 actual
production files were pinned and checked. The exact owned PostgreSQL process,
roles/databases, RAM fixture and generated binary were retired after verification.

The first package run exposed an overly broad privacy-test sample (the database
name `a` also matched ordinary redaction text); the sample was corrected without
weakening the assertion. One original native cancelled-close contract received
an installation refusal before dispatch on that run. Its isolated rerun and the
complete native package rerun passed. That transient refusal was not reproduced;
these local checks do not qualify deployed provider availability, a common
checkpoint, full repository acceptance or native Linux worker enforcement.


### Retained original PostgreSQL selection before checkpoint (2026-10-04)

`checkpointselection` now seals an immutable original database-name set before
a coordinated capture point exists. Its scope binds the operation, account,
project, frozen database definition hash, source database/backend/dataset,
PostgreSQL major and ready private maintenance UUID/OIDs. Names are canonical,
bounded by `api.PostgresCheckpointDatabasesMax`, and exclude the maintenance
database so its recovery connection stays accessible. A random HMAC key and
the names remain inside a namespaced age envelope. The public fingerprint is
keyed; ordinary JSON and formatting redact the selection. Recovery accepts
retained previous fleet keys but never substitutes a new selection/key after
damage, missing decryption keys or worker takeover.

The private control-plane receipt is write once under the original source
recovery hold. Its SQLC transactions derive scope from the authenticated frozen
capture and locked live source/ready maintenance receipt. Current worker token,
revision, phase and lease expiry are checked after lock waits and before commit.
An exact replay returns the original ciphertext/time; a replacement conflicts.
Foreign-key restrictions preserve its original fence and maintenance recovery
parents. Compensation can read the original while the source is abandoning,
but cannot rewrite it. The schema registry classifies this table as operational
recovery intent. It is not stage configuration or successful capture evidence.

The private APID selection helper recovers the original receipt before invoking
a producer. New reads require usable fleet keys, worker admission and a lease
deadline. Cancelled reads cannot retain a new selection. The connection-closure
worker accepts only that retained original and recomputes the supplied frozen
definition hash before remote IO. It reuses and independently authenticates the
ready maintenance owner, checks admission before maintenance/close/observation,
and separately observes the exact closure. Original database OIDs, owner OIDs,
connection flags and closure time must agree, while current session counts may
change. A final control-plane read checks current lease, placement, scope and
retained ciphertext identity. Lost replies, changed pins, cancellation, failed
admission or lease handoff return zero evidence and retain source recovery
authority, including after remote closure committed.

These helpers are private and are not installed in the capture coordinator.
The inventory producer still needs complete provider/native database coverage
and all application/external/background writer barriers. Neither a retained
selection nor closure/drainage establishes the common configuration/database/
object point, source retention, successful barrier release or stage readiness.
Original receipt retirement/account deletion, complete PostgreSQL authority and
schema/data fidelity, object/configuration coverage, admission/usage, paid
provider and native Linux qualification, and production-preserving promotion/
rollback remain part of the full clone contract. The data-bearing gate remains
closed.

Verification: 42 original top-level contracts passed (five encryption, 21 state,
16 APID), without skips. State/APID use test-only focused overlays preserving
43/149 original declarations respectively, with zero production replacements.
Real isolated PostgreSQL control-plane persistence qualifies write-once replay,
lost committed replies, key rotation, stale workers, ready owner/live placement,
source-lock lease expiry, independent pin comparison, post-closure admission/
cancellation/handoff and preservation of the held source without target
reservation or publication. Provider closure observations in these new APID
contracts are synthetic; native SQL/provider acceptance remains the separately
qualified preceding layer. The normal production APID build passed and 1,025
actual production files were pinned and checked. The real migrated schema
matches the live dump after normalizing trailing whitespace, replaying the new
migration against existing shape succeeds, and SQLC regeneration matches.

Initial local verification found an unsupported registry field and a test that
set the live major to its existing value. Both tests were repaired and the
focused suites passed. The first owned schema dump populated the test root;
only that empty owned schema was retained in a separate fixture database and
the owned test root was recreated from template0 before successful integration
checks. This did not alter another process or database. Exact owned test
databases, PostgreSQL process, RAM fixture and generated binary are retired
after qualification. Full repository, deployed provider and native Linux
acceptance are still outstanding.
