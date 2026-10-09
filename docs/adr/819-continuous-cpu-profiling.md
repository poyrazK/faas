# ADR-819: Continuous CPU profiling across guest lifetimes

- **Status:** accepted for internal implementation; production acceptance pending
- **Date:** 2026-10-07
- **Decision:** Collect sampled application CPU inside the guest, stamp identity
  in vmmd, parse and export in an unprivileged profiled daemon, and expose
  authenticated deployment queries through apid. Use a private, tenant-enabled
  Pyroscope backend with bounded pprof profiles.
- **Why:** Infrastructure CPU graphs cannot identify expensive functions.
  Host-only eBPF cannot reliably resolve application stacks across the guest
  kernel boundary. In-process runtime collectors preserve application symbols.

## Collection and ownership

Profiling is explicitly enabled in an app manifest and baked into the next
image. Managed Node 22/24 and Python 3.12/3.13 bases contain pinned collectors;
Go applications call `pkg/guestprofiling.Start` once. Custom container images
must include an instrumented runtime. Only CPU nanoseconds are accepted; wall,
heap and allocation samples are excluded. Python samples executing Python
threads holding the GIL; native extensions outside the GIL are not covered.

Collectors use the guest-local HTTP bridge at 127.0.0.1:9191. The bridge accepts
Node's pprof ingestion and Python's compressed protobuf PushRequest, discards
SDK selectors, and sends a bounded frame over dedicated vsock port 1040. It
never has backend credentials. This channel does not use the request/event
telemetry receiver or shared host directories.

vmmd bounds concurrent frames and derives account, app, deployment, scope,
runtime, plan, instance and process generation from its live lease and owned
state records. It does not decompress or parse pprof. The unprivileged profiled
service receives a JSON envelope inside a well-known protobuf BytesValue over
its local gRPC Unix socket. File permissions protect the socket and Linux
peer credentials allow only root vmmd to submit host-owned principals. Parsed sample labels and comments are discarded except for the host-validated route-label exception in ADR-820. Pyroscope
receives a host-owned `X-Scope-OrgID`, explicit `process_cpu` type and selectors
for app, deployment, scope and runtime. Stable raw sample IDs and a bounded retry
cache avoid retry duplication. Parsing and export failures affect diagnostics.
An SDK process discriminator separates otherwise identical fork-worker captures
inside a VM. The host hashes it with the instance and generation to derive an
opaque collector label, because backend queries deduplicate captures by series
and timestamp. Retries retain their sample ID; different collectors add CPU.
The discriminator cannot change tenant or deployment selectors.

apid checks authentication, read scope, MFA policy, app ownership and deployment
membership before constructing a backend selector. SDK and public API callers
cannot set the tenant header or arbitrary label expressions. Query windows are
bounded by plan retention. Profiles are never a billing input or a replacement
for metered VM CPU.

## Snapshots and forks

Profiling requests the existing before-checkpoint handshake, even without a
customer hook. Collectors stop and acknowledge a suspend command; guest-init
waits up to 500 ms. An incomplete diagnostic drain does not prevent parking.
The scheduler avoids warm snapshot reuse for opted-in profiling deployments.
The ordinary terminal init snapshot and cold-boot fallback remain available.

After the mandatory entropy and clock resume hook succeeds, guest-init creates
a new random collection epoch. Old SDK labels are rejected. Go discards its
old buffer; Node/Python stop their old agents and initialize fresh collectors.
The host additionally rejects capture windows predating its current observed
instance lifetime. Broker recovery starts a new lifetime boundary. No collector
or extra host process is retained for a parked application.

Python's fork hooks stop the native agent before fork and start fresh control
threads in parent and child, so native agent locks are not copied while active.
Native snapshot, fork and overhead acceptance must still qualify each runtime.

## Admission and comparison

All Go transport, parser, concurrency and plan limits live in
`pkg/api/limits.go`. Defaults: 10-second windows, configurable 1–60 seconds;
1 MiB per upload, 8 MiB expanded; four concurrent uploads/queries; 20,000
profile nodes; 256 frames per stack and 200,000 total frames. Queries render at
most 5,000 call-path nodes and 256 KiB of source symbols to bound JSON expansion
independently of the compressed profile size. Capture duration permits the
two-second transport budget as scheduling tolerance.
Hobby/Pro/Scale query history is 3/7/14 days and admission is 60/180/600 uploads
per account per minute. Free cannot enable profiling. These are initial internal
engineering limits, not changes to plan prices, RAM quotas or billing.

Comparisons rank function self CPU seconds per second of the selected capture
window. Unequal windows are normalized separately. Missing data is explicitly
empty, and missing samples or different runtimes prevent comparison. An explicit
CPU-per-request mode divides sampled CPU seconds by weighted request counts in
the same app, deployment, and profile window. It uses existing retained request
telemetry, whose rows have minute-bucket timestamps, so counts near a window
boundary can be approximate. It requires a configured minimum in both windows
and reports observed counts with the assessment. Missing telemetry is
inconclusive; counts can be incomplete and results remain sensitive to traffic
mix and background work. CPU-rate and CPU/request comparisons do not prove
deployment causality.

## CPU drill-down and collection evidence

The profile dashboard queries the existing app-scoped schedd CPU counter and
renders at most 120 clickable intervals. It labels this rollup as app-wide:
all deployments and scopes contribute. Links preserve a selected deployment,
runtime and overview window; apid revalidates ownership and retention before
querying CPU profiles or infrastructure history. Missing CPU measurements stay
missing rather than becoming zero.

Accepted pushes include a host-generated count profile containing collector,
capture interval and receipt time in the same tenant-scoped backend request.
This separate profile type cannot be supplied by a guest. Coverage merges
overlapping intervals and counts contributors without using merged CPU sample
counts as an estimate of collection quality. Retry IDs keep metadata idempotent.
Bounded best-effort failure records share upload concurrency and deadlines and
are limited to one per account per host per minute. They are a lower bound on
failed attempts; total loss and pre-ingestion failures remain unknown.
Coverage queries exceeding 5,000 records, missing metadata on older profiles,
and backend failures render coverage unavailable while preserving CPU results.

## Commit-pinned source navigation

After validating account/app/deployment ownership, apid enriches functions and
call-path frames using that deployment's recorded `github://` repository, full
commit SHA and frozen source root. Conflicting revisions and mutable references
produce an explicit unavailable source context. Links are constructed locally,
always use `https://github.com/.../blob/<sha>/...#L<line>`, and require the
viewer's GitHub permissions. No source fetch or credential transfer is added.

Managed `/app` and relative source paths map through the source root. Matching
Go module paths map directly to repository paths, and the preserved Python
implementation maps back to its original filename. Generated adapters, unsafe
paths and dependency directories remain unlinked. Unknown image/Dockerfile
layouts require recognized Go module paths. A separate 256 KiB link budget
bounds enrichment. Flamegraph frame identity retains filename and sampled line;
same-name functions cannot silently borrow another frame's source location.

Comparison rows retain separate source locations for each revision, choosing
the hottest mapped line deterministically when samples span multiple lines.
Recorded Git provenance is informational for uploads; the interface states
that local changes and generated files may differ from the recorded commit.
This does not establish source-map or archive-integrity verification.

## Differential call paths

apid compares the bounded profile views already authorized for each selection.
The differential tree matches frames by symbol and filename beneath the same
parent path; named frames aggregate sampled line moves, while unknown and
anonymous frames retain line identity. Recursive levels and different callers
remain distinct. Each side's inclusive CPU is divided by its selected window
duration. Width uses the sum of the observed rates so child layouts remain
additive when expensive paths shift between deployments.

The dashboard colors known increases red, decreases blue and unchanged rates
neutral. A path absent on either side is unobserved: its rate and delta are
omitted and the frame stays gray. No sampling absence becomes measured zero.
Function comparisons retain numeric fields for compatibility and add explicit
observation/delta flags; the dashboard and CLI use those flags. Empty profiles
and incompatible runtimes prevent comparison as before.

The union reuses the central 5,000-node and 256 KiB symbol limits, including
source-link strings. Exceeding those bounds returns no partial graph, reports
`flamegraph_reason`, and preserves the function comparison. Source locations
retain each revision's hottest mapped frame, and search/keyboard zoom preserve
the change colors. Both selections' coverage remains visible. Rates are not
normalized by capture coverage or requests, and changes do not establish
deployment causality. This adds no guest collectors or lifecycle transitions.

## Saved investigations

APID owns app/account-scoped `profile_investigations` metadata in Postgres,
using generated SQLC queries and an append-only timestamp migration. Save and
delete transactions lock the owned app row, enforce the central per-app quota,
and check an explicit revision to prevent lost team edits. The memory store
implements the same isolation, copy and concurrency contract. Persisted JSON
contains selections, findings, notes and an optional bounded complete caller
path, never profile samples, fetched source files or provider credentials.

Read operations use existing read scopes and app ownership; mutations require
existing deployment-write scopes. The dashboard adapter adds an account-bound
CSRF token. Sharing uses an authenticated dashboard URL with an opaque UUID,
not a public bearer grant. Metadata remains available after profile expiry,
backend outages and plan downgrades. Commentary can be edited for unchanged
selections; changing a selection rechecks entitlement, retention and owned
deployment membership. App/account purges cascade metadata.

Reopening retains the exact saved windows. Expired or unavailable selections
show explicit per-side eligibility and do not query expired samples or substitute
a recent time window. Eligible selections use the existing profile/source
pipeline; matching the complete frame path restores zoom and revision links.
A missing path stays saved with an explicit unavailable message. These changes
add no guest collection or VM lifecycle transitions. Store concurrency and
quota races, API scope/ownership/CSRF checks, and browser expiry/restoration
checks qualify this metadata increment; native rollout gates still apply to
collection.

## On-demand CPU regression assessments

Saved investigations support a revision-protected check with configurable
CPU/s or CPU-per-request thresholds. Both relative and selected-metric absolute
thresholds must be crossed by total CPU, comparable function self CPU or
complete caller-path inclusive CPU. CPU/request divides sampled CPU seconds by
weighted observed request counts for the same app, deployment and profile
window. Request rows use minute-bucket timestamps, so boundary counts can be
approximate. It requires the configured minimum count in each window; missing
telemetry is inconclusive. The default policy is CPU/s, 20%, 0.01 CPU/s, at least three
recorded profiles and 80% capture time on both sides. Insufficient/unknown
collection, recorded failures, absent samples and unavailable history produce
an inconclusive result. One-sided symbols and zero-baseline increases remain
unknown; if no signal qualifies, unknown entries prevent a no-regression result.
This is a bounded heuristic, not a statistical confidence test or an assertion
of deployment causality.

The nullable assessment column stores only the latest bounded summary (64 KiB
JSON, at most ten evidence entries and 32 KiB evidence), original selections,
coverage, observed request counts when selected, and policy. Source URLs and raw CPU profiles are not persisted. A
successful check advances the saved revision; app-lock serialization and
compare-and-swap prevent a concurrent edit/check from replacing another result.
Later metadata edits preserve the historical summary and mark it stale through
a revision mismatch. Expiry does not extend sample retention or erase historical
summaries. API writes use deploy-write/MFA; dashboard writes require account-bound
CSRF. Existing query slots, deadline, tenant/app/deployment/scope selectors and
ownership/retention checks apply. No cron, alert, guest or VM lifecycle changes
are introduced by this increment.

## Automatic deployment CPU checks

An opt-in app policy adds a durable APID worker using the existing regression
assessment and profile-query ownership, retention, concurrency and deadline
guards. It observes completed successful rollouts, selects the previous
successful deployment in the same scope before candidate creation, and queries
the policy's runtime in equal, fixed windows before creation and after rollout
warm-up. Collection and the VM lifecycle are unchanged. Missing predecessors or
insufficient/incompatible samples remain inconclusive.

Postgres policies and deployment-keyed receipts use SQLC and append-only
migrations. App locks serialize policy edits and result publication; policy
revision snapshots and lease tokens fence old workers. Discovery has a 24-hour
lookback, a 30-second cadence and ten-row batches. Two-minute leases recover
discovered jobs across restarts; inconclusive results retry original windows
at one-minute intervals up to five attempts. The fifth expired worker lease is
terminal inconclusive. Policy changes cancel pending jobs. Receipts expire after
30 days; listing is capped at 50. Old undiscovered rollouts are not backfilled.

Publication atomically saves a revision-2 investigation and its bounded
assessment under the existing 50-record quota. Quota exhaustion preserves the
receipt and original-window link without changing user records. A missing
predecessor or exhausted crashed worker has no invented comparison evidence.
Dashboard policies use account-bound CSRF; API changes use deploy-write/MFA.
Results appear in deployment details and app profiling history. This increment
sends no notifications and does not block or roll back releases. CPU rates are
heuristics affected by traffic and resources, not causal evidence. The capability
remains internal under the existing native acceptance
gate.

## Rollout and acceptance

The capability is internal and defaults off with `FAAS_PROFILING_ENABLED`.
Operational setup, history retention, UI/CLI examples and checks are documented
in [profiling.md](../profiling.md). A new backend is provisioned independently
and kept private. `profiled` is a compute-only best-effort daemon.

Unit/race checks cover parser expansion, CPU-only enforcement, retry admission,
owned selectors and restore epochs. Real pinned SDK captures and a tenant-enabled
Pyroscope qualify encoding, profile type IDs and deployment isolation. Promotion
requires native x86_64 KVM cold boot, snapshot/restore, repeated park cycles,
leakcheck and measured latency/CPU/RAM overhead at supported workloads. Container
or nested VM tests do not qualify that lifecycle acceptance.
