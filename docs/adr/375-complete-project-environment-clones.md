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
control-plane dispatch registry and worker integration are still required.

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
