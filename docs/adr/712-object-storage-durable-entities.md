# ADR-712 · Object-storage durable entities

- **Status:** accepted for an internal prototype and opt-in invocation/alarm/maintenance/inventory preview; native runtime and provider qualification pending
- **Date:** 2026-10-07
- **Decision:** Implement a SQL-free entity state engine using immutable JSON snapshots, a receipt index and one conditionally updated manifest per account/app/environment/customer/namespace/key. Ownership, state publication and cleanup fencing share that manifest. Add a narrow conditional-object capability to the existing S3 and native GCS providers, an opt-in authenticated invocation API, scheduled wake-ups, bounded cleanup with durable scan checkpoints, operational metrics and a runnable counter example.
- **Why:** Applications need persistent logical state without allocating a permanent VM or customer disk. PostgreSQL-backed exclusive operations do not provide this object-storage durability contract, and artifact storage lacks compare-and-swap.
- **Consequences:** An acknowledged transition has published its snapshot through a successful manifest compare-and-swap. Takeover changes the same manifest, fencing obsolete commits. Request receipts and results commit with state; uncertain responses are resolved by retrying the same request identity and payload. Entity state has no SQL dependency. Existing control-plane ownership, workload isolation and stateless deployment rules remain in force. This prototype is not a launched product capability.
- **Rejected alternatives:** Unconditional snapshot overwrites permit lost updates. Separate lease and state objects leave a race between ownership checks and commit. Unrooted receipt objects falsely treat unsuccessful uploads as committed work. Age-based cleanup can race an in-flight publisher. Local-only state cannot survive compute loss. Adopting an actor runtime now would couple storage evaluation to execution-language changes.

## First milestone

`pkg/durableentity` supports acquisition, renewal, release, read/restore and
serialized in-process transitions. Cross-process competitors are fenced by
manifest compare-and-swap. An activation uses a distinct private claim token and
an increasing epoch; all manifest mutations also carry a fresh revision to avoid
ETag ABA. The SDK must not automatically retry conditional writes after dispatch.

The engine probes conditional create, conditional replacement and immediate reads
before opening. These checks reject stores that ignore conditions; production
qualification still requires provider-backed crash, partition and latency/cost
evidence. A dedicated private platform bucket is required. Customer credentials,
public caching, lifecycle deletion and direct writes must not reach entity data.

Clock-based leases determine when another owner may attempt takeover. Hosts need
synchronized clocks for predictable lease behavior; the manifest CAS and epoch,
not the clock, fence publication after takeover. Callbacks compute a transition
and may not perform external effects. The first milestone bounded inline request
receipts; the journal milestone below removes that count ceiling while preserving
the original retry contract.

The preview implements the conditional-state capability for S3 and native GCS.
Both must pass the same startup probe. Native runtime and live-bucket
qualification remain pending; ordinary object storage support does not qualify
entity state.

### Native GCS generations — 2026-10-08

The existing OAuth/ADC client implements the private conditional-state
capability directly through the native JSON API. A read returns the content and
its generation from the same response. Generations are canonical positive
decimal opaque version tokens; HTTP ETags and metagenerations are not used for
state ownership.

Create uses `ifGenerationMatch=0`; replace uses the observed content generation.
Writes disable SDK retries and use a single non-resumable upload. The SDK checks
the response checksum, and the adapter requires a new positive generation and
the expected size before acknowledging success. Only HTTP 412 is a definite
generation rejection; other failed dispatched uploads remain uncertain.
Request-ID receipt replay resolves a lost response after successful publication.

Reads enforce the caller's byte bound and reject unknown-sized, encoded or
decompressed content and missing generations. Backend identity/fingerprint,
the dedicated private bucket and all preview opt-ins remain mandatory. The
existing GCS listing/deletion capabilities serve alarms, inventory and cleanup.

SDK-backed wire conformance tests exercise restart restoration, exact original
retry results after publication ACK loss, competing CAS writers,
identical-content generation changes, stale in-flight owner fencing and
fail-closed startup when a provider ignores generation conditions. These
fixtures do not qualify a live GCS bucket, network partition behavior, native
microVM execution or production latency/cost.

### Shared provider tooling — 2026-10-08

The trusted counter/maintenance command and opt-in live qualification harness
select S3 or native GCS through one configuration helper. S3 remains the default
for existing commands. GCS uses ADC and an optional impersonation target;
legacy S3 endpoints and credential settings never configure the GCS client.
The existing bucket is operator-selected; this tooling does not provision it,
modify platform preview settings or bypass the invocation authorization rules.

The shared live harness checks restart, uncertain publication replay, concurrency,
stale-owner fencing, an actual killed-owner process, alarm discovery/replay,
inventory and committed-byte caps. Cleanup requires a separate explicit opt-in
and preserves original receipts. Reports identify the provider and distinguish
live-bucket runs from the shared S3 wire-fixture check. Native GCS SDK wire tests
also cover paginated inventory, caps, cleanup and alarm listing/delivery/replay.
These fixtures provide CI evidence only; the dedicated-host and live-provider
acceptance gates remain pending. See the runnable configurations and qualification
commands in [the operator guide](../../examples/durable-entities/README.md).

Alarm deadlines persist with state. The opt-in alarm milestone below adds
discovery/delivery; owner-affinity routing, metering,
entity deletion and cross-entity
transactions are later milestones. No customer code runs on the host as part of
this prototype. Snapshot integrity
and identity are checked on restore; failure never substitutes empty state for a
missing or corrupt committed snapshot.

## Invocation preview

`POST /v1/apps/{slug}/entities/invoke` is disabled by default and requires an
explicit operator app allowlist, a dedicated private bucket, and a configured
backend's immutable placement fingerprint. Boot runs the conditional-storage
probe and fails closed if it fails. The endpoint uses the existing account
authentication, rate limit, deploy-write scope and configured MFA middleware.
It does not accept customer self-service credentials. The authenticated app
lookup supplies account/app identities. A selected customer must belong to the
account and be active. Project environments use their catalog UUID, including
production; a deleted/recreated environment cannot inherit its predecessor's
entities. Standalone and preview apps use their immutable app ID as the default
environment boundary. Namespace/key are business identity, not authorization.
The optional scope fields preserve the original unscoped development harness's
keys, but runtime ingress always supplies its environment identity.

Apid acquires the entity for one call under a distinct process-incarnation ID.
Local calls serialize; cross-process contenders receive a retryable busy error.
There is no permanent entity worker or one-VM-per-entity allocation. The engine
checks committed receipts before code selection, so successful retries do not
depend on the original deployment remaining live. New work captures an exact
revision or project release using the established pin resolver and existing
environment-owned invocation admission. Revision retention must be enabled.
Schedd remains the invocation and VM lifecycle owner; normal gateway dispatch
delivers the state/payload envelope to the app's `/__gregale/entities` handler.
The existing PostgreSQL execution ledger is dispatch metadata and is never the
authoritative entity state, receipt or ownership store.

The versioned guest envelope carries verified entity scope, request identity,
business state/version/alarm, payload and selected deployment. Claims, bucket
credentials and manifest keys stay in apid. The guest returns only JSON data,
result and optional alarm. Invalid, unsuccessful or late responses cannot
publish state. Handlers must compute transitions without external effects:
dispatch retries or a stale in-flight guest may still compute a response.
The JSON body is not a workload credential: ordinary public HTTP callers must
not gain access to external customer data by posting a forged entity envelope.
The preview counter consumes only the supplied state and payload; bucket
publication remains private to apid.
The invocation has one attempt and a deadline; final results are polled from the
durable ledger so missing notifications do not hide completion. The 25-second
request ceiling inherits a shorter caller budget. A five-minute ownership lease
avoids concurrent renewal in this slice and bounds recovery after apid loss.
Release uses a short independent cleanup budget; failed cleanup cannot turn a
committed result into a reported failure and delays competitors until expiry.

The Go SDK requires a stable request ID and preserves entity selectors across
retries. The HTTP response cache does not wrap this route: every retry rechecks
current authorization and resolves durable receipts in the bucket. An opt-in
live S3/GCS qualification test writes an isolated retained prefix, checks restart,
concurrency, acknowledgement loss and fencing, and kills an actual owner process.
Running this harness against a local S3 wire fixture verifies the harness only;
live-provider and native microVM evidence remain release gates. Provider request
counts and elapsed time are reported; price and production latency targets need
measurement against the selected backend.

The guarded native e2e runner also includes
`TestDurableEntityNativeParkRestoreMetal` in its wake phase and required-test
contract. It deploys a static pure counter through the normal invocation ledger,
imaged, schedd and Firecracker path. Two park/restore cycles verify new wake IDs
and explicit `restore` events. It kills and replaces only its own apid child
after committed work, then checks original receipt replay without guest wake or
additional handler dispatch, continued count/version, request conflicts and a
failed guest response that cannot publish a transition. Private fixture
credentials are added only to the apid launch recipe.

Its conditional S3 wire fixture lives in the test process and survives apid
replacement. Native evidence explicitly identifies the fixture provider,
verified restore wake IDs and `live_provider_qualified:false`. Compilation or
local fixture tests are not native acceptance. This case does not qualify
in-flight owner takeover, alarms after native parking, provider durability,
latency or billing; the provider and deployed SDK acceptance remain separate.
Use the existing exact-commit, dedicated-host runner and final leakcheck in
[native e2e CI](../ops/e2e-native-ci.md).

## Original alarm delivery preview

The discovery and retry behavior below records the first alarm milestone; the
indexed delivery milestone supersedes its full-scan delivery and unbounded retry
policy.

`FAAS_DURABLE_ENTITY_ALARMS_ENABLED=1` requires the invocation preview and
private delimiter listing; startup fails closed without that capability or
permission. Apid scans at most eight entity directories per page, using a
rotating native cursor. Delimiter listing skips retained snapshots. Each entity
read has a two-second budget; the page has a twenty-second budget. Discovery
restores the committed snapshot, including deadlines saved before enabling
delivery. Bad entities fail closed individually. Cursors are disposable process
state; restart rediscovers alarms from the bucket without a SQL alarm index.

Apid rechecks account status/plan, app allowance/deletion, active customer and
the immutable project environment before creating guest execution intent. Stage
invocations retain the expected environment UUID through admission. Schedd owns
the actual dispatch and wake, with normal execution admission and metering. An
alarm has the same twenty-five-second call ceiling and five-minute ownership
lease as a caller invocation. Each process dispatches one alarm at a time and
waits five seconds after its page completes before scanning again.

The guest envelope adds `event`, with `invoke` for caller work and `alarm` for
scheduled work. Alarm payloads contain `type`, `scheduled_at` and the observed
`state_version`. A reserved request ID derived from that version is stable
across workers and restarts; ordinary callers cannot submit its namespace.
Under entity ownership, a changed version, cleared deadline or replaced deadline
skips the old observation before guest dispatch. A preserved deadline in newer
state is rediscovered. Successful state, receipt and replacement/cleared alarm
commit together. A lost commit acknowledgement is recovered by receipt replay.
Failed, malformed or cancelled handlers leave the original deadline due.

Handlers clear an alarm by omitting/nulling `alarm_at` or rearm it by returning
a deadline. Keeping the same due deadline rearms it in the new version and can
cause repeated deliveries; the counter clears its alarm after incrementing.
Attempts are at least once and handlers remain pure. This is eventual delivery,
with no deadline ordering or latency SLO. Scanning all entities costs reads,
and a slow page can delay later pages. Alarm receipts use the same immutable
receipt index as caller work. A due-time index, bounded retry/dead-letter policy,
plan quotas/billing and provider-backed operational qualification remain production
work. Entity alarm authority and receipts stay in object storage; the existing
SQL ledger records guest attempts only.

## Indexed alarm delivery and bounded retries — 2026-10-08

The opt-in alarm worker reads one bounded, lexically time-ordered index page and
one independent rotating entity page per sweep (eight entries each). Committed
snapshots remain alarm authority. Index publication after a successful state CAS
is best effort and has a two-second bound; a failure cannot revoke the commit.
The entity scan repairs missing hints, including pre-upgrade alarms and crashes
between state/reservation publication and index upload. It can deliver recovered
due work in the same sweep. Each scan retains its twenty-second budget. Native
cursors are disposable, reset independently on errors, and are not SQL metadata.

Immutable hints carry identity, version, original deadline, effective retry time
and reservation count. Future hints end a pass before entity reads. Due candidates
are restored from the manifest/snapshot and checked again under fenced ownership.
Stale or corrupt hints can be pruned without touching entity state. Superseded
future hints remain until their indexed due time; bucket lifecycle deletion stays
disabled. Startup additionally requires flat LIST and private hint DELETE and
verifies DELETE using only a unique probe. Native S3/GCS listing order is required.
The index accelerates ready-work discovery, while reconciliation still costs a
bounded ongoing scan. This preview promises neither deadline ordering nor latency.

Before new alarm guest work, the entity manifest CAS durably reserves one attempt
and the earliest next attempt time. Only an acknowledged reservation dispatches.
Retry delays start at thirty seconds and double up to five minutes. Five reserved
attempts exhaust an alarm for that committed state version. Busy ownership and
obsolete observations do not reserve; rejected CAS does not advance the count.
Accepted-but-unacknowledged reservations and process death consume the reservation,
so this is a bounded attempt budget rather than a guarantee of five guest executions.
A valid receipt is checked before retry gates, preserving final-ACK-loss replay.
Admission remains rechecked before reservation. The existing execution ledger records
guest intent only and does not govern retry authority.

Manifest writes use schema 4 to fence older writers that would drop reservations.
Readers accept schemas 1–4; stop all older entity callers and background workers
before upgrading, and migrate storage before downgrading. Successful state commits
clear old retry metadata and derive a fresh hint from the new alarm. Other manifest
mutations preserve reservations. Snapshots, receipts, caps and guest protocol stay
unchanged. Advisory hint storage is excluded from committed-state caps, but its
upload volume and provider operations are observed by the existing bounded metrics.

Exhaustion retains the original state/deadline and suppresses automatic dispatch.
The final reserved attempt can still be in flight. The trusted harness's
`-alarm-status` reads one exact entity scope without claiming it, reporting alarm
identity, attempt count, retry time and exhaustion without payloads or raw errors.
A deliberate business transition can clear or rearm it in a new version. Public
inspection, automatic dead-letter replay, index compaction, plan quotas and billing
remain separate milestones. All preview gates remain off by default. CI fixtures
cover missing/corrupt hints, restart/backoff/exhaustion, uncertain reservations,
receipt replay and native provider wire paths; dedicated-host/live-bucket, latency
and cost qualification remain pending.

## Internal outbox commit contract — 2026-10-09

[ADR-829](829-object-storage-entity-outbox-contract.md) adds bounded outgoing
intents to the private Go engine transition. State, alarm, receipt and pending
messages publish through the same manifest CAS. Later transitions, receipt
replays, restart and cleanup preserve pending work. Deterministic message IDs
include full entity scope, committed state version and batch ordinal. Pending
message bytes count inside existing snapshot/storage caps. Exact-scope private
observation restores only committed work; there is no delivery worker or guest
outbox field in this slice. Customer handlers remain pure.

Current manifest writes use schema 5 (readers accept 1–5); snapshots containing
messages use schema 3 (readers accept 1–3). This supersedes the schema-4 writer
version above without changing alarm reservations. Stop all older entity callers,
alarm and maintenance workers before upgrading. Downgrade requires an explicit
storage migration. The preview and native/live-provider qualification status
remain unchanged; destination admission, deduplicating relay acceptance, fenced
acknowledgement, retry and external delivery remain the next milestone.

## Receipt journal and operator cleanup

Schema-2 snapshots contain business state, alarm state and roots for an immutable
receipt index. Each leaf stores the request ID, exact payload fingerprint, original
JSON result and original committed version. A path-compressed hexadecimal radix
index over SHA-256 request IDs bounds a lookup to at most 65 nodes. Branches are
bounded to 16 KiB and receipt objects to 1 MiB. New work copies only its changed
search path; a manifest CAS publishes the snapshot and receipt roots together.
Unpublished objects never constitute replay evidence, even after later state
versions advance. Receipts do not expire and there is no receipt-count ceiling.
Retained storage still grows with the number and size of committed results;
production quota, pricing and native-provider cost evidence remain required.

Schema-1 snapshots remain readable. The next successful new transition archives
their inline receipts once in a bounded immutable block, then uses the new index.
Old retries retain their exact result/version and conflicting payloads still fail.
The archive remains reachable for the entity's lifetime. No SQL table or migration
is introduced. Manifest writes now use schema 3, which binaries supporting only
schemas 1/2 reject. It includes accounting and the persisted cap alongside ownership,
root and generation metadata, including maintenance-only writes. Stop all old
entity callers, alarm and maintenance workers for the upgrade; downgrade after
a schema-3 manifest requires a storage migration.
The guest and public invocation protocol remain version 1.

`Manager.Collect` is an explicit private operation, exposed through the trusted
operator harness's `-cleanup` command. It requires ordinary bucket LIST and DELETE
in addition to conditional GET/PUT. The opt-in background worker below uses the
same collector; there is no public cleanup API. Each call handles at most 32 objects and has a
20-second budget. The caller holds an entity claim; maintenance preserves the
business-state version, alarms, receipts and claim epoch.

Before deletion, the collector validates committed snapshot/index roots and CAS
advances a separate storage generation in the manifest. New immutable object paths
include the generation. Publication checks that the generation captured before
computation is still current, including after the final authority reload. A lost
barrier acknowledgement causes no deletion. A competing takeover or state commit
that wins the CAS prevents the barrier from publishing.

The collector deletes only objects from earlier generations that are unreachable
from its barrier snapshot: superseded/uncommitted state snapshots, obsolete index
branches and unpublished receipt leaves/archives. Receipt paths encode their hash
prefix; lookup in the authoritative index proves whether that exact object is
reachable without walking all receipts. Live leaves, shared branches and the legacy
archive remain. Later commits can retain old objects only through that rooted
lineage; they cannot resurrect an unreachable old candidate. A delayed older PUT
may leave another orphan, but cannot publish it after the barrier and can be swept
later. New-generation uploads, manifests, unknown paths and probe objects are
retained. No lease expiry or object age alone proves that deletion is safe.

Native list cursors are disposable hints. Restarting from an empty cursor is safe;
failed or uncertain deletions are revisited on a later sweep. Keys are immutable
and never reused. A reader whose old snapshot/index was reclaimed reports a
retryable conflict when fresh authority changed; missing current committed data
still fails closed. External lifecycle deletion remains forbidden. Versioned
buckets may retain noncurrent versions/delete markers: this collector removes
current keys and makes no claim of reclaiming those physical versions or backups.
Entity deletion and recovery from storage corruption remain
separate production work.

## Opt-in automatic maintenance and metrics

`FAAS_DURABLE_ENTITY_MAINTENANCE_ENABLED=1` requires the invocation preview and
its explicit app UUID allowlist. It is disabled by default and independent of
alarm delivery. Startup verifies private delimiter listing and flat listing,
creates a unique maintenance probe, deletes only that probe and verifies its
absence. A failed permission/capability probe prevents startup; enabling ordinary
invocation does not require DELETE access.

The worker runs one step immediately and then waits 30 seconds after each step.
A step has a 45-second budget. It discovers at most eight entity directories,
visits at most one entity and processes one bounded cleanup or inventory page
under an ordinary entity claim. Completed cleanup sweeps alternate with inventory
scans; an entity without accounting starts with inventory. Each page visits at
most 32 candidate keys or journal nodes. Busy owners and disallowed or corrupt entities do not hold the
directory rotation. A large receipt journal gets one page per visit rather than
monopolizing the worker. The collector's generation barrier and rooted reachability
proof remain the only authority to delete an immutable object.

Scan progress lives in a conditional object under
`gregale/durable-entities/v1/maintenance/<allowlist-hash>.json`. The hash uses
sorted enabled app identities, so workers with different allowlists have separate
rotations. A five-minute checkpoint lease and CAS serialize workers with the same
allowlist, including workers sharing an owner label. The checkpoint stores the
pending directory page and its continuation cursor. Each entity's independent
`maintenance.json` stores its cleanup cursor and inventory/cleanup mode; both
checkpoint types are bounded
to 128 KiB and never become replay or deletion evidence. Their cursors are opaque
hints. A listing error resets the affected cursor for the next visit. Corrupt
checkpoint objects fail closed and require operator repair.

After a crash, another worker resumes pending directories when the checkpoint
lease expires. A crash between collection and checkpoint publication can repeat
a page safely. Uncertain acquisition acknowledgements cause no deletion and can
leave the checkpoint busy until expiry. Stale checkpoint owners cannot overwrite
a successor's progress. Failed/uncertain deletes are revisited in later rotations.
This offers eventual maintenance with no completion-time guarantee: scan cost
grows with entity count, retained receipt history and active-writer contention.
The worker has no persistent local disk or SQL metadata dependency and does not
dispatch customer code. Housekeeping uses the operator allowlist even for held
accounts/environments, while preserving their committed state and receipts.

Prometheus collectors use the apid metric prefix and fixed operation/outcome/kind
labels, without account, app, entity, request, object key or provider-error labels:

- `*_durable_entity_storage_operations_total`: GET, PUT, directory/flat LIST and
  DELETE calls, including conditional conflicts and uncertain dispatched writes.
- `*_durable_entity_uploaded_bytes_total`: acknowledged encoded upload volume by
  object kind. This includes manifest rewrites and unpublished uploads; it is not
  a retained-storage inventory, physical bucket size or billing measurement.
- `*_durable_entity_operations_total`: invocation, alarm and maintenance success,
  replay, busy, conflict, stale, uncertain, invalid, limit, pending, skipped and failed
  outcomes.
- `*_durable_entity_cleanup_objects_total`: deleted, retained and failed candidates.
- `*_durable_entity_maintenance_recoveries_total`: expired checkpoint takeovers.
- `*_durable_entity_maintenance_last_sweep_timestamp_seconds`: completed discovery
  rotations, including busy/skipped/failed entities; not proof every object was cleaned.
- `*_durable_entity_alarm_delay_seconds`: elapsed deadline-to-delivery-attempt time,
  including deferred attempts, with no delivery latency SLO.
- `*_durable_entity_inventory_samples_total`: partial, complete or completed with
  current-key byte sizes unavailable.
- `*_durable_entity_committed_bytes`: histogram of exact reachable immutable bytes
  in completed per-entity samples. Repeated samples are not an aggregate inventory.
- `*_durable_entity_current_key_bytes`: histogram of completed provider-reported
  current-key scans, including metadata and orphans. Scans are observational,
  exclude native version history/backups and are not provider billing evidence.

Committed receipts still grow indefinitely. Plan quotas, billing, retry/dead-letter
policy and real-provider maintenance cost/latency evidence remain required before
production availability. Versioned history and backups remain
outside the collector's physical reclamation contract.

## Exact committed storage accounting and operator preview caps

A schema-3 manifest carries exact encoded byte counts for the current snapshot,
reachable immutable receipt leaves, reachable index branches and a referenced
legacy archive, together with the receipt count. A checksum binds those counts to
the entity identity, state version and snapshot key/hash. Every new commit plans
its immutable objects in bounded memory, subtracts replaced index branches,
replaces the old snapshot size, and publishes the updated root and accounting in
one manifest CAS. Manifest rewrites, checkpoints, probes, abandoned uploads,
superseded objects, native version history, delete markers and backups are outside
this committed-storage measurement. This is per-entity accounting, not an account,
app or provider storage invoice.

`FAAS_DURABLE_ENTITY_MAX_RETAINED_BYTES` optionally configures a canonical positive
decimal byte cap per entity. It has no default and does not create a priced plan
quota. Acquisition persists the stricter of the configured and existing cap.
An uncapped or more permissive replica cannot remove an existing cap. Raising or
removing one requires an explicit private operator `Manager.SetStorageLimit` call
or the harness's `-set-storage-limit` command. Align all configured replica caps
with an operator increase; a stricter configured cap is reapplied on acquisition.
The operator can set zero explicitly to remove a persisted cap. Lowering a cap
preserves all state and receipts, even if current usage is already above it.

A transition whose exact projected committed bytes exceed the cap receives HTTP
409 `durable_entity_storage_limit` with `limit`, `observed` and `docs_url`.
Preflight rejects it before any immutable object upload. Publication reloads the
latest persisted cap/accounting and checks the final CAS, so a concurrently
lowered cap cannot admit an oversized root. A late race or lost write response
may leave uncommitted objects for cleanup; the cap does not bound physical bucket
usage. Pure handler computation can run before rejection. Existing receipts
replay before quota admission; state, alarm deadline and receipt count do not
change on rejection. Successful state shrinkage may reduce committed bytes;
cleanup never expires receipts to free quota.

Older nonempty manifests lack accounting. They remain readable and replayable.
With a positive cap, new work returns HTTP 503
`durable_entity_inventory_pending` before dispatching a handler until a verified
inventory finishes the logical proof. Without a cap, new work can continue, but
changes of the snapshot root restart an unfinished proof. Empty entities start
with known zero usage.

`Manager.Inventory` proves logical usage by following hash-validated snapshot,
archive and radix-index references. It never derives quota authority from LIST
sizes or uncommitted object contents. Each call has a 20-second budget and visits
at most 32 journal nodes or 32 current-key records. A deterministic depth-first
proof stack (at most 65 × 16 pending references) and checksummed progress live in
`inventory.json`, bounded to 1 MiB. The snapshot identity/version pins the proof;
changed roots restart it, corrupt or missing reachable data fail closed, and
manifest CAS fences ownership changes. Once the proof completes, missing usage
is published under the claim; known usage is audited against the proof. Progress
survives restart and uncertain checkpoint writes without double counting.

The same scan then optionally observes provider-reported current-key sizes using
bounded flat listing. Missing/invalid sizes make current bytes unavailable; native
cursor errors reset only that observation. Changes across listing pages mean it
is not a transactional bucket inventory. Logical inventory works without LIST or
DELETE; automatic maintenance still requires both for cleanup. Run the trusted
harness with the exact runtime account/app/environment/customer/namespace/key and
`-inventory`, repeating until `complete` is true. A busy claim can be retried after
release/expiry. Automatic maintenance provides this migration and auditing for
allowlisted apps when separately enabled. There is no public quota-management or
inventory API, no receipt expiry and no new SQL metadata table.

## Local verification record · 2026-10-07

Race-enabled engine, request-budget, API client and Go SDK tests passed. The
invocation API tests compiled all production apid files with the focused entity
and OpenAPI compliance tests; the entire apid test suite was not run. The local
S3 qualification fixture exercised an actual owner subprocess kill, restart,
fencing and replay with an unreachable SQL endpoint. The trusted counter HTTP
smoke test and source-manifest parser check passed. OpenAPI lint reported zero
errors, and the embedded spec and SDK API mirrors match their sources. Full
repository Go lint completed with zero issues, and repository policy checks
passed.

The alarm milestone passed race-enabled engine and focused runtime tests for
bounded directory discovery, concurrent workers, changed/cleared deadlines,
lost commit acknowledgements, failed guest responses and current customer,
account, app and environment admission. The S3 wire qualification fixture also
checks delimiter discovery and alarm receipt replay with SQL unreachable. The
counter HTTP smoke passed scheduling, preservation, delivery, clearing, invalid
deadline and overflow cases. The Go SDK acceptance harness has an additional
opt-in alarm case; its live execution remains pending. Full repository lint was
rerun after the alarm additions and again completed with zero issues.

The additional whole-module SDK lint run reported 18 existing issues in ten
files identical to HEAD; none were reported in the entity additions. No live
bucket or native deployed counter was available in this workspace. Their
opt-in qualification commands remain pending, so this evidence does not promote
the preview to production availability.

The journal/cleanup milestone passed race-enabled engine/harness tests covering
1,057 requests with more than 1 MiB of retained original results in a main snapshot
under 1 KiB, legacy receipt migration, restart/replay after collection, orphan
receipt exclusion, corrupt indexes, generation fencing during callback/upload/CAS,
concurrent commits, changed-authority reads and uncertain barriers/deletions. The
local S3 fixture also exercises flat listing/deletion and retained receipt replay
through the native provider adapter with SQL unreachable. Focused API, API client
and request-budget race tests passed, as did targeted Go lint, repository OpenAPI
lint, embedded/SDK mirrors and documentation links. Full `make lint` completed
with zero issues. Live provider cleanup and native workload acceptance remain
pending.

The automatic maintenance milestone passed race-enabled engine tests for fair
rotation across large and busy entities, persisted directory/entity cursors,
expired cursor recovery, competing workers, expired-owner takeover, lost/rejected
checkpoint acknowledgements and corrupt/disallowed entity isolation. The S3 wire
fixture exercises startup listing/deletion probes, checkpoint restart and replay
after collection with SQL unreachable. Focused apid tests cover initial worker
execution/cancellation, retained API replay and alarms, opt-in DELETE permission
checks, sanitized startup failures, metric registration/rebinding and bounded
labels. API client and request-budget race tests also passed. Full `make lint`
completed with zero issues, and documentation links, formatting and API mirrors
passed. Live provider and native workload acceptance remain pending.

The storage accounting milestone passed race-enabled engine/harness tests for
exact reachable bytes across radix path copies and cleanup, exact cap boundaries,
rejection before uploads, replay below a lowered cap, persisted caps across
replicas, concurrent cap changes during callback/upload/CAS, state shrinkage,
uncertain publication recovery and accounting bootstrapped during a callback.
Inventory tests cover bounded restartable legacy scans, inline receipt migration,
changed-root restart after cleanup, untrusted/missing/invalid LIST sizes, lost
checkpoint acknowledgements, corrupt leaves/checkpoints/accounting, expired native
cursor recovery and maintenance migration priority. The S3 wire qualification
fixture additionally checks native current-key sizes, quota enforcement and replay
at capacity with SQL unreachable. Focused apid race tests cover real API cap
rejection/replay, stable RFC 7807 quota/pending problems, canonical configuration
and completed-sample metrics. Targeted lint, OpenAPI lint, documentation links,
formatting and API mirrors passed. Full `make lint` completed with zero issues.
Live bucket and native workload acceptance remain pending.

The native acceptance harness milestone passed local race-enabled checks for
atomic S3 wire-fixture ownership, rejected unconditional/foreign-bucket writes,
private apid configuration updates, pure counter state transitions, invalid
payloads and integer overflow. The static Linux guest binary check passed. The
metal e2e test package compiled; no native test was executed. Native phase and
runner contract checks, documentation links, formatting and API mirrors passed.
Full `make lint` completed with zero issues. The additional metal/testdata lint
check retained 12 existing findings in unchanged HEAD files or unchanged source
context; none were reported in the durable entity additions after their context
handling was corrected. No KVM device, configured test bucket or API credential
was available here. Actual native park/restore, apid replacement and live-provider
acceptance remain pending, and the preview's release status is unchanged.
