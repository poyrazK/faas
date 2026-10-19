# ADR-1591: Managed Postgres data pipelines

- **Status:** implemented for staging qualification; production disabled
- **Date:** 2026-10-16
- **Builds on:** ADR-650 and the Data API release work in PR #4809

## Context

A generated Data API makes an existing Gregale database accessible to an app.
Customers also need a managed way to copy their existing data into that database
and keep the copy current. Provisioning credentials or an API alone does not
provide initial loading, change capture, replication recovery, or verification.

## Decision

Start with one supported topology: PostgreSQL 16 public tables to a dedicated
managed PostgreSQL database, exposed through a SELECT-only Data API. Sources
must have primary keys, ordinary supported builtin columns, logical replication,
publication ownership, and a finite source WAL retention setting of at most 1 GiB.
Partitioning, source RLS, generated columns, arbitrary connectors, and writeback
are outside this contract. A JWT subject allowlist explicitly grants reads of
all selected tables; this is not per-row organization authorization.

The apid HTTP boundary owns account-scoped intent and age-sealed source URLs.
A separate `apid --data-pipeline-worker` process owns remote SQL and uses native
publications and subscriptions for initial copying and continued row movement.
Customer rows never traverse the control API. The worker's internal provider
adapter verifies the destination resource identity and uses private credentials;
apps receive only the existing managed read-only credential mode.

Catalog revision and lease checks fence stale observations. A destination session
advisory lock serializes remote side effects, followed by a fresh intent check.
Private destination receipts make nontransactional subscription creation
recoverable. A durable retiring marker precedes teardown, so a lost response
cannot re-enable a broken generation. Lost source WAL, missing target receipts,
unsupported schema changes require explicit owner resync. Unchanged live-table
mismatches use the confirmed scoped repair described below. Resync replaces
only tables marked as owned by the preceding generation. Deletion removes native
replication resources and retains the mirrored data. Source cleanup must finish
before credentials, database, or account deletion can proceed.

RLS denies reads until every selected table completes its copy and replication
passes the initial barrier. Live status additionally requires an active source
slot within 16 MiB and a recent target receiver message. Observations expire after
90 seconds and must match the intent revision. Pausing stops replication while
retaining a readable snapshot; a blocked source does not imply fresh data.
Verification uses bounded primary-key-ordered digests and reports inconclusive
when the source changes, acknowledgement lags, or the size limit is exceeded.

Connection-budget reporting includes mirrored read-only bindings and marks native
replication demand unmodeled. It cannot establish a passing capacity guarantee.
All pipeline limits are declared in `pkg/api/limits.go`.

## Availability and qualification

### Connection onboarding (2026-10-17)

A bounded, durable check job per source connection runs in the same isolated
worker and reuses the replication source inspector. It returns compatibility
codes, remediation, and table/column metadata; it never returns connection URLs,
raw remote errors or customer rows. Optional target inspection uses the private
provider adapter. The HTTP process continues to own intent and encrypted custody
without dialing a customer database.

New pipeline reservations require fresh, passing evidence for the current source
revision and selected supported tables. Catalog row locks fence concurrent
credential rotation and replacement results. Job IDs and worker leases fence
late completions; expired claims recover after restart. A previously accepted
identical pipeline can still recover through idempotent creation after evidence
expires. The CLI checks sources before provisioning and then checks the mirror;
the dashboard adapts the same services through account-bound named CSRF forms.

Compatibility includes conservative source slot/sender demand and target native
subscription/worker limits, repeated before a new native copy. These are capacity
observations, not reservations against external consumers. Full application-pool
and provider capacity qualification remains unknown and production stays disabled.
A dedicated staging canary exercises live changes through signed Data API reads,
authorization, pause/resume, verification and cleanup without issuing release
approval.

### Measured capacity and durable admission (2026-10-17)

Private workers combine reviewed, expiring physical allocations with measured
PostgreSQL settings. An allocation never comes from compute class or
`max_connections` alone. Targets pin the database generation, backend fingerprint
and immutable dataset; source aliases sharing limits use one operator-reviewed
physical scope across databases, credentials and accounts.

A shared catalog ledger reserves both endpoint peaks in the pipeline intent
transaction. It accounts for control sessions, native initial copy/steady state,
source slots/senders and mirror workers. Reservations never expire with a worker
lease or capacity observation. Pause retains the peak; resync reuses it after
cleaning the previous generation. Only confirmed deletion under the current
intent revision/worker lease releases the allocation. Recovery accounts for
legacy native work before allowing any new allocation.

Runtime pool estimates cannot bound arbitrary clients or modified app versions.
Admitted bindings instead receive verified PostgreSQL login connection limits
before credentials cross the secret-delivery boundary. Reservations include two
credential generations and credential-control sessions. This bounds Data API and
Auth wake/rollout demand as well as generic pools. Providers must explicitly
implement bounded issuance; unsupported adapters cannot silently ignore ceilings.
Private cutover credentials remain unmodeled and are refused on admitted targets.

HTTP and dashboard reads use cached evidence and never dial customer SQL.
Compatibility and capacity are distinct. CLI creation refuses unknown, expired or
insufficient capacity, and API mutations recheck atomically. Status describes
existing reservations separately from the capacity needed for another pipeline.
The inherited budget API identifies enforced shared ceilings separately from
canonical advisory pool planning. External/provider/control-probe demand still
requires a reviewed bounded allowance for the actual fleet. No live provider or
production readiness is implied by local tests; production remains disabled.

### Staging gate

This is internal, staging-only functionality. Both HTTP admission and the worker
require an explicit account allowlist and exact source host/CIDR policy in TOML.
Production startup refuses enabled pipeline configuration. The shipped service
unit is installed without enabling it; an operator must configure the pilot.
No live-provider or deployed-fleet qualification is implied by local acceptance.

`make test-data-pipeline` exercises actual TLS logical replication and signed
Data API HTTP requests in disposable containers, including concurrent source
writes, restart after a lost response, pause/resume, schema drift, resync, lost
WAL state, verification, and cleanup. Production qualification still requires an
isolated provider canary, resource/connection measurements, outage/WAL-retention
exercises, and an operational release decision before changing these gates.

## Consequences

Customers can onboard a source and optional read API with one CLI command. Native
PostgreSQL supplies copy/change ordering without a new queue or row-streaming
service. The first release requires a privileged source replication user and a
destination provider capable of native subscriptions. Unsupported schema evolution
requires an explicit new copy and may temporarily remove the old mirror tables. Additional
connectors and row-specific authorization need separate designs and evidence.

Applied-progress followup: freshness is a lower bound from a periodically sampled
source WAL insertion checkpoint confirmed by the native destination replication
origin and local WAL flush, after all selected tables finish initial copy. Store
pending evidence in the generation-owned private mirror receipt and copy confirmed
timestamps into the fenced catalog observation. Clear catalog evidence on resync.
Compute customer health at read time, expiring monitoring independently of
replication. Idle sources may remain unconfirmed; do not infer time lag from WAL
bytes, heartbeats or an unadvanced origin. Recovery reuses existing guarded
controls and requires explicit confirmation for resync. This adds in-app guidance,
not external notification delivery or production/provider qualification.


Durable dashboard setup followup: apid records immutable setup intent and a
server-selected mirror definition with a preallocated database ID. The existing
private apid worker advances source check → provisioning → mirror check → pipeline
creation → fresh live observation. It uses managed database reconciliation with
an ID guard that rejects name-based adoption of another reservation. No provider
SQL or provisioning runs in the dashboard HTTP handler, and app builds remain in
the existing builder workflow. State/revision/lease fencing and stable IDs
recover lost acknowledgements without creating replacement resources. Shared
source discovery is serialized to one active setup per connection; unstarted
setups reserve account pipeline quota before provisioning. Stop preserves
requested resources, and active setup lifecycle guards prevent deleting its
account/source or deleting/resizing its mirror. The optional API stage pins an owned deployed Auth app and issuer, reserves a
stable app ID, and uses the existing SELECT-only binding service and connection
budget admission. Stable source-intent IDs recover ambiguous enqueue replies;
terminal failures require an explicit new attempt, linked to the previous
deployment in saved metadata and its annotation. Public JWT/schema/read-only
configuration is frozen in deployment overrides; credentials remain managed
secrets. apid submits the embedded Dockerfile template through apidsource and the
ordinary builder queue, never running builds in the workflow worker. Completion
requires the intended deployment to be live; API failure retains the live mirror.
Stop also retains API resources and does not cancel submitted builds. This remains
a staging-only source → mirror → optional read-only API workflow.


Schema evolution followup: retain the full source metadata baseline in the
private generation receipt while keeping legacy structural hashes stable.
Dashboard previews and explicit approvals bind exact before/after definitions,
table identity, pipeline generation and intent revision. Supported additions are
nullable builtin columns without defaults, missing values, identity or key
changes. Pending approval gates reads and disables the subscription while keeping
the existing bounded slot/publication. Unsupported changes and lost/excessive WAL
continue to require explicit resync and native cleanup.

Under a source SHARE lock, re-inspect the approved plan and atomically apply only
its added columns with an advanced target receipt. Recover ambiguous commits from
that receipt without recopying tables or creating a new generation. Resume native
replication and require equality verification to catch historical values that
DDL did not replicate. The resumable-verification followup below replaces the
original whole-table verification limit. Unproven data remains gated. Use the ordinary durable runtime-config-restart outbox, with
one ledger entry per plan/app/retry, snapshot invalidation and a freshness stamp
in the same catalog transaction. Existing schedd owns all runtime replacement;
no VM lifecycle writer or refresh protocol is added. Completion requires every
bound app's delivered refresh plus a final source definition/equality check under
a source write/DDL lock before RLS opens. Explicit retries advance durable attempt
IDs; limits are 100 bound apps and 10 retries per change. Production/provider
qualification remains unchanged.


Resumable verification followup: the private destination receipt stores a
schema/generation/attempt-bound cursor, typed range boundaries and counters.
Every bounded comparison drains source writes with a SHARE lock, waits for the
native subscriber acknowledgement and compares canonical digests under a target
write lock. Destination ALWAYS triggers invalidate affected ranges atomically
with native or direct row writes; updates to a key cover both positions and
truncate covers all ranges. No source journal, extra slot or separate row-streaming
service is introduced. Dirty ranges can split, with at most 4,096 retained ranges.
Per-batch budgets are 2,000 rows/4 MiB, with a 16 MiB maximum row and four batches
per tick. Acquisition, query and catch-up waits use explicit limits in api/limits.

The final all-table fence requires a current source definition, native
acknowledgement, a complete chain of ranges and no outstanding dirty work. At
most two residual ranges may be rechecked inside this fence. Opening schema
reads, recording a completed proof and removing tracking share one destination
transaction. Worker leases fence each progress commit; saved receipts recover
ambiguous responses. Missing tracking or incomplete coverage never yields
`passed`. Manual verification uses the same engine; dashboard cancellation is
account/revision/run-bound and keeps incomplete schema workflows gated. Stops,
limits, successful completion and generation retirement remove owned helpers.
Retries discard old proof without recopying the mirror. Local native tests cover
100,000 rows and concurrent write/restart/mismatch recovery. No VM lifecycle,
production admission or provider qualification gates change.

Bind resumable proofs to physical source storage identity as well as logical
schema/intent. Heap rewrites may change values without logical row events.
Compare opaque heap/TOAST/path fingerprints under each source lock and the final
fence; a rewrite starts a fresh physical run under the same approved intent.
Run IDs change, while cancellation remains bound to the unchanged logical
intent and continues to win over an automatic restart. Server-side idle
transaction timeouts use the existing 60-second pipeline lease to bound abandoned
write locks; worker operation and per-statement budgets remain shorter. Native
acceptance covers both value-preserving and value-changing rewrites after partial
verification.

Approved new-column backfill followup: extend supported additions to builtin
columns with defaults, stored missing values and NOT NULL, retaining exclusions
for identity, key changes, unsupported modifiers and changes to existing columns.
Create new mirror columns nullable without defaults. Within each existing bounded
comparison transaction, read canonical source values under the source lock and
native catch-up fence, assign only the exact approved new columns on owned mirror
tables, and compare the complete row. Data changes, range proof and counters
commit atomically behind the current lease. Missing rows or existing-column
mismatches still require explicit resync; this is not general drift repair.

Source expressions never execute on the destination. Typed JSON record input
uses the existing builtin mirror schema, including composite keys and binary,
JSON and timestamp values. Dirty ranges repeat this bounded operation before
the final proof. Cancellation keeps applied batches and gates reads; retries
and physical rewrites may revisit batches safely. A changed logical definition
during an unfinished backfill requires explicit recovery, so a later plan never
assumes partially backfilled columns are an established baseline.

Enforce required columns in a destination transaction with the durable
refreshing-phase receipt before any runtime refresh is requested. Existing lock,
statement and operation limits bound constraint scans as well as batches; a scan
that cannot complete within the statement budget retains progress and retries.
API refresh and the final equality/coverage fence remain prerequisites for opening
RLS. The dashboard exposes only backfill counters and phase, never source values
or cursor keys. No catalog migration, additional replication slot, VM lifecycle
change, hosting tier or production enablement is needed.

Additive table onboarding followup: store requested selection in the existing
schema-change intent, preview exact source definitions and require explicit
reader exposure approval. Approval expands catalog selection within the 16-table
limit without changing generation. Serialize against schema migrations and
manual verification, require fresh discovery for all selected tables, and reuse
the existing qualified peak allocation instead of admitting a second pipeline.
Before native refresh, check configured table-sync workers and actual additional
slot, sender and worker headroom. Native copying stays within the reserved peak.

Commit only new owned mirror tables and a private stage receipt, leaving existing
table policies open and new policies denied. Extend the owned publication and
recover an ambiguous source commit by validating its exact old or approved new
membership. Refresh the existing subscription with copy_data=true only when all
added subscription relations are absent. Recover an ambiguous refresh from its
existing relation entries; missing/mixed entries after copy admission block
onboarding rather than silently starting a new snapshot. Never recreate existing
subscription relations. Keep source defaults off the mirror as in initial setup.

Verify only new tables using the existing durable range engine. Persist the
verification's table scope privately so cleanup remains correct after catalog
selection expands or table ordering changes. Existing table reads remain enabled
through copy, verification and the normal runtime-refresh outbox. Completion opens
only new table policies in the same transaction as the final proof and completion
receipt. Lost catalog acknowledgement recovers that completed receipt and resumes
ordinary schema monitoring. Closing a full-pipeline gate handles selected tables
whose approved remote creation has not yet happened, without adopting foreign
objects. New-table-only failure blocks its onboarding; shared replication loss or
existing-table drift still uses whole-pipeline recovery. Explicit resync replaces
the full approved selection. No public API/SDK, catalog schema, VM lifecycle or
production admission changes are required by this dashboard-only slice.

Table recovery followup: a confirmed verification mismatch in an unfinished newly
added table can request an exact, separately approved recopy. Bind the preview to
pipeline/generation, original full source definition (including relation identity
and PK), owned destination OID, native publication/subscription OIDs and bounded
schema attempt. Require every other native relation to remain ready. Previously
available tables and shared replication/WAL failures retain full-resync semantics.

Persist a private recovery receipt before remote withdrawal. Remove only the
failed table from the owned publication, then use PostgreSQL's native REFRESH with
copy disabled to retire its subscription relation. Clear only that owned table
with transactional TRUNCATE ONLY/RESTRICT after retirement; save the cleared stage
in the same transaction. Republish and enable native copying for the absent
relation, recognizing exact membership and relation state after lost replies.
Never recreate the shared subscription, main slot or healthy tables. Keep the
shared readable receipt and healthy table policies unchanged. Reuse the reserved
copy peak, actual headroom checks and ten-attempt schema budget.

Fresh verification and the ordinary runtime refresh must finish before opening
new tables. Completion records private proof and read gates atomically; replay
recovers both schema completion and passed verification metadata. Retain private
recovery identity across worker restarts and distinguish later table-addition
approvals so a completed earlier recovery cannot block or be adopted by a later
one. Native local acceptance uses two 100k-row tables, source drift, lost remote
acknowledgements, rollback of an unfenced clear, worker restarts, signed read gates,
and independent healthy-table storage and CDC checks. No release or production
enablement is authorized.

Initial-copy recovery followup: extend the same exact approval to stalled or
untracked initial copies of newly added tables. Persist per-table native phases,
worker activity, available COPY counters and high-water progress clocks in a
generation/schema-bound private receipt. Publish safe counters through existing
dashboard schema metadata. Five minutes without observed advancement needs
attention; repeated worker retries do not imply successful progress. Unknown
counters are labeled unavailable. No automatic destructive retry follows a stall.

Approval selects a supported new table, binds recovery kind and native identities,
and retains ownership, revision, observation freshness, capacity and source-schema
checks. Previously available relations stay ready; other new relations may keep
copying, but must remain tracked so REFRESH cannot adopt an unrelated missing
relation without its approved snapshot. Native REFRESH retires the active worker
and temporary sync slot. Check worker/relation absence before clearing. Inspect
the already false reader policy instead of altering it behind an active COPY
lock, and remove verification helpers only after retirement. Preserve the shared
subscription/main slot, healthy policies/storage, and full peak reservation;
actual admission requires only the workers needed for the absent selected table.

An explicit fresh attempt resets only its table's progress clock. If copying
resumes or finishes before a durable recovery intent starts, withdraw the stale
request and continue normal proof/refresh without recopying. Final completion
also retires an earlier private recovery receipt when a later unstarted approval
was withdrawn. Native fault acceptance covers active COPY, slot retirement,
missing tracking via supported DDL, lost receipts, restarts, healthy-table CDC,
and copy completion racing approval. This remains a local, staging-only followup.


Approved-table cancellation followup: introduce a separate exact, confirmed
retirement intent for an unfinished addition. Cancellation allocates no new copy
capacity and does not consume the schema retry budget. Persist intent before
source withdrawal, let native REFRESH retire only its relation/worker/sync slot,
and archive committed rows privately after retirement. Never clear rows as a
side effect of cancellation. Preserve the main slot, subscription/publication
identities, baseline read gates/storage and other tracked copies.

Keep retained tables outside public API schemas, with false RLS and no app-reader
schema usage. Give retained tables and indexes unique names and retain original
identity in object comments. Bound retention at 32 tables per mirror database;
require owner-managed export/removal to free space rather than deleting retained
rows automatically. Archives survive later generation retirement and pipeline
deletion. Partial cancellation rebases only the approved subset, preserving copy
admission history and progress clocks. Fresh selection identities prevent re-adds
from adopting an earlier proof, API-refresh receipt or retained data.

Use a distinct cancellation identity for the existing scheduler API-refresh
outbox. Atomically reduce saved selection and transfer approval to the remaining
subset only after native cleanup, archive and runtime refresh. Recover lost final
catalog acknowledgements from the private completed result before ordinary schema
reconciliation. A privately completed addition wins a race with an unstarted
cancellation preview. Native local acceptance includes two simultaneous copies,
active worker/temporary slot retirement, lost acknowledgements and restarts,
private retained rows, signed healthy CDC, last-addition cancellation and repeated
cancel/re-add of a 100k-row source. No release or production enablement.

Live-table removal followup: reuse durable table retirement with an explicit live
kind, a fresh request identity and an exact preview bound to the saved full source
baseline plus native/owned object identities. Admit only a stable, freshly observed
live selection containing at least two tables. Require ordinary disconnect for the
last table. Preview and dismissal leave syncing and read gates unchanged.

On confirmation, atomically close only the selected owned table's read policy and
persist retirement intent under the current lease/revision fence. Native
publication withdrawal and subscription refresh retire only that relation; other
baseline relations must remain tracked and ready. Archive imported rows privately
without clearing them, using the existing retention bound and owner SQL lifecycle.
Require no new copy capacity, schema attempt or pipeline generation.

Use the distinct retirement identity for API refresh. Commit the remaining schema
baseline and private result before a fenced catalog completion reduces selection.
Replay a lost catalog acknowledgement from that result, preserving healthy table
storage, read policies and shared replication identities. Native local acceptance
uses two 100k-row tables with concurrent updates, lost native/final receipts,
worker restarts, signed scoped access checks and final-table rejection. This is
local staging implementation; no release or production enablement.

Live-table repair followup: replace generation-wide mismatch retirement for an
unchanged live source definition with a private quarantine receipt. Atomically
gate only the attributed table, bind the full original source baseline and
original mismatch run, and preserve the shared stream. Recover the receipt before
ordinary read/schema reconciliation. Retry a failed quarantine transaction from
the catalog/private mismatch even when its manual verification intent has ended.
Installing this quarantine identity does not authorize replacement.

Require a separate exact repair preview and explicit confirmation, bound to
pipeline/generation, owned table and native identities, current revision,
observation freshness and qualified copy capacity. Reuse native table retirement,
one transactional clear, republish/copy and fresh bounded verification. Introduce
an explicit existing-table copy scope rather than treating the table as newly
added or permitting a column backfill. Healthy baseline tracking remains required;
unknown shared state or source drift retains full-recovery semantics.

Seal scoped proof, private completion, native-recovery completion and the selected
read gate atomically after the normal API refresh. Recover a lost final catalog
acknowledgement using the exact passed scope and attempt, then resume ordinary
schema monitoring. Keep selected table names and pipeline generation unchanged;
do not report the partial proof as full-pipeline verification. Existing capacity,
retry and verification limits apply. Native local fault acceptance covers two
100k-row tables, concurrent writes, rollback/lost acknowledgements, blocked COPY,
restarts, unchanged healthy storage/shared identities and signed scoped reads.
No release or production enablement.


### Reader access updates on an existing pipeline

Keep reader intent independent of schema/copy/repair intent. The dashboard saves
an account-, pipeline-, generation-, revision-, selection-, and reader-set-bound
preview, requires exact confirmation, and displays approved/applied status.
The catalog keeps the last acknowledged applied list until the worker reports
an exact matching completion under its current revision and lease. There is one
outstanding request per pipeline; resync cannot discard an approved revocation.
New schema/table approvals wait for the acknowledged reader list.

Store the authoritative reader list and completed request identity in the private
mirror receipt. Under the existing pipeline session lock, apply a reader change
before source connection or capacity admission. In one fenced target transaction,
validate owned SELECT policies, change only open policies, and commit the list
and request receipt. Acknowledge that receipt before ordinary reconciliation
can retire a generation and delete it. That catalogue acknowledgement changes
only readers and releases the lease; it preserves sync-health observations and
schedules ordinary reconciliation immediately. False policies remain false; skip their DDL locks so a
long-running native COPY cannot block revocation on healthy tables. Recheck a
policy under its DDL lock before editing it. Retained private tables are excluded.
Every subsequent gate opening locks and reads this authority, so an old catalog
snapshot cannot restore a revoked subject after a lost reply. No row copy, native
resource replacement, runtime schema refresh, or additional connection capacity
is needed. Existing reader limits and whole-table access semantics remain.

Disposable PostgreSQL 16 and signed Data API acceptance cover revocation/grant
during blocked repair COPY, ongoing mixed-table CDC, lost acknowledgement/restart,
and the final repaired-table gate using the new subjects. Unit/catalog tests
cover stale/foreign approvals, fence rollback, closed-policy preservation, and
stale gate-opening requests. This remains local/staging code, without production
provider qualification or release.

### Independent pipeline health notifications

Add an opt-in, encrypted HTTPS destination per pipeline and a pipeline-owned SQLC
incident/outbox ledger. App webhook subscriptions have required app/release scope;
mirrors need no app, so reuse `webhookout` signing and the shared egress transport
without expanding app webhook scope. Endpoint replacements are revision-fenced
and cancel previous queued events. Never include rows, source credentials,
relation names, JWT subjects, or raw errors in notifications or delivery history.

A separate staging-only APID worker monitors catalog observations without source
connections or managed-database startup. Separate monitor/delivery loops keep
receiver/key failures from suppressing detection. Persistent two-minute candidate
state and a single open incident coalesce condition changes; a fresh live state
must persist 30 seconds for recovery. Intentional pause does not report recovery.
A quiet source's old checkpoint alone is insufficient evidence of stale rows.

Queue transitions and incident state commit atomically under a configuration row
lock. Delivery uses leased claims, destination revision checks, bounded durable
retries, stable event IDs and incident/recovery ordering. Delivery is at least
once; a lost HTTP acknowledgment requires receiver deduplication. Opt-out and
disconnect cancel pending events; an already in-flight request may complete.
Acceptance uses local fixtures; deployment and production enablement remain separate.

The transport adapter uses a small typed attempt/result contract outside the
catalog package. This avoids a dependency cycle between managed Postgres and the
shared outbound daemon stack, while retaining the existing guarded transport.
The first destination surface accepts DNS hostnames on HTTPS port 443.
