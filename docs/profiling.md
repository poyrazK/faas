# Continuous CPU profiling

Gregale's internal profiling feature connects sampled CPU consumption to
application functions and call paths. Open the app's production debugger and
select **CPU profiles and deployment comparison** to inspect a flamegraph,
search functions, zoom into a call path or compare deployment windows.

The profile page includes an app CPU graph when Prometheus is configured.
Select a deployment and runtime, then click a CPU interval to open its functions
and call paths. The link preserves that selection and the original chart window,
so it can be shared during an investigation. CPU rates in this graph combine all
deployments and scopes of the app; the profile itself uses the selected deployment.
Missing measurements remain gaps, while a measured zero remains clickable.

**Collection coverage** shows recorded profiles, contributing collectors, the
latest receipt in the selected capture window, captured time and gaps. Captured
time is the union of observed intervals, clipped to the selected window;
overlapping workers do not inflate it. A gap can mean idle or parked time,
uninstrumented code or upload loss. It is not a measurement of missing VM CPU.
Collectors are distinct collection lifetimes across the window, not a peak
replica count.
Recorded upload failures are a lower bound: failure evidence is limited to one
attempt per account per compute host per minute and uses the receipt window.
Failures before ingestion, busy ingestion slots and failed evidence exports
remain unknown. A failed attempt does not prove that a backend lost the profile.
Unavailable metadata is reported explicitly, including older CPU profiles with
no collection records and metadata queries exceeding supported bounds.
The API and SDKs expose this evidence in the optional `coverage` object.

This feature defaults off and requires an operator-configured private Pyroscope
backend. Production availability awaits native Firecracker lifecycle and overhead
acceptance; the capability catalog keeps it **internal**.

## Open the source of an expensive function

In the function table, choose **Open source**, or select a flamegraph frame and
open the source link below the zoom controls. Links open GitHub at the full
commit SHA recorded for that deployment and the sampled line. A deployment
comparison offers separate baseline and candidate links; when a function has
samples at several lines, each side links its hottest mapped line.

Repository, commit and build root come from the selected deployment, including
older deployments after an app changes repository or build root. Managed source
builds map `/app/` and relative symbols through that frozen build root. Go
symbols with a matching `github.com/owner/repository/` module prefix map directly
to repository paths. Python's preserved `.faas-handler.py` implementation maps
back to `handler.py` with unchanged line numbers. Generated function adapters,
dependency directories, unknown absolute paths, missing lines and malformed
provenance have no link. Arbitrary Dockerfile and image layouts only support a
recognized Go module prefix; other paths need future build source maps.

The deployment source panel reports unavailable provenance explicitly. Links
use the recorded commit; they do not verify uploaded files against Git, and
local changes, compiled assets or generated files can differ. Source maps and
byte-for-byte archive verification are not provided by this navigation feature.
Private repositories require the viewer's GitHub access. Gregale does not fetch
source or pass credentials to the browser.

API and SDK responses include optional `source` provenance, function and frame
`source` locations, and comparison `baseline_source` / `candidate_source`
locations. A location contains `url`, repository-relative `path` and sampled
`line`. CLI output includes these URLs. Source links share a 256 KiB output
budget per profile; remaining unmapped locations are omitted if it is exhausted.

## Enable collection

For managed Node and Python runtimes, add this to `gregale.yaml` and redeploy:

```yaml
profiling:
  enabled: true
  window_seconds: 10
```

The setting can also live under `lifecycle.profiling`; declare it once. TOML uses
`[profiling]` with the same keys. It applies to the new deployment. Changing it
invalidates cached snapshots. Disable it with `enabled: false` and redeploy.

Managed Node 22/24 bases preload a pinned `@pyroscope/nodejs` collector with CPU
sampling enabled. Managed Python 3.12/3.13 bases load pinned `pyroscope-io` through
`sitecustomize`. Customer Node options, Python import paths and an existing
application `sitecustomize` startup hook are preserved.
The collectors use a local bridge and receive no backend credentials. Their
failure does not prevent the application from starting.

Go applications opt in during startup:

```go
import "github.com/onebox-faas/faas/pkg/guestprofiling"

// appContext lasts until application shutdown.
guestprofiling.Start(appContext)
```

Call it once, and cancel the context at shutdown. Do not run another Go CPU
profiler concurrently. Custom container images must include the runtime bootstrap
and its pinned dependencies; enabling the manifest alone cannot instrument an
arbitrary binary. Go source locations require the binary's symbol information.

## Compare deployments

The dashboard's **Differential flamegraph** compares complete caller paths.
Red frames show increased inclusive CPU seconds per second; blue frames show
decreases. Each side is divided by its own selected window duration. Frame
width is the sum of the two observed rates, so both deployments' paths fit the
same additive layout even when their most expensive children differ.

Select a frame to inspect baseline, candidate and signed delta rates, its caller
path and source links for both commits. Search outlines matching frames while
preserving their change colors; click or press Enter/Space to zoom, then choose
**Reset comparison zoom** to return to the full tree. Named frames match by
function and file within their caller path, allowing sampled lines to move.
Unknown and anonymous frames retain line identity. Recursive calls and identical
function names beneath different callers remain separate paths.

Gray frames seen on only one side say **Not observed** and show an unknown
delta. Missing samples do not prove zero CPU or that a path was added or removed.
An entirely missing profile prevents comparison. The function table and CLI
also distinguish unobserved functions from measured rates. API function rows
include `baseline_observed`, `candidate_observed` and `delta_known`; numeric
fields with a false corresponding flag must be ignored. Differential tree
rates are optional: an omitted side or delta is unknown.

Collection coverage for both selections stays above the graph. Rates use the
selected window duration, not observed capture time. Gaps, workload mix, traffic,
replicas and CPU allocation can affect the result, so a red frame alone does
not establish a deployment regression. The union is bounded to 5,000 frames
including its root and 256 KiB of symbols and source-link strings. If it exceeds
those bounds, the graph reports unavailable with `flamegraph_reason` while the
function comparison remains available. Shorten the selections to retry.

Use explicit RFC3339 timestamps and a runtime such as `node22`, `python312`,
`go124`, or `custom` for an instrumented container with no managed runtime name.

```sh
gregale debug profiles my-app \
  --deployment-id CANDIDATE_UUID --runtime node22 \
  --start 2026-10-07T11:00:00Z --end 2026-10-07T11:10:00Z

gregale debug profiles my-app \
  --deployment-id CANDIDATE_UUID --runtime node22 \
  --start 2026-10-07T11:00:00Z --end 2026-10-07T11:10:00Z \
  --baseline-id BASELINE_UUID \
  --baseline-start 2026-10-07T10:00:00Z --baseline-end 2026-10-07T10:10:00Z
```

Add `--json` for the call-path tree and typed comparison response. The REST API
exposes `GET /v1/apps/{slug}/profiles` and
`POST /v1/apps/{slug}/profiles/compare`. The latter accepts `baseline` and
`candidate` objects with `deployment_id`, `runtime`, `start` and `end` fields.
Generated Node/Python clients and the Go SDK expose these operations.

Self CPU counts time sampled directly in a function; total CPU includes its
callees' subtree. Comparison rates are self CPU seconds divided by each selected
window's seconds. Use similar traffic, replica counts and CPU allocation when
investigating a regression. Named functions match by symbol and source file,
so moving their source lines between deployments does not create false deltas.
Source locations in the comparison refer to the candidate when available.
The profiles do not attribute CPU to individual
requests, and they do not replace the app's metered CPU graphs.

No samples means missing coverage, not zero usage. Short-lived processes, upload
failures and quota drops can leave gaps. Python covers executing Python threads
holding the GIL; work in native extensions outside the GIL is not represented.
Source paths and function names are profile data visible to authorized app
readers. Except for the host-validated Go route label described below, sample labels, comments, environment values and request payloads are
not exported by this pipeline.

## Saved investigations

After loading a baseline and candidate in **CPU profiles**, select a flamegraph
frame, enter a title, findings and notes, then choose **Save investigation**.
The saved record keeps both deployment IDs, runtimes and exact time windows,
plus the complete selected caller path. Saving uses the loaded comparison;
load profiles again after changing selections. Named comparison frames can
reopen across sampled line changes. Candidate and anonymous frames retain
line identity. If the path is no longer observed, its saved selection stays
visible and the page explains that it could not be restored.

Open an entry under **Saved investigations** to reload profiles and the source
links for both recorded revisions. Use **Authenticated link to this
investigation** to share it with someone authorized to access the same app.
Links contain only the saved UUID and app slug; viewers must authenticate and
pass the existing account/app ownership checks. They grant no access on their
own. GitHub source links still require the viewer's repository access.

Saved records contain metadata and commentary, not profile samples. The page
shows separate baseline and candidate eligibility. **Expired** windows keep
their original timestamps and notes; Gregale does not replace them with recent
captures or treat them as zero CPU. Metadata remains readable and commentary
editable after a plan downgrade or profiling backend outage. Changing windows
requires current profiling entitlement and retained, owned deployments. Missing
deployments and unavailable backend data have explicit messages. Saving does
not extend the plan's profile retention or pin backend data.

Each app can hold **50 investigations**. Titles allow **160 UTF-8 bytes**;
findings and notes allow **8192 bytes each**. A selected caller path contains up
to **257 frames including the root**, at most **16384 bytes** of symbol/file
text, with each symbol/file bounded to **4096 bytes**. Save request bodies allow
**65536 bytes**. Updates and deletions check the saved revision, so a concurrent
edit returns a conflict and requires reloading before trying again. Delete an
investigation to free a slot; app/account purges cascade saved metadata.

The authenticated REST operations are:

- `GET /v1/apps/{slug}/profiles/investigations`
- `POST /v1/apps/{slug}/profiles/investigations`
- `GET /v1/apps/{slug}/profiles/investigations/{id}`
- `PUT /v1/apps/{slug}/profiles/investigations/{id}`
- `DELETE /v1/apps/{slug}/profiles/investigations/{id}?expected_revision=N`

POST and PUT accept `expected_revision` and `investigation`. The latter contains
`title`, `findings`, `notes`, `baseline`, `candidate`, and optional
`selected_path: {view: "comparison" | "candidate", frames: [{name, file, line}]}`.
Use revision **0** for creation and the returned revision for replacement.
Reads accept the existing app-read scopes; writes require `deploy:write` or
`admin`. Dashboard mutations use the authenticated session and an account-bound
CSRF token. Audit records include identifiers and revision, never notes or
profile symbols. Go, Node and Python SDKs expose all five operations.

Responses contain `saved`, a relative authenticated `url`, `baseline_status`
and `candidate_status`. Window statuses are `retained`, `expired`,
`deployment_unavailable`, `plan_unavailable`, or `backend_unavailable`.
`retained` means the window is eligible for a query, not that samples exist.
The bounded list sorts by last update and UUID. Saving metadata does not query
the CPU backend; reopening uses the existing bounded profile query pipeline.

## Capture a running instance on demand

On-demand captures profile one running instance now, without a redeploy or
`profiling.enabled` (ADR-967). They are the tool for "why is this instance hot"
and "where is this memory going":

```sh
# 30 seconds of CPU and heap from the app's earliest running instance
gregale debug capture my-app --type cpu,heap --duration 30s

# a specific instance, keeping the merged pprof files
gregale debug capture my-app --instance INSTANCE_UUID --output ./profiles

# list captures, or show one again
gregale debug captures my-app
gregale debug captures my-app CAPTURE_UUID --output ./profiles
```

The command prints the hottest functions per kind. `--output` writes
`profile-<id>-<kind>.pb.gz`, which opens in `go tool pprof -http=:`,
speedscope or Grafana Pyroscope. Windows are 1–60 seconds (default 10 s).

Captures only run on a RUNNING instance; a parked app is not woken, because a
fresh process would not show the state you are investigating. Send a request
first. While a capture runs the instance counts as busy and is not parked for
idleness. One capture per app runs at a time, and an account can start 60 per
hour. Captures are kept for 7 days.

What a heap capture shows depends on the runtime:

- **Go** — the sampled live heap of the process (`runtime/pprof`).
- **Node** — objects allocated during the window that are still live at its
  end (V8 sampling heap profiler). `Buffer` contents live outside the V8 heap
  and appear as `(external)`.
- **Python** — allocations made during the window that are still live at its
  end (`tracemalloc`). Tracing slows allocation-heavy code while it runs.

Memory that is allocated during a capture and still held at its end is the
shape of a leak. Repeat a capture later and compare the top functions.

The REST API is `POST /v1/apps/{slug}/profiles/captures` with optional
`kinds`, `duration_seconds` and `instance_id`; poll
`GET /v1/apps/{slug}/profiles/captures/{id}` until `status` is `ready` or
`failed`, then read `GET …/captures/{id}/view?kind=cpu|heap` or download
`GET …/captures/{id}/pprof?kind=cpu|heap`. A ready capture with zero
`processes` means the instance has no collector: a custom image without an
instrumented runtime, or a Go app that does not call `guestprofiling.Start`.

## Continuous heap profiles

Add heap to continuous collection with:

```yaml
profiling:
  enabled: true
  kinds: [cpu, heap]
```

Each instrumented process reports its live heap once per window.
`gregale debug profiles my-app --type heap --deployment-id UUID --runtime node22
--start … --end …` shows the live heap near `--end`: the query is narrowed to
the last collection window so snapshots are not added together. Heap profiles
have no route attribution or deployment comparison.

## Operator setup

Run a private Pyroscope with tenant enforcement enabled. The integration is
verified against Pyroscope 1.14.1's protobuf push and merged-profile query APIs.
Do not expose its ingestion or query ports publicly. Keep storage, compaction,
authentication and TLS under the existing observability deployment policy.

Provision `/etc/faas/profiling.env` with mode `0640`, owner `root`, group `faas`
on apid and compute hosts:

```dotenv
FAAS_PROFILING_ENABLED=1
FAAS_PYROSCOPE_URL=https://profiles.internal.example
FAAS_PYROSCOPE_TOKEN=OPERATOR_BACKEND_TOKEN
```

Omit the token for a private backend that authenticates transport separately.
The same URL must provide `/ready`, `/push.v1.PusherService/Push` and
`/querier.v1.QuerierService/SelectMergeProfile`. Backend proxies must preserve
`X-Scope-OrgID` and forbid guest or customer access. The pipeline constructs all
selectors from host-owned state; customer backend selectors are discarded.

Collection metadata is a separate host-generated profile type,
`gregale_profile_coverage:events:count:events:count`, under the same tenant and
deployment selectors. Each accepted CPU push includes its metadata in the same
request; retry identities remain stable. Give it the same physical retention as
CPU data. Coverage queries are bounded to 5,000 records; use shorter windows
when metadata is unavailable. No new backend credentials or guest collectors
are required for the graph and coverage enhancements.

Install the generated `faas-profiled.service` on compute hosts, rebuild the
managed runtime bases, and release the updated guest-init and daemons together.
Bootstrap stages the profiled unit; explicitly enable it after configuration.
Restart apid and vmmd to load the opt-in flag. Confirm
`http://127.0.0.1:9160/readyz` and `/metrics` on each compute host. Collector
readiness checks backend health. `gregale_profile_uploads_total{result=...}`
records accepted, duplicate, invalid, limited and unavailable uploads. Add a
local scrape or an existing private metrics tunnel for this loopback endpoint.

On-demand captures need no Pyroscope backend. Set `FAAS_PROFILING_ON_DEMAND=1`
for apid (captures API) and imaged (stamps dormant collectors into new
deployments), apply the `profile_captures` migration, and release guest-init,
vmmd, schedd and the managed runtime bases together. Existing deployments gain
collectors on their next deploy. Continuous heap profiles additionally need
the Pyroscope backend above; they are stored as
`memory:inuse_space:bytes:space:bytes` under the same tenant selectors.

| Plan | API query history | Uploads / account / minute |
|---|---:|---:|
| Free | Disabled | 0 |
| Hobby | 3 days | 60 |
| Pro | 7 days | 180 |
| Scale | 14 days | 600 |

These are internal defaults in `pkg/api/limits.go`. Queries enforce the current
plan's history window. Physical deletion belongs to Pyroscope: configure a
maximum 14-day backend retention and per-tenant 3/7/14-day overrides where the
backend supports them. API query restrictions alone do not delete stored data.
Monitor backend storage and label cardinality before enabling more tenants.
Each VM lifetime and instrumented process has an opaque collector label so that
identical captures from separate workers add CPU instead of being deduplicated.
Retry uploads retain a stable sample ID.

Uploads are bounded to 1 MiB, expansion to 8 MiB, and concurrency to four at the
bridge, broker and parser. Export is synchronous with a two-second transport
budget and no unbounded host queue. Collector failure or a full budget drops
diagnostics. Parked apps retain no collector RAM. Capture windows from before
the current collection epoch or observed instance lifetime are rejected.
Rendered queries have at most 5,000 call-path nodes and a 256 KiB source-symbol
budget. Select a shorter window if a large merged profile exceeds these bounds.

To roll back, disable the operator flag on apid/vmmd/profiled and disable the
manifest setting before redeployment. Existing backend data follows its
retention policy. This rollback changes no billing or application code paths.

## Validation and promotion

The [native profiling CI gate](ops/profiling-native-ci.md) provisions disposable
revisions and requires complete deployment, restore and cleanup evidence. It
uploads JSON reports with investigation links and rejects incomplete runs. The
dedicated acceptance stack must run the candidate daemon and guest-image revision.

For real deployment traffic, use the [deployment profiling acceptance runner](../tests/profiling/deployment/README.md).
It supplies a Go workload and checks known CPU regressions, sparse traffic,
partial route labeling, independent gateway counts, route filtering and saved
investigation persistence. The included native restore adapter supplies park,
restore and stale-epoch evidence; without it the runner reports `incomplete`. It requires disposable,
predeployed fixtures and does not alter rollout policy.

Profile-enabled applications take a fresh terminal snapshot on park so their
current collector completes its checkpoint handshake (ADR-824). This increases
snapshot capture and storage work. Native acceptance stages a synthetic CPU-format
probe to verify old-epoch rejection and separately measures real post-restore CPU
and request counters.

Portable checks:

```sh
go test -race ./pkg/profiling ./pkg/profileproto ./pkg/guestprofiling ./guest/init
npm ci --prefix guest/profiling/node
python -m pip install --require-hashes --only-binary=:all: \
  -r guest/profiling/python/requirements.txt
python tests/profiling/sdk_smoke.py --python /path/to/python --output /tmp/profiles
GREGALE_PROFILE_SDK_CAPTURES=/tmp/profiles go test ./pkg/profiling \
  -run TestCPUProfileSDKCaptures
# Add --fork to the smoke command, then set GREGALE_PROFILE_SDK_FORK=1
# on the capture test to require CPU from both Python processes.
GREGALE_PROFILE_TEST_BACKEND=http://127.0.0.1:4040 go test ./pkg/profiling \
  -run TestCPUProfilePyroscopeIntegration
# With playwright-core on NODE_PATH and Chromium installed:
node tests/profiling/drilldown_browser.cjs
node tests/profiling/investigations_browser.cjs
```

The smoke fixture requires Linux x86_64 and the pinned native SDKs. The backend
integration uses temporary fixture tenants, tests function totals across two
deployments, checks tenant isolation, and verifies that worker CPU adds together
while retransmitted captures count once.
The browser fixture additionally checks differential colors, normalized rates,
search, keyboard zoom, both source revisions, unobserved paths and missing
profiles under the dashboard's Content Security Policy.

Before promotion, run `make test-metal` and `make leakcheck` on the dedicated
native x86_64 KVM acceptance host. Exercise cold boot, snapshot and repeated
restore/park cycles for Node, Python and Go; verify new epochs, accurate
post-restore timestamps, no parked residency or leaked resources, and unchanged
cold-boot fallback. Include Python fork workers and abrupt exits. Record profile
loss and collector CPU/RAM overhead under idle and loaded traffic alongside
wake latency. Portable SDK/backend tests do not establish those results.

## Regression checks

Open a saved investigation and choose **Check for regression**. By default, the
check uses its saved baseline and candidate, independently normalizing sampled
CPU by each window's wall-clock duration. It checks total CPU, function self CPU, and
inclusive CPU for complete caller paths. A CPU increase must meet **both**
thresholds: defaults are **20%** and **0.01 CPU seconds per second**. The form
also lets you require more recorded profiles or capture time; defaults are
**three profiles** and **80% recorded capture coverage per window**.

Select **CPU per request** to divide sampled CPU seconds by weighted request
counts for the same deployment and profile window. Gregale uses its existing
request telemetry, whose rows are timestamped in minute buckets. Counts near
window boundaries can therefore be approximate, and telemetry gaps can omit
requests. The result is an observed average affected by traffic mix and
background work. Both windows must meet the configured minimum request count;
missing or sparse counts produce an inconclusive result. The selected metric
thresholds apply to total CPU, comparable function self CPU and complete
caller-path CPU. Keep CPU per second for background workloads or apps without
enough request telemetry.

The result is **regressed**, **no_regression_detected**, or **inconclusive**.
Missing samples, unavailable or insufficient coverage, recorded upload failures,
expired windows, plan restrictions, and backend outages are inconclusive. A path
observed on only one side is unknown, never a measured zero. A zero baseline
cannot establish a relative increase. If no comparable signal crosses both
thresholds but any function or path remains unknown, the result is inconclusive.
A detected increase remains a signal even if other paths are unknown.

The assessment stores its timestamp, options, exact windows, coverage summary,
total CPU rates, and up to ten largest qualifying function/call-path increases
within a 32 KiB evidence budget. Inclusive caller paths overlap; do not add their
rates together. **Open this saved differential flamegraph** returns to the
comparison for deeper investigation and source links. CPU samples and source
URLs are not copied into storage or pinned past backend retention. Summaries
can remain visible after profile expiry and are labeled historical when history
is unavailable.

The check increments the investigation's revision. A concurrent edit or check
returns a conflict. Later edits preserve the previous assessment and label it
**stale** until you check the current revision. Only the latest assessment is
stored. This action runs on demand; automatic deployment checks are described
below. These threshold observations do not establish deployment causality or
statistical confidence: traffic, replicas, CPU allocation, sampling gaps and
uninstrumented processes can affect CPU rates. Capture coverage is the union of
recorded intervals, not the proportion of CPU instrumented. Recorded failures
are a lower bound; zero recorded failures does not prove zero loss.

The REST operation requires existing deploy-write access and MFA:

```http
POST /v1/apps/{slug}/profiles/investigations/{id}/check
Content-Type: application/json

{"expected_revision": 1, "options": {"relative_increase_percent": 20, "absolute_increase_cpu_per_second": 0.01, "minimum_profiles": 3, "minimum_coverage_ratio": 0.8}}
```

Omit `options` to use defaults. Explicit options must contain the four existing
threshold fields. Set `metric` to `cpu_per_request` and also include
`absolute_increase_cpu_seconds_per_request` and `minimum_requests` to select
traffic normalization.
Percentage accepts 0–10000, absolute CPU/s must be positive and at most 1000000,
CPU seconds/request must be positive and at most 3600, minimum requests accepts
1–100000000 (default 20), minimum profiles accepts 1–5000, and capture ratio accepts 0.1–1. The response is
the saved investigation with an optional `assessment`; compare its
`investigation_revision` to the saved `revision` to determine staleness. Go,
Node and Python SDKs expose the check and assessment models.

## Automatic checks after deployments

On an app's **Profiles** page, open **Automatic deployment checks**, enable
checks and save its runtime, capture window, warm-up and regression thresholds. Collection
must already be enabled on the deployments. Automatic checks are disabled by
default and require a profiling-enabled plan and a configured backend. Defaults
are a **300-second window**, **120-second warm-up**, and the thresholds above.
Window length accepts 60–1800 seconds; warm-up accepts 0–3600 seconds.

APID discovers successful, completed rollouts after the policy was saved. It
selects the latest previous successful deployment in the **same environment**
that completed before the candidate was created. Both queries use the policy's
runtime. The baseline window ends at candidate creation; the candidate window
starts after rollout completion plus warm-up. The windows have equal duration
and stay fixed through retries. Missing predecessors, mismatched runtime samples,
parked apps, insufficient capture and unavailable history produce an explicit
inconclusive result.

Discovery runs every 30 seconds and looks back 24 hours; deployments completed
before policy activation are not backfilled. A discovered check waits until its
capture window ends plus 30 seconds for ingestion. Inconclusive comparisons
retry at one-minute intervals, for at most five attempts. Persistent leases
recover interrupted work after two minutes and prevent duplicate publication
across APID instances. Policy edits or disabling cancel pending checks; completed
results keep their original settings. An undiscovered rollout older than the
24-hour discovery window is skipped after a long outage.

Completed comparisons save an investigation with coverage, observed request
counts when selected, threshold evidence and a qualifying caller path when
available. CPU-per-request policies also require request-telemetry access for
the plan and use minute-bucketed counts, so requests near a window boundary can
be approximate. The deployment detail page displays
the result and a link to inspect functions; the Profiles page lists recent checks.
The existing **50 investigations per app** limit still applies. If that limit is
full, the deployment result remains visible with an explanation and a link to its
exact comparison windows. Existing investigations are preserved. Checks without
a predecessor and workers that exhaust their attempts without finishing do not
create an investigation. Receipts expire after 30 days, and the list returns the
latest 50. Saved investigations retain their existing metadata lifetime; sample
retention is unchanged. Deleting an investigation leaves its receipt and exact
comparison link.

These checks record CPU regression signals. They do not send notifications,
block rollouts or roll back deployments. Traffic mix, replica counts, CPU
allocation and instrumentation coverage can affect the result.

When this policy is enabled, `gregale routes health report`, the canary's
route-health API report and deployment detail page also show a **default advisory
canary profile signal**. A background worker pins the active canary stage and
policy revision, then compares it with the unique live stable deployment over
the same fixed window after the stage's configured warm-up and a 30-second
ingestion allowance. Route-health and dashboard reads return the latest saved
result for the deployment, including its stage and policy revision; they do not
query the profiling backend. Each stage and policy revision has one deduplicated
result, retained for 30 days. Pending checks show their attempt count and next
scheduled time; inconclusive checks retry for up to five attempts when
profile ingestion or request telemetry may still arrive. Missing stable
deployments are recorded as immediately inconclusive. Policy changes cancel
pending work; retained history keeps each result's policy revision and status.
The signal never holds
canary advancement or triggers rollback. CPU-per-second comparisons reflect
different traffic volumes; CPU-per-request is available when the plan exposes
request telemetry, with minute-bucket counts that can be approximate near
window boundaries.

Policy reads and receipt reads require app-read access; policy writes require
deploy-write access and MFA. Dashboard policy changes use account-bound CSRF.
Read the policy first, then save with its `revision` as `expected_revision`
(`0` for the initial policy):

Read a deployment's retained canary-stage timeline with app-read access and
completed MFA:

```http
GET /v1/apps/{slug}/profiles/canary-checks/{deployment_id}?limit=5
```

Use `next_cursor` as the `before` query parameter to page through older stages.
The deployment detail page and `gregale routes health profile-history APP`
show the same history. Each entry includes its exact stable/canary windows,
threshold evidence and policy revision.

```http
PUT /v1/apps/{slug}/profiles/deployment-policy
Content-Type: application/json

{"expected_revision": 0, "config": {"enabled": true, "runtime": "node24", "window_seconds": 300, "warmup_seconds": 120, "options": {"relative_increase_percent": 20, "absolute_increase_cpu_per_second": 0.01, "minimum_profiles": 3, "minimum_coverage_ratio": 0.8}}}
```

Every capture setting and threshold is required. Stale revisions return `409`.
Use `GET /v1/apps/{slug}/profiles/deployment-policy` to read settings,
`GET /v1/apps/{slug}/profiles/deployment-checks` to list receipts, and
`GET /v1/apps/{slug}/profiles/deployment-checks/{deployment_id}` for one result.
Each receipt contains the pinned configuration, exact queries, status, attempts,
next/completion time and optional investigation/comparison link. Metadata reads
and disabling remain available when profiling is unavailable. Go, Node and
Python SDKs expose all four operations and their models.

### Source attribution for canary findings

Choose **Investigate this function or call path** in the canary signal or its
stage history. The comparison opens the recorded windows and displays the
finding's CPU increase and share of the net total sampled CPU increase.
Function findings use self CPU; call paths use inclusive CPU. Inclusive paths
overlap, and shares may exceed 100% when other functions consume less CPU.
Do not sum these shares.

Source attribution compares the selected function's file, or the leaf frame's
file for a call path, at the recorded stable and canary commits. Matching Git
blob IDs show **Source unchanged**; different IDs show **Source file changed**.
This is file-level evidence, not proof that the function changed or caused the
regression. The diff link uses an endpoint comparison between the two commits.
Attribution details are prefilled in investigation notes and can be edited
before saving.

Both deployments must have validated GitHub provenance and the same mapped
repository file. Different repositories, unmapped or renamed paths, inaccessible
files, unavailable GitHub installations, and timed-out lookups show
**Attribution unavailable**. Repository requests use the account's GitHub App
installation with a four-second lookup budget and a bounded response. Source
contents and installation tokens are not persisted. Attribution is evaluated
when opening a finding; stage history continues to read recorded assessments.

### Traffic-aware canary analysis

The latest canary signal, stage history and finding investigations show recorded
request counts and rates alongside sampled CPU/s, CPU/request and profile
coverage. When both windows satisfy the recorded minimum request counts and
coverage requirements, Gregale compares CPU/s and CPU/request against their
respective absolute thresholds and the shared relative threshold. A zero CPU
baseline makes threshold agreement unavailable. Missing telemetry, low traffic
or insufficient coverage suppress the normalized comparison and ranking.

The table ranks retained threshold findings by their signed CPU/request
increase. It is not a ranking of every observed function. Function values use
self CPU, while caller paths use inclusive CPU and overlap. Average CPU/request
includes background CPU and is sensitive to request mix; it is not a measurement
of the CPU consumed by an individual request. Minute-bucket request telemetry
can make boundary counts approximate.

New CPU/s assessments collect request counts when the plan and installation
support them, with a two-second optional lookup budget. Lookup failures do not
change the CPU/s result. Existing CPU/request assessments continue to require
request telemetry. Older assessments without recorded counts show unavailable
traffic context; dashboard reads do not backfill history from current traffic.
The traffic comparison does not alter the saved stage result or canary rollout.

### Request-mix context

Open a canary stage's full comparison or investigate a finding to compare
stable/canary request shares by declared route label and HTTP method, and by
response-status class (1xx–5xx). Route links open the debugger with the exact
recorded deployment, route and time window; pagination keeps that window.

Mix difference is total variation distance, expressed in percentage points:
half the sum of absolute differences between traffic shares. Zero means the
observed distributions match; 100 means they do not overlap. A difference of
at least 20 points produces a warning when both windows have positive request
counts and meet the recorded minimum requests. Route and status distributions
are assessed separately; matching marginals do not imply matching joint
route/status distributions. This context does not change a stage's result.

Completed canary assessments now capture a bounded request-mix snapshot using
weighted request counts. The snapshot freezes its capture time, exact windows,
route/status counts, mix distances and warnings. Dashboard history and finding
pages use the snapshot without reading live request telemetry. Capture failures
are recorded as partial or unavailable and never change the CPU check result.
Completeness describes aggregation of observed rows, not telemetry delivery.
Request-mix capture has a four-second budget.

Older assessments without snapshots continue to read currently retained
telemetry for their fixed windows and are explicitly labeled as live views.
Changed totals are flagged; late arrivals or retention can explain differences.
Those legacy views are unavailable outside telemetry retention or on unsupported
plans/installations or lookup failure.

Snapshots include up to 20 routes per window, with further trimming to a
16 KiB JSON budget. Legacy live views show up to 50 routes per window. Shares
use the full observed request totals. If either list is truncated, a missing route on that side is unknown
and aggregate route-mix distance is unavailable. Status-class comparisons
remain complete. Low traffic suppresses substantial-mix warnings. Minute-bucket
boundaries and missing telemetry can affect the observed distributions.

Legacy captures and unsupported collectors have no route attribution. Go captures
can now provide route-associated CPU and a guarded common-mix comparison, as
described below. Findings can save the mix
summary and warnings through the prefilled investigation notes. Saving from a
canary comparison also atomically copies the server-resolved completed CPU
assessment and its snapshot into the investigation. Clients cannot supply the
snapshot contents. The copied assessment remains readable after stage history
or raw request telemetry expires; its lifetime follows the saved investigation.
Commentary edits preserve it. An explicit new regression check replaces the
latest assessment and captures a new request-mix snapshot.

The REST canary signal/history and saved-assessment responses expose optional
`request_mix` metadata; Go, Node and Python SDKs include the snapshot models.
Stage assessment metadata still follows the existing 30-day history retention;
snapshots do not extend retention for raw requests or CPU profile samples.


## Route-associated CPU (Go)

Go applications can attach a static declared route label to handler execution:

```go
import "github.com/onebox-faas/faas/pkg/guestprofiling"

mux.Handle("GET /orders/{id}", guestprofiling.RouteHandler("GET /orders/{id}", handler))
```

Enable and start the existing Go profiling collector as described above. Configure
that same method and path in the application's explicit declared route contract.
Only labels admitted by vmmd's current host route policy at upload time are kept;
environment policy takes precedence. Policy read failures disable attribution.
OpenAPI inferred routes alone do not authorize labels in this initial increment.
Use patterns such as `GET /orders/{id}`, never concrete URLs or request values.
Labels are limited to 256 bytes and 50 sorted unique declared method/path pairs.
Invalid labels, unknown routes and captures at encoding limits retain their CPU
as `[unattributed]`. Historical captures without labels remain unattributed.

### Automatic ServeMux attribution

For Go 1.22+ ServeMux routing, install one wrapper around the completed mux:

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /orders/{id}", orderHandler)
server := &http.Server{Addr: ":8080", Handler: guestprofiling.ServeMux(mux)}
```

The wrapper looks up the registered pattern and dispatches through the mux,
preserving `Request.Pattern`, `PathValue`, redirects and response writer
capabilities. It never uses a concrete request URL as the route label. Host
prefixes in patterns are omitted; the actual request method is used, so a HEAD
request matching a GET pattern needs a declared `HEAD /orders/{id}` contract.
Methodless registrations also use the actual method. Unmatched requests and
method mismatches clear attribution. Redirect CPU can be associated with the
registered target pattern. Register routes before serving; concurrent changes
can make the lookup and dispatch disagree. Pattern spelling must match the host
contract exactly, including wildcards and trailing slashes.

The wrapper disables attribution when `GODEBUG=httpmuxgo121=1` is set. Use the
default modern routing configuration and do not change this setting at runtime.
Other routers can continue to use `RouteHandler` or `WithRoute`. Go profiling
still needs to be enabled and the collector started; the middleware only adds
labels. Host validation, unmatched-label handling and background-goroutine
cautions remain the same.

`guestprofiling.WithRoute(ctx, label, work)` provides equivalent scoped execution.
Go child goroutines inherit pprof labels. Wrap asynchronous background work in
`guestprofiling.WithoutRoute(ctx, work)` to clear inherited attribution. Route CPU
is sampled CPU associated with labeled execution; it does not measure the exact
CPU of an individual request. Node and Python route attribution are not yet
supported; their existing collectors continue to provide aggregate CPU.

The dashboard's **CPU by route** table links to route-scoped functions and
flamegraphs, including differential views. API queries accept `route`, and CLI
queries accept `--route 'GET /orders/{id}'` or `--route '[unattributed]'`.
Saved investigations preserve the route selection. Baseline and candidate must
select the same route. Collection coverage describes the entire capture and
cannot establish whether application route instrumentation is complete.

Where retained request telemetry is available, route rows show observed request
counts and sampled CPU/request for the same deployment and time window. Missing
or truncated telemetry can leave these values unknown. The unattributed bucket
includes background CPU, unlabeled requests, unsupported collectors and bounded
encoding fallbacks; it has no CPU/request value.

All-route comparisons calculate CPU/request under common route weights only
when both captures meet regression collection coverage thresholds, have matching
route sets with no unattributed CPU, have complete observed request-route
coverage, and have at least the default minimum request count (20) per route in
each window. Weights are the average of the two windows' request shares. The API
returns `route_adjustment` with an availability reason and, when available,
weights and baseline/candidate/delta CPU/request. This is supplementary context;
it does not change rollout outcomes or prove deployment causality. Matching
observed routes cannot prove every request was instrumented.

The backend stores host-validated route attribution as a reserved synthetic root
frame so attribution survives merged pprof queries. Public views strip that frame.
Guest-supplied reserved markers are removed before storage; arbitrary sample
labels and tenant selectors are never forwarded. See ADR-820.

## Advisory route regression checks

Add up to ten unique static route labels to `options.routes` in an automatic
profile policy or a saved investigation regression check. The dashboard provides
one route label per line, for example:

```json
"routes": ["GET /orders/{id}", "POST /checkout"]
```

Routes use the policy's `relative_increase_percent`,
`absolute_increase_cpu_seconds_per_request`, `minimum_requests`,
`minimum_profiles` and `minimum_coverage_ratio`. The route metric is always
sampled CPU/request, even if the aggregate metric is CPU/s. Default CPU/request
threshold and traffic minimum apply when omitted from a CPU/s policy. Both
relative and absolute increase thresholds must be met.

Each selected route returns `regressed`, `no_regression_detected` or
`insufficient_data` with a reason, available observed request counts and a metric
when comparable. Missing samples are unknown, never zero. Missing labels or
telemetry, insufficient route traffic, incompatible captures and inadequate
capture coverage prevent a conclusion. Optional route-count reads share a short
deadline, are plan gated, and obey request telemetry retention. Critical routes
outside the top-route request-mix summary can still obtain scoped request counts.

API assessments and canary signals expose `route_checks`. Canary stages freeze
selected routes with their policy revision and retain results in stage history.
Saving the completed canary comparison copies those results into the saved
investigation; later profile or telemetry expiry does not erase its summary.
Dashboard route links load the frozen stable/canary windows and route-specific
differential flamegraph when raw profiles remain available.

These are advisory checks. They do not change the aggregate result, schedule
extra retries, block advancement or trigger rollback. No detected regression is
not proof of complete instrumentation or absence of a performance problem;
capture coverage describes collection, not labeled execution of every request.
Background goroutines retaining request labels and minute-bucket telemetry
boundaries can affect averages. See ADR-821.

## Route attribution quality

Profile responses now include `attribution`: labeled and unattributed sampled CPU
seconds and percentages for the **whole deployment window**, even when the
functions/flamegraph selection filters one route. No sampled CPU makes these
percentages unavailable. This is not VM CPU coverage or proof that every request
was labeled.

Comparisons expose both quality summaries, the labeled-share change in percentage
points, and warnings. A lower candidate share is highlighted. An absolute change
of at least 20 percentage points in either direction marks `substantial_change`
and makes advisory route checks `insufficient_data`. Missing quality also prevents
a route conclusion. Aggregate CPU results, retry scheduling and rollout decisions
remain unchanged. Background CPU and request mix can change these shares too;
equal shares do not guarantee equivalent instrumentation within each route.

Host-generated diagnostics report CPU seconds for these fixed reasons:

| Reason | Meaning |
| --- | --- |
| `unlabeled` | No nonempty route label, including background work, unlabeled requests and unsupported collectors. |
| `invalid_label` | Invalid or ambiguous route label. |
| `route_not_admitted` | Valid-looking label outside the host's admitted contract; includes undeclared routes, the allowlist limit and policy-read failures. |
| `encoding_limit` | A permitted label could not be stored because of stack, node or identifier limits. |
| `unknown` | Legacy captures or missing/inconsistent diagnostics. |

Rejected label values are never retained. New host coverage metadata carries only
fixed CPU counters. Older captures have unknown reasons. `diagnostics_complete`
is true only when counters reconcile with the merged CPU totals; mismatches do
not replace the CPU-derived shares or invent a background-work diagnosis. Tiny
floating-point differences are tolerated. These are sampled CPU totals, not
counts of discarded labels.

The dashboard shows quality and warnings next to route profiles, canary results,
stage history and saved assessments. CLI profile output includes percentages and
reason totals. Quality is copied when saving a completed canary assessment and
remains readable after raw profile or telemetry expiry. Go, Node and Python SDKs
include the new response models.

Upgrade apid before profiled so the coverage reader understands the new versioned
metadata format. New apid reads both old and new records. Live backend merge and
browser verification are still required before treating this feature as validated
in production. See ADR-822.

## Per-route request labeling consistency (Go)

`guestprofiling.ServeMux` and `RouteHandler` now count labeled request entries
while the Go CPU collector is active. For other routers, use:

```go
guestprofiling.WithRouteRequest(r.Context(), "GET /orders/{id}", func(ctx context.Context) {
    handler.ServeHTTP(w, r.WithContext(ctx))
})
```

`WithRoute` labels execution but does **not** count requests; use it for non-request
work. Nested request wrappers sharing a context count once. Instrument the final
matched route once so the counted label agrees with the execution label. Background
work should still clear inherited CPU labels with `WithoutRoute`.

Counters reset at each CPU capture and stop before its upload. Old-epoch reports
are discarded with pre-resume buffers. Counts are capped at 50 static route labels
and one billion entries per route/capture; any cap makes the report incomplete.
The guest bridge transports a bounded optional header, and the host keeps only
its admitted explicit contract labels. Invalid optional reports do not drop valid
CPU uploads. The host fingerprints admitted labels in bounded private metadata;
rejected labels and arbitrary request values are not stored. Counters are
application reports, not independently verified request attestations.

Route rows expose `label_coverage` with reported labeled entries, observed weighted
requests, labeling percentage when reconciled, and counted/boundary capture totals.
Counters are aggregated only from reports wholly inside the query window. Boundary
captures are excluded rather than estimated. The denominator is observed traffic
in the whole deployment window, so capture gaps and excluded boundaries can lower
the ratio. Request-entry timestamps and minute-bucket request telemetry can also
misalign counts. Counts above observed traffic are unavailable, not clamped to
100 percent. A one-second host tolerance accommodates pprof writer startup and
stop timestamps while still enforcing the VM lifetime.

Advisory route checks now require reconciled labeling shares of at least **80%**
in each window and a change of **less than 20 percentage points** in either
direction. Missing reports, cap/allowlist failures, unreconciled counts, low shares
or substantial differences produce `insufficient_data`. The prior capture, traffic
and overall attribution checks still apply. These guards do not change aggregate
CPU results, retries or rollout behavior. High shares do not prove complete or
consistent instrumentation for every request.

The dashboard shows each route's labeling evidence and saves the summaries with
its regression result, canary stage history and copied investigation assessment.
The API exposes `route_checks[].label_coverage`, and Go, Node and Python SDKs
include its models. CLI route output includes available labeled-entry counts and
shares. Historical results without this field remain readable; checking legacy
raw captures again produces insufficient labeling evidence. Node/Python collectors
currently lack request counters.

Upgrade **apid before profiled**, then deploy updated guest-init images and rebuild
Go applications with the new helpers. Old guest-init ignores the new header, which
leaves counters unavailable. Older API readers cannot parse the extended coverage
record. This increment has build and syntax checks; live backend, runtime and
browser acceptance remain pending. See ADR-823.

## Advisory route regression notifications

Automatic deployment and canary CPU checks can notify existing app webhooks when
an explicit route regresses or recovers. Enable automatic checks, configure
`config.options.routes` with static labels such as `POST /checkout`, and set
`config.notify_route_regressions: true` when replacing the policy through
`PUT /v1/apps/{slug}/profiles/deployment-policy`. Include the current
`expected_revision` and the other existing policy settings. The automatic policy
form in the Profiles dashboard exposes the same checkbox. Notifications default
to disabled and require enabled checks with at least one advisory route.

Subscribe an app webhook to `profile.route_regressed` and
`profile.route_recovered`. An empty app event filter includes both. Signed delivery,
retries, delivery inspection, and replay use the existing webhook system.
Receivers should deduplicate retries using the webhook delivery identity and
correlate the two events by `incident_id`.

Route alerts reuse the configured minimum requests, minimum profiles, capture
coverage, and both relative and absolute CPU/request increase thresholds.
Attribution must be available and stable. Request-label coverage must be at least
80% in both windows, with a change below 20 percentage points. Insufficient,
failed, or missing evidence does not produce recovery. These events are advisory
and remain advisory unless an explicit canary profiling gate is enabled.

Repeated regression results for the same application, scope, baseline,
candidate, policy revision, and route produce one incident notification. A
fresh sufficiently supported healthy result for that same context produces one
recovery notification. Older windows cannot overwrite newer observations.
Different deployment pairs or policy revisions start independent contexts.
Disabling notifications prevents future transitions but does not retract an
already queued event.

The event payload identifies the deployment pair, policy revision, route metric,
request counts, label coverage, windows, and evidence path. `investigation_path`
links to a saved comparison when quota permits. Canary events fall back to their
retained history endpoint when the saved-investigation quota is full; that history
has the existing 30-day retention. `application_frames` contains at most five
bounded application-level hotspot frames; these do not prove which function
caused a particular route's change. Raw samples are excluded.

Apply the route-alert migration and deploy the API/worker before enabling the
setting. Deployment and canary checks produce rollout observations. Enable the
optional periodic policy below to continue checking running deployments.


## Periodic profiling of running deployments

Add a `periodic` object to the existing automatic policy configuration, retaining
its capture settings, explicit `options.routes`, thresholds and current
`expected_revision`:

```json
"periodic": { "interval_seconds": 900, "confirmations": 2 }
```

The Profiles dashboard provides the same controls. Checks must be enabled;
intervals must be whole minutes, between 60 seconds and one day, and at least the
capture window. Confirmations range from one to five. Omit or set `periodic` to
null to disable monitoring. `notify_route_regressions` controls webhook delivery;
periodic incident history remains available with notifications disabled.

Each route on a live, completed deployment pins its first evidence-qualified
window as a reference. The first self-comparison establishes sufficient capture,
traffic, attribution and labeling quality, not proof that the initial code is
fast. Later non-overlapping windows compare against that fixed reference. Two
confirmations, for example, require two consecutive supported regressed results
before opening an incident and two supported healthy results before recovery.
A terminal inconclusive window interrupts the sequence and never resolves an
open incident. Retries and recovered leases preserve the original windows and
cannot count the same window twice.

A replaced deployment, disabled policy, policy revision change, account hold or
ineligible plan stops old work. Parked deployments can still be checked, but idle
applications usually produce insufficient evidence. Profile-retention expiry
records `baseline_expired` and requires a new reference; it does not report a
recovery. The new reference starts a separate incident context.

Inspect `GET /v1/apps/{slug}/profiles/periodic-monitors` with app-read access and
completed MFA, or use the periodic incident history in the Profiles dashboard.
The response includes active/inactive status, pinned windows, scheduling state,
route metrics, incident identities, transitions and comparison links. It returns
up to fifty newest monitors with up to ten terminal observations each, bounded
to thirty days. On transitions, a saved investigation is created when quota
permits; otherwise the history comparison link and evidence remain available.
Samples retain the existing backend retention limits.

Periodic events reuse `profile.route_regressed` and `profile.route_recovered`,
with payload `source: "periodic"`. Incident contexts include the pinned baseline
window so periodic results cannot recover rollout incidents or incidents against
an expired reference. Event delivery remains advisory and at least once. Apply
migration `20261008210606568_profile_periodic_monitors.sql` and deploy the updated
API/worker before enabling this setting.

### Route-specific code evidence

Automatic deployment, canary and periodic route checks also compare code from
profiles filtered to the configured route. Qualified route capture and request
label coverage are required first. `code_evidence` ranks threshold-crossing
functions (self CPU) and complete call paths (inclusive CPU) by their increase
in CPU seconds per observed route request. Call paths overlap; do not sum them.
Missing samples, symbols observed on only one side and zero baselines remain
unknown. `code_reason` explains unavailable or truncated evidence. These
observations do not prove which code change caused a regression.

Evidence uses the same frozen deployment windows, including the pinned periodic
baseline. Additional route queries share the parent timeout and query admission
slots; an unavailable code comparison does not discard a valid route CPU check.
Historical evidence is bounded by the existing assessment storage budget and
stores only symbols, relative source paths, lines and metrics. Dashboard source
links are derived from deployment commits at read time. Route webhook payloads
include the retained code evidence and a `comparison_url` opening the exact
route/windows and highlighting the first recorded call path when still observed.
Application-level hotspots remain separately identified as `application_frames`.

### Canary deployment gates

Enable **Canary profiling gate** in the automatic profiling policy to require
qualified route CPU/request evidence before advancing a canary. Checks remain
advisory when `canary_gate` is omitted or null. Gate configuration requires
enabled automatic checks and explicit `options.routes` on a traffic-split plan.
For example, add this field to the existing deployment-policy config:

```json
"canary_gate": {
  "confirmations": 2,
  "timeout_seconds": 1800,
  "on_timeout": "hold",
  "auto_rollback": false
}
```

The timeout must cover warmup plus every confirmation window and ingestion grace.
Distinct nonoverlapping windows compare the same stable and candidate deployment
in the current stage. A qualified regression on any route must repeat for the
configured number of windows to hold the rollout. All routes must accumulate
healthy confirmations to pass. Unknown evidence resets that route's streak.
Insufficient evidence remains inconclusive: `hold` waits for review; explicitly
choosing `continue` permits advancement after the deadline with inconclusive
profiling evidence. A confirmed regression remains held regardless of timeout.
Other route, health and binding gates still apply.

Inspect the baseline, route streaks, functions and call paths on the deployment
dashboard, through `GET /v1/deployments/DEPLOYMENT_ID/canary/profile-gate`, or with
`gregale canary gate DEPLOYMENT_ID --json`. The readout uses retained evidence and
commit-pinned source links; it does not query profile storage. Changing the policy
revision starts fresh evidence collection for the current stage. A passed gate
certifies those recorded windows for that stage, rather than continuously
monitoring the stage after it passes.

A customer may explicitly override only the profiling gate:

```sh
gregale canary advance DEPLOYMENT_ID --expected-step 1 \
  --profile-policy-revision 7 \
  --profile-override-reason "Reviewed checkout profiles and accepted the CPU cost."
```

The equivalent advance request supplies `profile_gate_override` with
`expected_policy_revision` and `reason`. The current revision, reason and
customer actor are recorded atomically with the traffic change. Workers cannot
override. Generic traffic edits and legacy recovery promote/advance cannot
bypass the gate; use the stage advance API or manually abort the rollout.

Opting into `auto_rollback` lets the worker restore the exact stable predecessor
when a route regression is confirmed, without waiting for stage dwell. APID
rechecks the current policy, stage, evidence, worker lease and binding release
checks. A stale rollback request never becomes a promotion. This direct rollback
supports request-mode canaries; service recovery requires its checked handoff
flow and remains held for review. Automatic rollback is disabled by default.
Gate evidence, including code attribution, is observational rather than proof
that a deployment's code caused the regression.
