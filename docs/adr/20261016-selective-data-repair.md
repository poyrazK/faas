# ADR-20261016: Recorded field updates and selective data repair

- Status: accepted for internal operator qualification
- Date: 2026-10-16
- Decision: Capture explicitly enrolled PostgreSQL 16 updates in the application's business transaction. Prepare immutable inverse-field plans, rehearse exact candidates in an isolated database, record digest-bound approval, and atomically apply fresh identity/version guards with a durable repair receipt.
- Scope: One owner-scoped regular table and one original database transaction. The internal preview includes an application-bound executor and an explicitly scoped hosted management workflow with native Operations, approvals, HTTP, CLI, and browser review. Scheduled schedd/vmmd rehearsal is implemented behind explicit gates and remains a separate native qualification boundary.

## Transaction ownership

The existing managed/Customer Operation wrapper owns the source transaction and
its idempotency receipt. SDK capture helpers run inside its business callback.
Owner-installed triggers record selected actual before/after fields and advance
versions for all updates, including unrecorded writes. A deferred constraint
prevents unfinished capture from committing, even if an application swallows a
callback error. Repeated writes collapse to first-before/final-after evidence.
No history is synthesized for incidents before enrollment.

The journal stays in the application database. Only repair identity, plan digest,
counts, approval and progress enter the control-plane Operations ledger. New
control-plane queries use sqlc. The explicit owner-installed SQL follows the existing
Commit/changefeed pattern: static Go queries invoke SQL functions; dynamic table
and field identifiers inside those functions come from the qualified enrollment
registry and use PostgreSQL identifier quoting. This is the bounded exception to
control-plane sqlc-only business queries required by a generic customer-table
contract.

## Enrollment and evidence

Enrollment adds an insert-generated immutable UUID identity and a database-owned
bigint update version. Primary keys remain immutable. The supported subset
excludes RLS tables, inheritance/partitioning, custom triggers, rewrite rules,
custom expression functions, and cascading updates of selected fields. Ordinary
foreign-key and CHECK constraints remain authoritative. The owner must declare
that there are no unsupported external effects or undocumented dependencies.
Cross-table or external effects in application code are not inferred.

Source scopes and releases are reported by trusted application code using the
platform-authored operation request. Transaction-local settings are correlation
metadata, not authentication. Database owners and explicitly granted application
writers/repair executors remain trusted. No privileges are granted to PUBLIC.
Journal scope is not a replacement for database authorization.

The fingerprint covers columns, constraints, triggers and their implementations,
rules, RLS, and inheritance. A bounded share-update-exclusive relation lock fences
relevant DDL while allowing ordinary row writers. Operator DDL/function changes
require quiescing captures and requalification. There is no automatic migration
or silent fingerprint refresh for existing evidence.

## Source discovery

The application-database owner installs a metadata-only source query and journal
indexes through the explicit contract upgrade. It lists committed captures by
exact scope/release/table and an inclusive/exclusive creation-time range, using
bounded keyset pagination over creation time and UUID. Query counts and receipt
references never read business rows, captured JSON values or plan documents.
Purged identities remain visible with unavailable counts and retained receipts.

Executor authentication fixes scope. Hosted readers select exactly one installed
binding within their account/app; the configured scopes contain no credentials
or destinations. Hosted continuation also pins the binding identity, and only
settled retained source Operations can offer preparation. Cursors are navigation
hints, not credentials. All pages authenticate and all preparation repeats the
existing admission checks. The listing neither freezes candidates nor changes
the original plan, rehearsal, approval or apply contract.

## Plan and apply contract

Eligibility requires the original row incarnation, final recorded version, and
recorded values. Changed-back values still conflict. Deleted/recreated keys are
unavailable. Any subsequent edit conflicts, even to an unrelated field. Only
fields actually changed by the source operation are restored.

The plan digest covers scope, source release, enrollment, schema fingerprint,
every classification/inverse patch/expected version, and the configured SQL
check hash. Expiry is fixed at creation. Approval never refreshes versions or
shrinks its selected candidate set. If one approved candidate is stale, every
inverse write rolls back. Constraints and a configured single SELECT returning
one true boolean run before commit. The default check is `SELECT true`; it adds
no semantic proof beyond database constraints. Check SQL is trusted operator
configuration, not a caller-supplied request or a sandbox for hostile SQL.

Apply serializes on the durable plan, locks candidate rows in UUID order, and
records a new linked database operation, its journal, and the apply receipt in
one transaction. Versions advance. Repeated requests with the same plan digest
and repair operation ID return that receipt without rerunning checks or writes,
including after expiry. Another repair ID conflicts. A lost COMMIT response is
unknown until the application database receipt is inspected; platform status
must never infer rollback from a transport failure.

## Advisory validation

An owner-installed, read-only validation function compares only the immutable
candidate set with a consistent database snapshot. The executor runs it in a
read-only repeatable-read transaction, validates scope/digest and report bounds,
and compares the frozen policy identity with its current configuration. No
capture, inverse update, business-check SQL, rehearsal, approval, expiry refresh,
receipt write or control-plane state transition occurs. The result is returned
separately from the plan and is never accepted as evidence or write authority.

The report distinguishes row-version/value drift and missing original UUIDs,
and reports expiry, schema/enrollment changes, changed policy definitions, empty
plans, applied receipts and purged payload. Incompatible schema stops row reads.
Version checks precede value comparisons; value comparisons project only the
recorded changed fields. Bounded frozen key text identifies changed candidates
without returning current business values. Omitted keys retain their UUID.
Originally excluded rows are neither queried nor promoted.

Both executor and hosted read-authorized endpoints accept only the expected
digest, including on expired/applied plans. Hosted validation uses the exact
recorded binding and never invokes reconciliation or intent advancement. Older
executors may continue serving plans without implementing this additive
diagnostic interface. CLI returns JSON with a nonzero status for a nonmatch;
review screens keep the diagnostic separate from frozen observations. A match
is only a snapshot observation. Apply retains its original locks, fresh guards,
business checks and atomic receipt contract. No VM lifecycle or CP schema change
is needed; installation remains an explicit owner upgrade.

## Named checks and failed reports

The subsequent [replacement decision](2026101702-repair-replacement.md) adds an
explicit linked child plan after drift. It preserves the parent's frozen history,
narrows only its original candidates, atomically fences the parent and requires
fresh rehearsal and approval. Validation remains read-only and grants no authority.

The owner may configure a bounded version-1 named policy instead of the legacy
single SELECT. Its canonical digest binds exact query bytes, names and order;
the plan document additionally freezes names and individual query hashes. Raw
queries stay in operator configuration and authenticated rehearsal inputs.
Existing single-SQL hashes and wire fields remain unchanged.

An owner-installed evaluator uses the same scalar-boolean contract in local
Docker rehearsal, the fixed guest and live apply. Its fixed search path requires
qualified application references. Failed/NULL results and evaluation errors stop
the policy; later declarations are explicitly not_run. Each report is checked
against every frozen declaration, including order and individual hash. Reports
store no SQL, query values or database error strings. Owners remain responsible
for read-only assertions, dependency locks and external effects.

Failed rehearsal reports live separately from successful evidence and invalidate
earlier unapproved rehearsal success. Live failures roll back before a separate
best-effort diagnostic write. Successful live reports commit with the repair
receipt, so replay can return them without rerunning checks. Policy changes never
permit an old digest to acquire a new declaration or new approval authority.

The existing scheduled JSON evidence record can carry a bounded negative check
outcome after confirmed retirement. ValidOutcome permits durable publication of
that report; ValidEvidence still requires success. The source validates every
result before storing it separately from rehearsal approval evidence. Publication
completion projects a failed task back to prepared review, with no approval.
The same saved publication and receipt reconciliation handle response loss;
recovery neither fetches another snapshot nor starts another producer. No VM
resource lifecycle or control-plane SQL schema changes are introduced here.

## Rehearsal and release boundary

The internal local backend obtains a bounded consistent full `pg_dump`, restores
it into a digest-pinned PostgreSQL 16 container with no network, no host mounts,
no source credentials, a non-root user, and memory/CPU/PID/tmpfs bounds. It runs
the same inverse plan and configured check, then removes the sandbox before
recording success. Failed restore/check/cleanup produces no successful evidence.
Only an operator-configured trusted runner can report evidence; the customer HTTP API
accepts no caller-authored evidence. A separate coordinator-only executor route
records host evidence after matching the frozen plan and check digests. The standalone executor derives its actor
from server configuration. An explicitly configured managed coordinator may
forward the actor of an approval already committed in the control plane.

This Docker runner is for local operator qualification, as with Data API dev.
It does not add host-container workload execution to the production platform.
The hosted API can instead admit a durable rehearsal task through schedd/vmmd
as described below. Activation still requires native KVM qualification, scoped
executor credentials and operational ownership. Full project-environment cloning
is not assumed. The hosted API connects a native platform Operation to the
application-database receipt.

## Hosted Operations and recovery

Repair is a native `data_repair` execution in the existing Operations ledger.
It never creates an invocation, job or synthetic workflow. Its immutable source
is a retained, settled Customer Operation; scope and customer come from that
record. The app-database capture must match exactly. Raw executor-only imports
keep their standalone workflow until they have a managed source identity.

Native repairs have no executable customer definition. A migration adds the
native identity to the execution-family constraints. Existing HTTP/workflow/job
execution identity columns become trigger-derived ordinary columns to extend
their derivation without dropping dependent identity FKs, indexes or views.
CHECK constraints verify the derived values independently. Previous execution
writers continue to use their original columns. This migration takes table
locks and must be qualified against production ledger sizes before rollout.

Preparation reserves the supplied UUID in both databases: the same ID denotes
the plan, platform Operation, native execution and eventual repair receipt.
Repeated prepare returns the original selection and expiry. The operator-owned
binding generation and destination are pinned, so a new destination cannot
silently resume a previous repair. No database credentials enter apid.

The account API uses existing PostgreSQL scopes, MFA and ownership middleware.
Account approvals use the existing Operation approval store and event, with
the deciding key/account recorded by the server. Deployment permission alone
cannot approve repairs; PostgreSQL management permission alone cannot approve
customer workflows. Approval binds a fixed plan with a fixed deadline.

CAS transitions serialize rehearsal and apply claims. Progress, approval,
unknown outcome and success appear in the normal Operation history/timeline;
row values remain in the separate PostgreSQL-read-authorized plan endpoint.
Generic cancellation and recovery refuse native repairs, including claimed
success and `safe_to_retry` resolutions. Only the coordinator can project a
matching committed application receipt as success.

Reconciliation reads the receipt without applying writes or calling approval.
No receipt leaves an uncertain apply unresolved. An explicit apply may retry
the same immutable plan and fixed repair ID; the application transaction
serializes that request and replays an existing receipt, including after plan
expiry. Disconnected local rehearsals can recover recorded evidence or release their
claims after the configured execution bound. Scheduled rehearsals require the
task retirement protocol below; expiry alone never releases their capacity. An interrupted
apply claim stays unresolved until a receipt or an explicit same-ID retry.

## Retention and privacy

Capture is bounded by row and byte counts and fails the entire source transaction
on overflow. A full rehearsal snapshot is bounded separately. Before/after and observed data
is sensitive and opt-in. Scope-authorized API responses use no-store and expose
exact JSON text for browser rendering of large numeric keys and values. No data
or token is persisted in browser storage. Existing redacted bulk history is not
repurposed as an undo log. There is no automatic journal deletion. The subsequent
[retention decision](2026101701-repair-retention.md) adds explicitly reviewed
application-payload compaction, preserves source/repair identities and receipts,
and holds approved plans without apply receipts indefinitely.

Plan review now records a closed mechanical exclusion reason, observed version
and bounded affected-field observations. These are frozen in the original plan
digest and never become inverse write inputs. Observation payloads are limited
to 16 KiB per row and 256 KiB per plan, with explicit omission markers; later
business edits can exceed the source capture's size. Older plan documents keep
their existing digests and remain rehearseable/applicable after the explicit
owner upgrade. Browser filters affect presentation; the approved set is still
every candidate in that immutable plan. Diagnostic properties stay out of the
execution header so existing strict guest readers remain compatible. The
restored database still contains the complete original plan document and digest.

## Acceptance

`pkg/datarepair/repair_test.go` covers 500/40 classification, whole-plan rollback,
ABA and key recreation, unfinished capture, undeclared writes, scope isolation,
concurrent/idempotent apply, schema drift and explicit real Docker rehearsal.
`pkg/apid/datarepair/handler_test.go` covers the authenticated review journey and
rejection of caller-authored scope, approver and rehearsal evidence. CLI tests
cover exact-plan confirmation and safe transport. SDK integration tests use real
PostgreSQL and their existing transaction receipts.

`TestManagedDataRepairState` checks PostgreSQL/memory lifecycle parity,
approval replay, competing apply claims, native identity, history and timeline.
`TestManagedDataRepairHTTPJourney` exercises real control-plane/application
databases, scoped authenticated executor transport, account permissions,
missing receipts, a committed repair with a lost response, read-only
reconciliation, the 460/40 result and the hosted CLI. The strict gate uses
the actual isolated Docker rehearsal in that journey.

## Durable scheduled rehearsal

Apid alone admits rehearsal intent, atomically with the native Operation's
`rehearsing` transition. A new `data_repair_rehearsals` table holds metadata only:
exact scope/binding, plan and check digests, immutable placement/daemon identity,
runner artifact byte digests, snapshot hash and cleanup/publication status.
Database indexes cap active tasks at one per account and one physical task per
node. Schedd additionally charges the shared node ledger for 512 MiB plus VM
overhead, one vCPU and 1000 millicores; rehearsal consumes no serving replica
concurrency. Restart seeding runs regardless of the creation gate and fails
startup if these reservations cannot be reconstructed.

The worker obtains a bounded consistent dump from the scope-bound executor,
which keeps the source DSN and credentials. It persists the snapshot hash before
sending any payload to vmmd. A metadata-first gRPC stream transfers a bounded
JSON header and raw SQL in 64 KiB chunks. Dumps and check SQL are not persisted
in the control plane, host journals, logs or artifacts. The executor's internal
snapshot/evidence routes require its explicit coordinator mode and scoped token;
customer routes cannot accept snapshots, actor substitutions or evidence.

Vmmd accepts the original node and daemon incarnation, verified pinned kernel,
base and layer bytes, fixed resources and a fresh task identity. It cold-boots a
platform PG16 image with the existing two-drive and jailer/cgroup lifecycle,
without a Firecracker network interface, deployment image, app secrets, outbound
grant or serving route. Guest-init selects a dedicated marker before any app
supervisor or proxy starts. A private bounded tmpfs holds the clone. PostgreSQL
runs as UID/GID 999 with Unix sockets only. The guest restores the exact dump and
uses the same Engine.Apply transaction and strict boolean check as live apply.
Only that clone bypasses consent. Success still provides no semantic guarantee
beyond the declared checks and constraints.

Before native allocation, a private host journal records the complete task.
Its physical generation/lease is bound before any producer can start. Producer
admission and replacement refuse reuse of this identity by other VM modes.
Retirement durably revokes it, cancels and joins its original flight, and checks
native exit/resource evidence before publishing a retirement timestamp. A
retirement arriving before Run fences that delayed call. Startup validates these
records alongside the existing native journal. An old daemon incarnation cannot
create a VM after restart. No generic Destroy result substitutes for task
retirement.

Schedd records successful evidence only after this retirement proof. It first
persists a publication intent, then records evidence in the source database,
then atomically projects `awaiting_approval`. A lost source acknowledgment is
resolved by reading its matching task evidence. This recovery never reruns a VM.
A lost VM response, timeout or cancellation enters cleanup; fresh physical
retirement is required before failure/cancellation releases capacity. A new
rehearsal requires an explicit request and a new task UUID for the same frozen
plan. Claim acknowledgment uncertainty is resolved from primary state; unknown
reservations remain charged. API reconciliation cannot bypass this protocol.
Cancellation requests survive process death and remain available during cleanup.

Creation is disabled without all three choices: scheduled mode on an apid
binding, schedd's dispatch gate and matching operator-pinned vmmd runner metadata.
Cleanup runs with dispatch disabled and uses the original frame even if the
binding or runner changed. Terminal task history follows Operation retention;
active-task delete guards and retention filtering preserve unresolved cleanup.
Application capture journals follow the separate reviewed retention policy above.

### Acceptance limits

Portable PostgreSQL/race tests cover durable admission, concurrent claims,
capacity, cancellation, stream bounds, native journal fences, cleanup failure,
lost VM responses and publication recovery. The hosted 500/40 gate also runs the
actual guest PostgreSQL process in a networkless local container. This exercises
guest logic, not KVM isolation.

`make test-data-repair-native` requires the designated bare-metal Linux x86_64
acceptance host, exact release images and a disposable PG16 source. It exercises
native 500/40 rehearsal and apply, cancellation after boot, replay refusal,
artifact integrity and leak checks. Native execution and crash injection must be
qualified before enabling a customer preview.

Legacy native generations refuse retirement after daemon death when recovered
helper ownership is not attested. The
[repair producer retirement ADR](2026101710-repair-producer-retirement.md) adds
explicit daemon-bound enrollment for new Linux repair cold boots. It preserves
the refusal for unenrolled generations and unconfirmed helper cleanup.
Qualifying native execution and daemon/node failure recovery remains required
before production activation. No deployment or public capability promotion is
part of this change.

A retirement with no original host journal is accepted only in the same daemon
incarnation that could have received the task. Missing history after a daemon
change or physical-node replacement is unconfirmed and retains the task.

The [apply resolution decision](2026101703-repair-apply-resolution.md) adds
authoritative source closure before replacement of an unsuccessful apply.
