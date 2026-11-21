# Post-deployment monitoring verification

`gregalectl obs verify-deployment` waits for usable monitoring evidence after a
rollout and saves a release-linked JSON report. It checks expected serving
probes, the aggregate notification pipeline and every expected notification
receiver. It reads the existing operator APIs and performs no deployment,
rollback, direct URL probe or notification send.

## Run it

After confirming release convergence, record the boundary and run the verifier
on the control plane, where the API defaults to loopback:

```bash
release="$(git rev-parse HEAD)" # checkout of the selected signed release
evidence_after="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
gregalectl obs verify-deployment \
  --release "$release" --after "$evidence_after" \
  --report post-deployment-monitoring.json --json
```

`--release` requires the full 40-character lowercase Git SHA. `--after` requires
an RFC3339 timestamp representing the rollout evidence boundary (allowing the
existing 30-second clock skew). Record it **after** release convergence and
retain it across retries. Setting an older boundary weakens the freshness claim.
The SHA labels the report; the three health gates do not prove runtime identity.
Optional advisory build evidence compares observed metric identities with that
SHA for explicitly selected critical jobs. The platform workflow separately
verifies fleet release convergence.

### Runtime collector overload

Each apid admits at most two source-backed runtime collections at once. The cap
is shared across operators, keys, versioned profiles and diagnostic selections;
it applies even when metrics are disabled. There is no waiting queue or shared
cached report. Authentication and query validation run before admission.

At capacity the endpoint returns HTTP 429 with problem code `obs_runtime_busy`,
`limit` and `observed` values, and `Retry-After: 1`. Retry after the hint, or reduce
overlapping verifiers. The delay reserves no slot. Requests without an expected
role keep their source-free unknown response; requests already canceled perform
no source reads. Admitted work retains capacity until it finishes unwinding after
cancellation, including aggregation and preview fitting. The existing
three-second source deadline remains unchanged.

The CLI records HTTP 429 with `X-Faas-Error-Code: obs_runtime_busy` as advisory
unknown (`source_runtime_busy`) in the report and poll history. The terminal
summary points to overlapping verifiers and the Grafana capacity panels.
Other HTTP 429 responses remain `source_rate_limited`. The next scheduled poll
follows the existing verification budget and health gates. Refusals count
separately from completed reports and source-read attempts.

### Runtime collector metrics

Open **Gregale Runtime Diagnostics** in Grafana to compare active collections
and refusals by apid, report outcomes, source read failures and latency by
diagnostic group, and preview omissions. Compare collection p95 with the
three-second source budget as a reference. The duration histogram also includes
aggregation and response fitting. Latency and size panels leave gaps when
the collector has no observations.

Use **apid instance** to narrow every panel to one scrape target. The default
**All** selection shows every apid instance in the time range. The selector
includes failed scrapes and refreshes with the dashboard time range.
Use **Evidence group** to focus the core evidence warning rows and the panels
that break down evidence by group. Its **All** selection shows every group.

The runtime collector warning notifications link to the affected apid in
Grafana. Firing warnings show the last two hours. Resolved warnings show the
two hours ending at each alert's recovery time. Incomplete-evidence warnings
also select the affected evidence group. Open the usual SSH/IAP tunnel
to use the default `http://127.0.0.1:3000` links. Set Alertmanager's `am_grafana_base_url`
to the URL operators use when they access Grafana through another address or
mount path. Each warning also links to its runbook.

Start with **Runtime telemetry coverage by apid** when other panels are quiet.
**Reporting** includes idle collectors. **Runtime metrics missing** means the
scrape succeeded without the runtime collection gauge, which can happen during
a rolling upgrade. **Scrape failed** means Prometheus could not scrape the
instance. **No matching apid targets** means no apid `up` series matches the
instance filter at the end of the selected time range.

The **Runtime collector warning timeline** shows when warnings were pending,
firing, and clear for the selected apid. It shows a separate row for each core
evidence group. **Clear** requires a successful scrape with the warning's
source metric present. Core evidence also requires at least 10 evaluations
for the same apid and group over 30 minutes. **Insufficient evaluations**
marks core evidence rows below that floor when the scrape succeeds and the
source metric is present. Pending or firing warnings take precedence.
**Scrape failed** and **Runtime metrics missing** show gaps in coverage
during the same time range.

**apid revision changes** marks observed upgrades and rollbacks with the
instance and short commit SHA. The marker time is the scrape that first saw the
new revision. Markers require two closely spaced, successful scrapes with
different valid revisions. The first observation after a scrape gap
establishes a new baseline.

**apid restarts** marks the first scrape that sees a drop in an instance's
uptime. It also covers restarts on the same revision. Detection requires two
closely spaced successful scrapes with uptime samples. A restart during a
scrape gap can go unobserved.

**Time since the last complete runtime report by apid** shows the age of
the last report with complete evidence for its selected diagnostics.
**No complete report since startup** means apid has yet to collect one.
Compare it with **Last runtime collection by apid**, which shows the age and
outcome of the latest finished collection. A recent incomplete or timed-out
collection explains why the complete-report age can grow even while requests
are finishing. An idle collector keeps its previous outcome as the age grows.
**No collection since startup**, **Collection metric missing**, and **Scrape
failed** identify why an age is unavailable. Older replicas can still report
other runtime metrics during a rolling upgrade.

Apid's existing private `/metrics` registry exposes self-monitoring for accepted
`runtime-builds` reads. These metrics describe this process's collection work;
HTTP 200, collection completeness and daemon health are separate observations.

| Metric | Meaning |
|---|---|
| `apid_operator_runtime_active_collections` | Current admitted source collections in this apid, including work still unwinding after cancellation. Reads the shared admission object. |
| `apid_operator_runtime_refused_collections_total` | Capacity refusals before source reads; excludes authentication/validation refusals, unselected roles and preflight cancellation. |
| `apid_operator_runtime_reports_total{outcome}` | Completed collections: `complete`, `incomplete`, `deadline_exceeded`, `canceled`, `unselected`, or `encoding_error`. Fresh mismatches and not-ready results can still be complete evidence. |
| `apid_operator_runtime_incomplete_reports_total{group}` | Reports with outcome `incomplete`, counted once for each affected core or selected diagnostic group. |
| `apid_operator_runtime_group_evaluations_total{group,outcome}` | One evaluation per group in each complete or incomplete report. `outcome` is the completeness of that group's evidence. |
| `apid_operator_runtime_core_evidence_gaps_total{group,reason}` | One event per affected core group and reason in an incomplete report. Full-set summaries include evidence outside the retained preview. |
| `apid_operator_runtime_last_complete_report_timestamp_seconds` | Unix time of apid's most recent `complete` report. Zero until the first complete report in that process. |
| `apid_operator_runtime_last_collection_timestamp_seconds{outcome}` | Unix time of apid's latest finished collection, with one sample for its report outcome. Zero with outcome `not_collected` until a collection finishes in that process. |
| `apid_operator_runtime_collection_deadlines_total{phase,origin}` | One event per completed report with `deadline_exceeded`. The origin is apid's `collection_budget` or an inherited `caller_deadline`. |
| `apid_operator_runtime_collection_duration_seconds` | Collection time, including initial identity reads, aggregation and preview fitting. |
| `apid_operator_runtime_phase_duration_seconds{phase}` | Wall-clock time for each collection phase. Parallel reads contribute one elapsed time to the phase. |
| `apid_operator_runtime_inventory_attempts_total{outcome}` | Compute inventory checks by `success`, `invalid_inventory`, `source_error`, `deadline_exceeded`, `canceled`, or `not_configured`. Successful empty inventory counts as success. |
| `apid_operator_runtime_query_attempts_total{group,outcome}` | Source-read invocations: `success`, `source_error`, `deadline_exceeded`, `canceled`, or `not_configured`. Includes preflight rejection without an HTTP request. `source_error` includes HTTP, transport and strict payload failures; raw errors are excluded. |
| `apid_operator_runtime_query_source_errors_total{group,reason}` | Detail for the `source_error` outcome. Reasons are `transport_error` for a failed HTTP request, `http_error` for a non-200 response, `invalid_response` for an unreadable or invalid vector payload, and `other` for remaining failures such as an invalid request URL. |
| `apid_operator_runtime_query_duration_seconds{group}` | Source-read latency through decoding, including failed invocations. |
| `apid_operator_runtime_response_bytes` | Encoded payload size including the JSON newline, before transport writing; not acknowledged delivery. |
| `apid_operator_runtime_preview_omitted_items_total{group}` | Items omitted by preview and response limits, counted again on each report. Full-set summaries retain their coverage. |

`group` is one of `identity`, `readiness`, `process`, `loop_health`, or the
fourteen fixed diagnostic group names below. Readiness and process details
share the identity preview omission count. Selection excludes reads without
counting them as failed. Query success alone does not establish valid or fresh
domain evidence: inspect the report's full-set counts and incomplete flags.
Use **Incomplete runtime reports by evidence group** to find which groups
contribute to the incomplete outcome, including groups with successful source
reads. One report can appear in several groups.
The **Incomplete report rate by evidence group (30m)** table pairs the
incomplete share with the estimated number of group evaluations in the
30 minutes ending at the selected time. Both columns use the **apid instance**
filter. Groups appear when Prometheus observes an increase in evaluations.
**Core evidence gaps by reason (30m)** uses the same window and instance
filter. A single report can count in several rows. Identity uses the report
summary's `unavailable` category for conflicting targets. The process rows
also identify changes in the target set, repeated samples, and unavailable
comparisons.

**Source read failures by reason (5m)** breaks down recent source errors by
apid and evidence group. Use **Non-successful source reads by group (5m)** for
deadlines, cancellations, missing configuration, and totals. Older replicas
may report aggregate source errors during a rolling upgrade.

A collection deadline outcome means its shared context expired before completion;
a source-read deadline can also expire independently. All counters reset with the
apid process. No labels contain release SHAs, nodes, target IDs, queries, source
URLs, selection combinations or upstream errors.

The **Collection phase duration p95 by apid (5m)** panel shows five phases.

- `inventory` loads and validates compute inventory for control-plane profiles.
- `core_evidence` reads build identity, readiness and process evidence and builds
  the initial summaries.
- `loop_and_advisory` collects and validates loop and selected advisory evidence.
- `assembly` combines loop and advisory summaries.
- `preview_fitting` fits the report within its response size limit.

Static profiles skip inventory, and unselected roles skip the collection phases.
Each phase has its own distribution. Compare the lines to locate a slowdown and
use the total duration panel for overall p95.

The **Collection deadlines by phase and origin (5m)** panel shows where expiry
was first observed. `before_collection` means the caller deadline had already
expired at entry. `outside_phases` means expiry could not be assigned to a
measured phase. Compare phase durations to see how much time earlier stages
also consumed. Individual Prometheus query timeouts appear in source read
outcomes while the collection context still has time. Caller disconnects that
cancel the request appear as `canceled` report outcomes.

The capacity and deadline warnings each require an estimated three or more
events in the previous five minutes for five consecutive minutes, with a
successful apid scrape.

- [FaasRuntimeCollectorCapacityRefusals](../runbooks/FaasRuntimeCollectorCapacityRefusals.md)
  points to HTTP 429 refusals before collection starts.
- [FaasRuntimeCollectorDeadlines](../runbooks/FaasRuntimeCollectorDeadlines.md)
  points to reports whose collection context expired. A report can have several
  timed-out source reads, so the warning counts it once.

[FaasRuntimeCollectorIncompleteCoreEvidence](../runbooks/FaasRuntimeCollectorIncompleteCoreEvidence.md)
warns when one core group on a successfully scraped apid has at least 10
estimated evaluations in 30 minutes and at least half are incomplete.
The condition must hold for 15 minutes. The warning names the affected group
and links to that replica's dashboard.

### Advisory running-build evidence

Pass an explicit versioned build profile with `--expected-role` to include
`runtime_builds` in the JSON and human report. For example, add
`--expected-role control-plane-v2` to the verification command above after
recording the same release SHA and evidence boundary. This adds one protected
read per poll from `GET /v1/admin/obs/runtime-builds?release=...&after=...&expected_role=...`.
Without the flag, the report explains that the role was not selected and makes
no additional HTTP request. An older server returning 404 remains advisory
unknown. Build evidence never changes the health gates, exit codes, stability
window or confirmed-fault flag, and does not add another polling iteration.

The original profiles remain unchanged: `single-box-v1` covers API, public
gateway, scheduler and VM manager; `control-plane-v1` covers API, public gateway,
control-plane scheduler, compute gateway/scheduler and VM manager. Their job
sets and selectors are frozen independently of telemetry-input roles.

`single-box-v2` and `control-plane-v2` add `builderd` and `imaged` to their
corresponding v1 profiles. Single-box expects at least one static scrape target
for each. Control-plane expects both on each active configured compute-registry
member, matching the existing service-discovery scope. Select v2 when both
services are part of the intended placement. Neither v1 nor an unset role is
automatically upgraded. `obs input-health` keeps its two existing v1 roles and
minimum readiness/input checks; v2 names are build profiles only.

This is a selected scope, not every platform daemon. Static jobs use observed
scrape membership and a minimum of one target. Compute jobs use exact active configured node names and
IDs from the registry. An unexpected old node cannot replace evidence for a
missing newly registered node merely because the counts match. Inactive and
unconfigured nodes are outside this scope. Unavailable registry membership is
unknown, not an empty expected fleet.

`matching`, `mismatched` and `mixed` describe fresh unambiguous sampled commit
identities. `incomplete` or `unknown` explains insufficient evidence. Mixed and
mismatched states can coexist with missing data: always inspect per-job source
availability and `missing`, `stale`, `unavailable` and `unexpected` counts.
Unknown/dev revisions, down scrapes, conflicting targets/build series and
samples older than 120 seconds or before the evidence boundary do not count as
matches. Boundary and future checks allow the existing 30-second clock skew.
This does not attest binaries or demonstrate continuous rollout convergence.

Counts and revision diversity include the complete inspected set, even beyond
the deterministic 32-item preview. `omitted_items` includes preview and report
envelope omissions; `envelope_omitted_items` identifies the latter separately.
The full report keeps its existing 1 MiB bound and removes advisory build
preview items before other retained evidence. Summaries and job availability
survive. Only approved node names, full SHA-256 target fingerprints, complete
commit SHAs, bounded safe version labels and original sample clocks are saved;
target addresses, arbitrary labels, build-time strings and raw errors are
excluded. API responses remain within 64 KiB.

The SSH collection adapter accepts the optional environment input
`MONITORING_EXPECTED_ROLE` with any of `single-box-v1`, `control-plane-v1`,
`single-box-v2` or `control-plane-v2`. For an explicitly selected profile:

```bash
MONITORING_EXPECTED_ROLE=control-plane-v2 \
  python3 scripts/ci/post_deployment_monitoring.py
```

Use the existing `DESIRED_RELEASE`, `MONITORING_NOT_BEFORE`,
`MONITORING_OPERATOR_TOKEN`, `CP_HOST` and SSH-key inputs. An empty or unset
role preserves the existing verifier invocation; the adapter does not infer a
role from observed targets or enable one in the deployment workflow. The
operational owner selects the role when activating this collection input.
Invalid nonempty roles produce `invalid_expected_role` without contacting SSH.
Initialization still creates a private unknown artifact without remote reads.

Collection forwards the role to the pinned release binary. An older CLI that
cannot accept the flag leaves verification unknown; it is not retried with the
role omitted. An older API returning 404, or a report without the optional
section, remains advisory unknown (`source_endpoint_not_found` or
`evidence_not_returned`) while preserving any valid health result.

The adapter checks the optional evidence's release, role, boundary, clocks,
closed metadata, counts and existing size limits before retaining approved
fields. Unknown extra fields are discarded, including nested provider details.
Malformed optional data becomes advisory `invalid_advisory_evidence`; it does
not alter the health checks, confirmed-fault flag or verifier exit code. A
malformed mandatory report or an untrusted transport still cannot qualify.

Job summaries show advisory state, fixed source codes/status, full-set counts,
preview omissions and per-job source availability. Observed node, target,
version and commit labels stay in the private artifact. A mixed state can
coexist with unknown job inventory, so inspect availability as well as counts.
Artifact fitting removes advisory build preview items first and accounts for
them in `omitted_items` and `envelope_omitted_items`. If advisory metadata must
also yield to the 1 MiB bound, the section becomes unknown `report_envelope`.
Mandatory evidence retains priority. The adapter rejects a mandatory report
that cannot fit the serialized envelope.

With the same explicit role, `runtime_builds.evidence.readiness` reports
advisory daemon readiness independently of build matching. For example, a daemon
can run the requested revision while `not_ready` with reason `draining` or
`database`. Counts cover the full inspected set, including targets beyond the
preview. Per-item `readiness` carries the original ready and active-reason sample
clocks. Fresh successful scrapes, post-boundary samples, a complete one-hot
reason vector and agreement with the ready gauge are required. Missing, stale,
conflicting and unavailable observations cannot establish readiness. Only the
ten approved metric reason codes reach artifacts and aggregate job summaries.
An older server omitting readiness remains valid identity evidence and readiness
is unknown.

`runtime_builds.progress.readiness` records first/last ready and not-ready,
observed losses, last loss/recovery, `ready_throughout_observed_polls`, and sticky
incomplete evidence. A source gap interrupts continuous observed readiness;
it does not create a loss or recovery. A later known observation can establish
an aggregate transition across that gap. The first not-ready observation is
not a loss from previously observed readiness. These observations describe
aggregate selected-target state, not per-daemon lifecycle or uninterrupted
availability between polls. Optional readiness aggregates share the existing
build-history event ring and report envelope; sample-clock and preview changes
do not create events. The identity state and deployment qualification remain
independent. See [the readiness decision](../adr/20261115120001-advisory-daemon-readiness-evidence.md).

With the same selected profile, `runtime_builds.evidence.process` adds advisory
sampled process continuity from existing daemon uptime. The first fresh read is
only a `baseline`. During later existing polls the CLI supplies the previous
valid response clock as `compare_at`; the API compares the full inspected sets
before previewing. No additional poll or protected HTTP read is introduced.
A verification that passes on its first poll retains a baseline, not a claim
of no restarts.

`changed` means an observed uptime reset or later estimated process start.
`unchanged` means no change was detected in the compared sample pairs. A fresh
same-revision process replacement remains visible even if the daemon is ready
and runs the requested SHA. Revision classification is `same`, `different` or
`unknown`; it does not establish whether a rollout was planned or a process
crashed. Current sample states distinguish fresh, missing, stale, conflicting
and unavailable data. Counts distinguish compared/changed/unchanged pairs,
new/removed targets, repeated samples and unavailable comparisons. Exact target
identity includes the node ID, keeping node replacement separate from restart.
Current expected membership defines the historical query scope.

Both original uptime sample clocks and values appear only in the protected
per-target preview. Comparisons permit the one-second producer tick and existing
clock skew: an uptime decrease over one second or a forward estimated-start
shift over 31 seconds indicates change. Regressed sample clocks and excessive
backward estimated-start shifts cannot establish continuity. The comparison
clock must precede generation, remain within ten minutes, and respect the
release boundary. Old/missing sources and unexpected targets remain incomplete.
These sampled observations cannot count every restart or prove uninterrupted
process survival between polls. The optional launcher restart counter is not
used as proof of continuity.

`runtime_builds.progress.process` preserves compared-pair and observed-change
totals, same/different/unknown revision changes, membership changes, first/last
change observations and sticky incomplete evidence. Process summaries share the
existing bounded history. Clock churn does not create events; distinct change
intervals do, even when their counts match. Replayed or overlapping windows do
not increase progress totals. The existing report fit preserves aggregates and
progress while dropping preview items and older events first. Job summaries
include only reviewed aggregate fields and clocks.

Older APIs without process evidence keep their identity/readiness results.
When an older API rejects `compare_at` during a rolling cutover, the next
existing poll reads without it; no immediate retry or downgrade of the profile
occurs. Invalid optional process evidence is isolated from build identity,
readiness and health qualification. See [the process-continuity decision](../adr/20261115130000-sampled-daemon-process-continuity.md).

The same selected profile adds advisory `runtime_builds.loop_health`. It uses
existing watchdog reports: `runtime` for each daemon, plus scheduler `main` and
VM-manager `sweep`. A ready process with the expected SHA and unchanged uptime
can still report a stalled work loop. Other work loops remain outside this
scope; a runtime heartbeat alone does not prove the daemon is doing useful work.

`reported_stalls` counts independently fresh `loop_stalled=1` reports. A positive
report stays visible when its heartbeat-age evidence is missing or unavailable:
the aggregate is `stalled` with `incomplete=true`. Complete item states distinguish
`stalled` and `no_reported_stall` from missing, stale, conflicting or unavailable
evidence. A stale one does not establish a current stall. Fresh successful up
samples, original metric clocks and the same rollout boundary/freshness rules
apply. The producer decides its configured stall budget; age is diagnostic.
No reported stall is a sampled report, not proof of continuous loop progress or
an independently healthy sampler.

Counts cover all inspected target-loop pairs before the separate 32-item preview.
Fresh reported stalls appear first, then incomplete observations. Unexpected
targets and unknown inventory prevent a complete clear aggregate. The protected
preview retains approved loop names, safe node names, target digests, reported
bits, bounded ages and original scrape/stall/age clocks. Addresses, arbitrary
labels and raw provider errors are excluded. The 64 KiB API response trims loop
detail first when needed; the CLI keeps its existing 1 MiB report envelope and
records `loop_envelope_omitted_items` separately from source preview omissions.

`runtime_builds.progress.loop_health` preserves first/last stall and complete
clear observation, last recovery/regression, stall regressions,
`no_reported_stall_throughout_observed_polls` and sticky incomplete evidence.
A source gap stops the throughout claim, without inventing a fault or discarding
a pending observed stall. A later complete clear aggregate records recovery in
the current selected scope; it does not prove the same daemon recovered.
Loop summaries share the existing bounded history. Routine age, sample-clock
and preview changes do not create events, and per-target samples stay outside
history and job summaries.

Loop evidence remains advisory. Older APIs omitting it, bad optional JSON and
invalid loop history preserve independently valid evidence and health decisions.
The protected API adds `loop_health` around its existing typed identity response;
CLI artifacts save it alongside `runtime_builds.evidence`. No new HTTP read,
poll, probe, watchdog action or operational activation is added. See [the work-loop decision](../adr/20261115140001-advisory-work-loop-health.md).

### Advisory builder progress and pressure

The explicit v2 profiles also include `runtime_builds.builder_health`; the
protected API returns it as optional `builder_health`. V1 profiles retain their
existing scope. This reads existing builderd metrics within the same API call
and evidence budget. Deployment qualification, poll timing and exits are unchanged.

The worker state distinguishes waiting, working, draining and overdue work.
Working can legitimately span the full build. The progress allowance matches
existing rules: twice the configured poll interval plus a 60-second observation
margin, additionally the configured busy allowance while in-flight. Initialization
is the origin before the first completed poll; completion includes idle/error
returns and does not establish build success. Overdue is a sampled assessment,
without the existing alert's five-minute hold or a new execution deadline.

Build and cancel queues show process-local waiting/active counts, buffered
capacity, workers and oldest waiting age. Dispatch handoffs can raise waiting
plus active to capacity plus workers. These notification hints are separate from
durable fleet backlog. Slot usage/limit describe local reservations; saturation
is capacity evidence, not a failed build or available host RAM. Do not sum these
observations into claims about fleet capacity or unique builds.

Each component requires fresh successful up and fresh original sample clocks.
Its `observed_at` is the oldest original sample clock among its required gauges.
Missing/stale/conflicting/unavailable components omit values. Fresh overdue
workers remain visible when queue/slot evidence is incomplete; inspect both
`overdue_workers` and `incomplete`. Counts cover all inspected targets, including
those omitted from the bounded preview. Builder detail yields first under the
existing API/report byte limits, with explicit omissions and preserved counts.

`runtime_builds.progress.builder_health` preserves overdue/regression/recovery
clocks and sticky gaps. Drain does not establish recovery; a later complete set
of enabled workers within allowance does. This may include restarted/replaced
processes. Aggregate changes share existing bounded history; age/clock/depth churn
does not consume events. Older APIs and malformed optional JSON preserve other
usable evidence. See [the builder decision](../adr/20261115150000-advisory-builder-progress-and-pressure.md).

### Advisory build-to-image handoffs

V2 profiles also expose `runtime_builds.handoff_health`, returned by the protected
API as `handoff_health`. It reads existing imaged collection/backlog/recovery
gauges within the same API request and evidence budget. Health qualification,
exits, stability and poll scheduling stay unchanged. V1 and unset profiles retain
their existing scope.

The three components separate collection status, pending handoffs and recovery
activity. Fresh collection metadata can report `success=false` or an expired
last-success clock. Neither establishes zero work. Backlog values require a
successful fresh snapshot after the evidence boundary, with coherent original
scrape clocks. Missing/stale/conflicting/unavailable parts omit values; fresh
pending evidence remains visible when recovery metadata is incomplete.

Pending values count succeeded builds without an image-preparation checkpoint
in each imaged recovery scope. Oldest wait starts at build finish, which can
precede the rollout. Replica scopes can overlap through legacy owner routing:
inspect each target and never sum pending counts into a fleet total. Aggregate
`waiting_scopes` and `empty_scopes` count observations, not unique builds. Known
empty requires fresh proof; no targets or unavailable inventory remain unknown.

Recovery state is waiting, working, stopping or not_started, with enabled,
inflight and attempt/completion clocks. Long image work can occupy one pass;
there is no new stall threshold. Stopping can retain in-flight work. A completion
is a pass return, including failure/no-op, not committed preparation or deployment
readiness. Image-preparation phases and downstream work remain outside this scope.

The preview prioritizes waiting scopes by oldest wait, then incomplete and empty
targets. Full-set counts survive preview/report clipping with explicit omissions.
`runtime_builds.progress.handoff_health` retains first/last waiting and complete
empty observations, sampled empty-to-waiting transitions and sticky gaps. These
observations do not establish successful recovery or delivery; routine clocks,
ages and pending magnitudes do not consume bounded history. Invalid optional
handoff data preserves all other usable evidence. See [the handoff decision](../adr/20261115160001-advisory-build-image-handoff-evidence.md).

### Advisory image-preparation phases

V2 profiles also expose `runtime_builds.preparation_health` (`preparation_health`
in the protected API), using existing imaged gauges in the same request budget.
It observes active and recoverable durable checkpoints in `preparing`,
`layer_published`, and `scan_complete`. Collection status and observer expectation
remain separate from each phase. A disabled observer can still have pending
work. Failed, stale, missing or pre-boundary collection never establishes empty
phases. Valid positive evidence survives a gap in another phase.

Each phase reports `pending` and `unknown_age`. Unknown ages are included in
pending. `oldest_phase_started_at` and `oldest_known_age_seconds` describe only
rows with known historical timing and are absent when no known ages exist,
including an observed empty phase. A zero oldest metric therefore never proves
zero work. Phase age includes time waiting to resume and can precede the rollout;
it is not the duration of a currently running step or a new stall threshold.

Replica recovery scopes may overlap: inspect per-target counts and never sum
them into a fleet total. Aggregate waiting/empty/unknown-age counts describe
observation scopes, including each phase separately. Empty requires all three
phases known empty; no targets stays unknown. Complete component evidence can
still contain historical unknown ages, which remain explicit.

Full-set aggregate counts survive preview/report clipping with explicit omitted
items. The shared bounded history records phase-scope and coverage changes;
routine ages, clocks and pending magnitudes do not consume events. Preparation
progress retains first/last waiting and empty observations, sampled transitions
from empty to waiting, and sticky gaps/unknown ages. Disappearing work does not
establish preparation completion or successful deployment. Existing deployment
qualification, exit behavior and poll scheduling remain unchanged. See the
[preparation decision](../adr/20261115170001-advisory-image-preparation-phase-evidence.md).

### Advisory snapshot priming

V2 profiles expose `runtime_builds.snapshot_prime_health` (`snapshot_prime_health`
in the protected API), using existing scheduler gauges within the same evidence
budget. Single-box-v2 observes `schedd`; control-plane-v2 observes both `schedd`
and `schedd-compute`. Each item retains its job and sanitized target. Scheduler
ownership scopes may overlap: compare them separately and never sum their
attempt counts into a fleet total.

The six ordered classes are `waiting`, `release_blocked`, `active`,
`publication_pending`, `snapshot_recorded`, and `unknown`. Per-class `pending`
counts current attempts, including active and recorded snapshots. Positive
evidence is therefore `nonempty`, without implying a stalled scheduler. A
recorded snapshot does not establish deployment readiness. Collection status
and observer expectation remain independent; disabled observation can coexist
with nonempty classes. Usable values require fresh successful collection after
the evidence boundary and coherent original scrape clocks. Failed, stale,
missing or conflicting evidence never becomes empty; partial positives remain.

`unknown_age` is included in `pending`. Only attempts with known timing contribute
to `oldest_attempt_at` and `oldest_known_age_seconds`; these fields are absent
when no known ages exist. The `unknown` class always has unknown timing. Attempt
age can precede the rollout and accounts for stage start, release completion,
runtime invalidation and known instance activity. It is not continuous idle
time, execution duration, or a new stall threshold.

Full-set nonempty/empty/unknown-age scope counts and per-class scope counts
survive preview/report clipping, with explicit omissions. Empty requires all
six classes known empty; missing targets and unknown inventory remain explicit.
History retains class-scope and coverage changes while excluding routine clocks,
ages and pending magnitudes. Progress retains first/last nonempty and empty
observations, sampled empty-to-nonempty transitions and sticky gaps/unknown ages.
Disappearing work does not prove recovery or successful deployment. Qualification,
exit behavior and poll scheduling remain unchanged. See the
[snapshot-priming decision](../adr/20261115180001-advisory-snapshot-priming-evidence.md).

### Advisory post-capture activation

V2 profiles expose `runtime_builds.post_capture_health` (`post_capture_health`
in the protected API), using existing imaged gauges in the same request budget.
The explicit scope is `replica_observations_of_shared_fleet`: every imaged
replica independently observes shared fleet data. Compare these views without
summing their deployment counts or inferring agreement. Different fresh reads
can legitimately disagree; this is distinct from conflicting source series.

The eight ordered classes are `publication_pending`, `activation_pending`,
`dependency_held`, `compatibility_held`, `hosting_verifying`, `hosting_retry`,
`gate_overdue`, and `unknown`. Holds and overdue gates reflect retained durable
evidence, not an independent present-eligibility or health verdict. All positive
class evidence is nonempty and remains advisory. Collection status and observer
expectation are independent; disabled observation does not establish drained
work. Usable class values require fresh successful collection after the evidence
boundary and coherent original scrape clocks. Failed, stale, missing or
conflicting parts never become empty; usable partial positives remain visible.

Each replica view retains per-class `pending` and included `unknown_age` counts.
Only known timing contributes to `oldest_boundary_at` and
`oldest_known_age_seconds`; both are absent when no known ages exist, including
empty classes. The unknown class always has unknown ages. Boundaries can be
capture park time, init publication, or retained gate/verification evidence and
can precede the rollout. Boundary age is not continuous time in a class,
execution duration, or a new stall threshold.

Full-set nonempty/empty/unknown-age replica-view counts and class-view counts
survive clipping with explicit omissions. Known empty requires all eight classes
observed empty. Bounded history records class-view and coverage changes while
excluding routine clocks, ages and pending magnitudes. Progress retains
first/last nonempty and empty observations, sampled empty-to-nonempty changes,
and sticky collection gaps/unknown ages. Disappearing work does not prove
successful activation or recovery. Deployment qualification, exits and polling
remain unchanged. See the
[post-capture decision](../adr/20261115190001-advisory-post-capture-activation-evidence.md).

### Advisory build recovery and builder VM cleanup

Explicit v2 profiles expose `runtime_builds.build_recovery_health`
(`build_recovery_health` in the protected API), using existing apid build-recovery
gauges within the shared request budget. Scope is
`replica_observations_of_shared_fleet`: every apid independently observes shared
fleet data. Compare replica views without summing build or cleanup counts.
Different fresh views can disagree without indicating conflicting source data.

Collection status, running build claims and cleanup obligations remain separate.
Work values require fresh successful collection after the evidence boundary,
fresh successful scraping and coherent original scrape clocks. Running evidence
can remain usable when cleanup evidence is missing, and vice versa. Collection
failure, missing data or stale evidence never becomes an empty fleet view.

Running values contain `count`, `oldest_started_at` and `oldest_age_seconds`.
Cleanup contains `pending`, `retried`, `oldest_created_at` and
`oldest_age_seconds`. Retried counts outstanding obligations with recorded
failure; it is a subset of pending, not attempts or a rate. Pending includes
claimed and retrying obligations. Cleanup age starts at obligation creation
and survives retries. Positive counts require valid timing; zero counts omit
both timing fields. Ages may precede the rollout and introduce no new deadline
or stall threshold. Running claims do not prove active VMs, and cleanup
obligations do not prove live or leaked VMs.

Aggregate counts describe replica views and component coverage. They survive
preview/report clipping with explicit omissions. Known empty requires both
running and cleanup observed empty. Bounded history records view-state and
coverage changes, excluding routine clocks, ages and work magnitudes. Progress
retains first/last nonempty and empty observations, sampled empty-to-nonempty
changes and sticky gaps. Disappearing work does not prove successful completion
or recovery. Deployment qualification, exits and polling remain unchanged. See
the [build-recovery decision](../adr/20261115200000-advisory-build-recovery-and-cleanup-evidence.md).

### Advisory app tasks and release commands

Explicit v2 profiles expose `runtime_builds.app_task_health` (`app_task_health`
in the protected API), reusing existing scheduler gauges. Single-box observes
`schedd`; control-plane also observes `schedd-compute`. Scope is
`replica_observations_of_shared_fleet`: compare scheduler views without summing
task counts. Local handlers and worker slots are separate from shared fleet work.

The fixed kinds are `release`, `manual`, `cron`. Each retains nine ordered
classes: `queued`, `retry_wait`, `held`, `deadline_overdue`, `restoring`, `running`,
`cancellation_pending`, `lease_expired`, `unknown`. Durable evidence requires a
fresh successful collection after the evidence boundary, a fresh successful
scrape and matching original clocks. Missing or failed collection stays unknown;
usable positive evidence survives gaps in other kind/class observations.

Each pair retains `pending`, included `unknown_age`, and oldest known boundary
and age when available. Restoring, retry-wait, held and unknown classes have
only unknown ages. Known boundaries reflect queued admission timing, running
start, cancellation request, expired lease or missed deadline; they may precede
the rollout and do not measure continuous residence in a class. Empty and
entirely unknown timing omit both timing fields.

Collection, observer expectation and dispatcher evidence remain independent.
Dispatcher state distinguishes configured opt-out (`disabled`), an enabled
inactive runner (`stopped`) and an active runner (`running`). Configured/in-use
slots count local handlers, including restore, execution and teardown. Last
successful claim includes empty results; busy slots need not claim. Last sweep
includes empty sweeps. Absent clocks mean no success observed yet. These signals
introduce no new progress deadline, execution verdict or deployment gate.

Full replica-view and kind/class counts survive clipping with explicit omissions.
Bounded history records task coverage and local activity categories, excluding
routine clocks, ages and work/slot magnitudes. Progress retains first/last
nonempty and complete empty observations, sampled empty-to-nonempty changes,
and sticky gaps/unknown ages. Disappearing tasks do not prove successful command
execution or deployment readiness. See the
[app-task decision](../adr/20261115210001-advisory-app-task-and-release-command-evidence.md).

### Advisory command-cron admission

Explicit v2 profiles expose `runtime_builds.command_cron_health`
(`command_cron_health` in the protected API). This explains command schedules
before they produce app tasks, using existing scheduler metrics. Single-box
observes `schedd`; control-plane also observes `schedd-compute`. Scope is
`scheduler_ownership_scopes`: ownership and legacy fallback can overlap, so
compare scopes without summing schedules.

The ten ordered classes are `due`, `overlap_due`, `replacement_wait`,
`deployment_missing`, `exclusive_due`, `disabled`, `suspended`, `account_held`,
`not_due`, `unknown`. Due scopes contain any first-five class evidence. Inactive
scopes contain disabled, suspended, account-held or not-due schedules. These
scope categories can overlap. Nonempty includes intentional inactive schedules;
it is not a failure or an execution verdict. Empty requires every class observed
empty. Schedule counts do not enumerate catch-up occurrences.

Durable values require fresh successful collection after the evidence boundary,
a fresh successful scrape and coherent original sample clocks. Failed, stale or
missing collection never becomes empty schedules; usable partial positives remain
visible. Observer expectation and sweep activity are checked independently of
collection success. Observer expectation describes the loop, separately from
each schedule being enabled.

Each class retains `schedules`, included `unknown_age`, and oldest known nominal
due boundary and age when available. The last five classes have only unknown
ages. Empty or entirely unknown timing omits both age fields. Known due timing
uses the existing canonical timezone/DST interpretation of the first unconsumed
boundary. It can precede the rollout and is not continuous eligibility, command
execution delay, last successful command or a new stall threshold.

Sweep values contain explicit `running`, `last_started_at` and `last_success_at`.
The clocks are absent before a start or success is observed. Idle does not mean
successful or disabled. Sweep success includes empty enumeration and owner
lookup, not admission of every command. The scan also includes inline HTTP work,
so scan activity alone cannot identify a command-runner failure.

Full scope and class counts survive preview/report clipping with explicit
omissions. Bounded history records class coverage, due/inactive scopes and sweep
activity, excluding routine clocks, ages and schedule magnitudes. Progress retains
first/last nonempty and complete empty observations, sampled empty-to-nonempty
changes and sticky gaps/unknown ages. Disappearing evidence does not prove
admission, delivery or command completion. See the
[command-cron decision](../adr/20261116080000-advisory-command-cron-admission-evidence.md).

### Advisory app-task operation outcomes

V2 profiles expose `runtime_builds.app_task_operations` (`app_task_operations`
in the protected API), using existing process-local operation counters for
`restore`, `renew`, `execute`, `destroy` and `complete`. Scope is
`process_local_counter_observations`. These are returned calls, not unique tasks,
customer verdicts or a fleet backlog. Errors, cancellations, lease loss and
capacity refusals remain separate. Execute success does not establish a
successful customer command; cleanup and completion calls have their own
acknowledgement semantics.

First samples are baselines, so old cumulative failures are not new activity.
Later comparisons reuse the existing pinned comparison time and uptime evidence.
Both counter points must be fresh relative to their evaluation times, after the
evidence boundary, and match the original scrape clocks of unchanged process
samples. Uptime advance must match elapsed scrape time within the existing
one-second reset tolerance. Changed/new processes, repeated samples, uncertain
windows, source gaps and counter decreases establish no delta. Sampled
continuity cannot rule out every intervening restart.

Comparable points retain their exact nonnegative difference, including explicit
zero. This is an observed difference between samples, not an extrapolated rate,
exact event timestamp or execution verdict. Capacity series can be absent until
first observed; missing series at either endpoint remain unknown, never zero.
Each of the 35 operation/outcome signals is independent, so usable partial
positives survive gaps elsewhere. A complete no-change claim requires complete
comparison evidence; missing sparse capacity remains explicit.

Full-set summary counts describe signals and their coverage. Per-operation
category counts identify signals with an increase, not numbers of returned calls.
Point values and deltas remain in the restricted target preview. Aggregates
survive preview/report clipping with explicit omissions. Bounded history records
comparison categories and coverage, excluding routine clocks, cumulative values
and delta magnitudes. Progress retains first/last observed increases and sticky
error, cancellation, lease-loss, capacity, baseline and incomplete-evidence flags.
These observations do not establish continuous error-free operation or command
completion. See the
[operation-outcome decision](../adr/20261116090001-observed-app-task-operation-outcomes.md).

### Advisory task completion and recovery

V2 profiles expose `runtime_builds.app_task_results` (`app_task_results` in the
protected API). This reuses `schedd_app_task_results_total` and
`schedd_app_task_recovery_total`, separately from operation-return evidence.
Scope is `process_local_counter_observations`: counters belong to scheduler
processes, not unique fleet tasks.

Completion acknowledgements use the returned durable row for `release`, `manual`
and `cron`, each with ordered `succeeded`, `failed`, `timed_out`, `cancelled`,
`queued` outcomes. Queued means a policy retry, not a terminal task result.
Failed acknowledgements can reflect customer verdicts or platform failures; the
counter does not identify a cause. Successful calls and acknowledgements do not
independently prove delivery, publication, readiness or promotion.

Recovery has three separate outcomes: `requeued_restores`, `failed_runs`,
`cancelled`. These are changes reported by successful recovery sweeps, excluding
completion acknowledgements. Expired restores can safely return to the queue
before dispatch; recovered running tasks can have uncertain command outcomes.
Policy retries absent from the existing sweep result are not inferred. Do not
sum completion and recovery evidence as unique completed tasks.

The same sampled comparison safeguards as operation outcomes apply: first
samples are baselines; fresh endpoint points, unchanged process proof, matching
original scrape clocks and coherent uptime advancement are required for an exact
nonnegative difference, including zero. Resets, changed/new/repeated processes,
uncertain windows and missing or stale evidence establish no delta. Sampled
continuity cannot exclude every intervening restart. Differences are not rates
or exact event timestamps. All 18 signals are assessed independently.

Full-set coverage and per-outcome signal counts survive preview/report clipping
with explicit omissions. Bounded history records coverage and outcome categories,
excluding routine clocks, cumulative values and delta magnitudes. Progress keeps
first/last increases and sticky flags for acknowledged successes, failures,
timeouts, cancellations, queued retries and each recovery outcome. It does not
sum overlapping poll deltas or introduce health gates, retry policy or extra
polls. See the [completion and recovery decision](../adr/20261116100000-observed-app-task-completion-and-recovery.md).

### Advisory command-cron outcomes

V2 profiles expose `runtime_builds.command_cron_outcomes`
(`command_cron_outcomes` in the protected API), using the existing scheduler
sweep, decision and admission counters. Single-box observes `schedd`;
control-plane also observes `schedd-compute`. Scope is
`process_local_counter_observations`: these are local counter observations,
not unique fleet schedules, occurrences, tasks or deliveries.

The three ordered sources retain 29 outcomes. Sweep has `success`, `error`,
`canceled`. Decision has `disabled`, `not_due`, `account_held`, `plan_limit`,
`replacement_wait`, `not_owned`, `invalid_schedule`, `lookup_error`,
`store_unavailable`, `no_live_deployment`, `canceled`, `legacy_queued`,
`legacy_skipped`, `legacy_stale`, `legacy_error`. Admission has `queued`,
`exclusive_pending`, `coalesced`, `skipped_overlap`, `missed_deadline`, `stale`,
`deployment_unavailable`, `rejected`, `error`, `canceled`, `unknown`.

Keep scheduling policy and return classifications separate from faults. A
successful shared sweep includes inline HTTP work and does not prove every
command was admitted. Queueing, pending exclusive work and coalescing do not
prove execution or delivery. A rejection is not automatically an infrastructure
failure. The admission outcome `unknown` records a returned classification;
missing telemetry is a separate evidence gap. Do not combine families as unique
work, or infer admission latency, command success or a new deployment gate.

Comparisons use the same process proof, original scrape clocks, freshness,
boundary and uptime-advance safeguards as app-task counters. First observations
are baselines, including historical errors. Changes in process, resets, repeated
samples, uncertain windows and source gaps establish no delta. Comparable points
retain exact nonnegative sample differences, including zero; these are not
rates or exact event times. All 29 signals are independent, and missing values
are never zero.

Full-set coverage and source/outcome signal counts survive preview/report
clipping with explicit omissions. Bounded history uses the existing lossless
compaction and excludes routine clocks and counter/delta magnitudes. Progress
retains first/last increases and a fixed-order bounded union of observed
source/outcome categories, plus sticky baselines and incomplete evidence. It
never sums overlapping poll deltas. Commands, cron/task identities, raw targets,
output and provider errors stay outside the artifacts. See the
[cron-outcome decision](../adr/20261116120001-observed-command-cron-outcomes.md).

Loop-health reads retain priority within the shared three-second API budget;
these advisory counters use the remaining deadline. Missing counter evidence
stays explicit when the source budget is exhausted.

### Observed runtime evidence history

With an explicit role, the verifier also retains `runtime_builds.progress` and
`runtime_builds.history` across the polls already needed for health verification.
The final state remains the current snapshot; history explains earlier mixed or
mismatched revisions, incomplete coverage and unavailable sources. For example,
a final `matching` state with `first_mismatch_at` present and
`matching_throughout_observed_polls=false` reports recovery from an earlier
observed mismatch. It does not claim matching builds throughout the run.

Progress keeps observed-poll count, first/last observation, first/last mismatch,
first/last matching observation, last recovery/regression and regression count.
A later matching poll confirms observed build recovery after a mismatch, even
when a source gap intervened. A later mismatch after matching is an observed
regression; its exact onset during any gap is unknown. Sticky
`observed_incomplete_evidence` and `observed_source_unavailable` flags survive
recovery. `matching_throughout_observed_polls` requires at least one poll and
every observed assessment to match; an unknown/incomplete poll makes it false.
These times and flags are independent of the health stability window and do
not affect `had_confirmed_faults`. Initial/terminal saves add no observations.
Coordinator timestamp regressions are clamped to the preceding assessment;
source generation clocks remain separately recorded.

History covers aggregate state/reason, source HTTP status, full-set coverage and
revision counts, and fixed job inventory/source availability. It records the
first assessment and semantic changes, excluding routine source-clock updates,
preview ordering and preview omissions. It does not track individual target or
revision changes that leave these aggregates unchanged. No additional API call,
poll or convergence gate is introduced. A run that passes on its first health
poll has only that observed build assessment.

The latest events fit within 16 events and 16 KiB of conservatively indented
serialized event-array data. Oversized combined snapshots retain their advisory
summaries losslessly in `compact_details` (`zlib-base64-v1`); core rollout state,
counts, jobs, readiness and process proof remain directly readable. The CLI
renders these summaries normally. Collection validates the decoded summaries and
saves a freshly encoded closed projection, with an independent 64 KiB decoded
ceiling. The encoded event array still uses the existing 16 KiB budget.

If even compaction cannot fit an event, its bounded core remains and
`omitted_sections` names the diagnostic detail that yielded.
`detail_omitted_events` counts retained events with omitted sections, and
`truncated` also covers those omissions. Omitted history detail does not mean
missing source telemetry, empty work or a successful outcome. Current evidence
and sticky progress remain separate; historical detail omitted from an event
cannot establish its earlier diagnostic categories.

Inspect `total_events`, `retained_events`, `retained_bytes`, `dropped_events`,
`envelope_omitted_events`, `detail_omitted_events` (absent when zero), limits and
`truncated` alongside progress. Lossless compaction is not truncation. First
observations and recovery/regression summaries survive older-event eviction.
Small events keep the original inline format, and old reports remain readable.
See the [history compaction decision](../adr/20261116110000-bounded-runtime-history-compaction.md).

The complete report remains bounded at 1 MiB: current advisory build preview
items yield first, followed by oldest build events, before older
notification/qualification history or primary detailed fault evidence. Fitting
preserves recorder data and counts envelope omissions separately.

The collection adapter retains and validates this optional pair. Historical
source clocks stay historical; they are not reassessed as stale merely because
collection happens later. A malformed history pair becomes
`history_unavailable_reason=invalid_history`, preserving valid current build
evidence, health results and exit codes. Reports without history remain
compatible. Job summaries show the final state alongside earlier mismatch,
matching-throughout-observed-polls and regression summaries, first/last times
and event retention. If optional metadata must yield entirely to the artifact
bound, `history_unavailable_reason=report_envelope` discloses the omission.

Use an existing operator session, or the existing bearer fallback
`FAAS_ADMIN_TOKEN`. The APIs enforce operator read authorization, MFA and the
allowlist. Automation can use `--token-stdin` to read a bearer from one
newline-terminated stdin line; this explicitly takes precedence over saved
sessions and environment tokens. It cannot be combined with `--admin-token`.
Never place the credential in report files or workflow command arguments.

The default wait is the largest expected probe's `max_result_age_seconds`
plus 120 seconds for fresh collection and evaluation. A policy age of 900
seconds therefore gives a 17-minute budget. Missing policy uses 20 minutes;
all automatic budgets are capped at one hour. `--wait-timeout 5m` overrides the
budget, and `--poll-interval 10s` sets the polling interval (1 second to 1 minute).
Each HTTP read has a 10-second ceiling and shares the remaining overall budget.

## Require a passing observation window

By default, verification exits at the first complete passing assessment. Add
`--stable-for 2m` to require a passing window within the existing wait budget:

```bash
gregalectl obs verify-deployment \
  --release "$release" --after "$evidence_after" \
  --stable-for 2m --wait-timeout 10m --poll-interval 10s \
  --report post-deployment-monitoring.json --json
```

The window starts with the first assessment where all three checks pass. It
requires both two minutes of local observation time and at least two minutes
of advancement in **every required source clock** from that first assessment.
These clocks include serving inventory, each expected probe's evaluation and
actual run, Alertmanager API and pipeline observations, aggregate integration
observations, and receiver collector, inventory and group observations. A new
HTTP response or evaluation of an unchanged probe run cannot prove stability.
All expected probes participate, including those outside diagnostic previews.

A quiet receiver can retain a historical successful-delivery value. Its fresh
`last_success_observation.observed_at` must advance; no new notification is sent
or required. Direct compute-node webhook and heartbeat history remains advisory
and does not contribute clocks or another qualification check.

A degraded or unknown check restarts the window. Changed probe/receiver or
integration expectations, changed probe policy, regressing source or local
observation clocks, or a gap between observations longer than the existing
120-second freshness ceiling also restart it. Repeated non-passing observations
do not repeatedly count the same reset. These are sampled observations; the
window does not prove continuous availability between polls.

The duration accepts zero through one hour; zero preserves the default behavior.
It cannot exceed an explicit `--wait-timeout`, and never extends that timeout or
the automatically derived budget. Allow time for initial evidence and probe
cadence: infrequent probe runs can require a longer wait than the requested
stability duration, even when every observed assessment passes. The platform
workflow keeps its existing default unless the option is explicitly supplied.
An observation received after the budget cannot complete the window; its
summary explains `observed_after_deadline` while retaining the healthy check rows.

With the option enabled, schema-version-1 JSON includes optional `stability`.
Human output prints its state, required and observed seconds, local elapsed
seconds, minimum source span, source-clock count, window/last-observation times
and reset count with the latest reset time/reason. `observed_seconds` is the
smaller of local elapsed time and the minimum source span. Progress stops at
the last actual observation. The summary states are `waiting`, `stabilizing`,
`passed`, `incomplete` at timeout and `interrupted` on cancellation/write error.

While all three health checks pass but the window is incomplete, the overall
state is `unknown` with `reason=awaiting_stability`; the check rows and health
timeline retain their actual passing assessments. At timeout this becomes
`reason=stability_window_incomplete`, exit 3. A current confirmed fault still
exits 1; current unknown health evidence retains its health reason and exits 3.
Cancellation and report-write errors preserve the last progress with the normal
exit 3 and 2 respectively. `had_confirmed_faults` records only the original
health checks, independent of the stability hold and advisory history.

See [ADR-20261114040000](../adr/20261114040000-deployment-monitoring-stability.md).

### Find what is holding back the window

New reports add optional `stability.diagnostics`. Its `state` is `not_observed`
before a health read, `available` when all checks pass with a valid source vector,
and `unavailable` when health or source evidence prevents starting a window.
Unavailable evidence omits `blockers`; it does not claim zero blockers. `checks`
lists the current non-passing checks with their reviewed state and reason.

With available evidence, `observation_remaining_seconds` shows the local time
still required. `blockers` lists sources whose clocks have advanced less than
the requested duration since this window's baseline. Each item identifies its
`kind`, pipeline `name`, node and hashed `url_id`, or receiver/integration as
appropriate. `baseline_at`, `previous_at` and `latest_at` retain original source
clocks; absent clocks are null. `advanced_seconds` and `remaining_seconds`
measure source advancement, independently of local elapsed time.

For example, a two-minute window can have zero local seconds remaining but one
`probe_run` blocker for `fsn-2`, with `advanced_seconds=45` and
`remaining_seconds=75`. This means that probe's run clock needs to advance
another 75 seconds; it does not predict the next run's arrival. If all sources
have advanced far enough but local time remains, `blockers.total=0` explains
that only the observation interval is still pending. A passing window has no
current source blockers and no local observation time remaining.

Each preview retains at most **four items** and **4 KiB** of conservatively
indented item-array JSON. The limits live in `pkg/api/limits.go`.
`total`, `omitted`, `truncated`, `item_limit`, `byte_limit` and `retained_bytes`
disclose retention. Current blockers are sorted by least advancement, then
source identity, for deterministic output. All required sources contribute to
the total, including probes outside the retained preview. The preview does not
contain raw URLs, response bodies, delivery-success values or provider errors.

`diagnostics.last_reset` retains the latest actual reset across subsequent polls
and recovery. It includes observation clocks, its fixed reason and any affected
health checks. With `source_context=reset_cause`, the bounded `sources` preview
identifies added/removed source clocks, changed probe budget/age policy with
before/after values, or regressing clocks with previous/latest timestamps.
Identity and policy changes count clock sources, not unique probes or receivers.
An observation gap includes `observation_gap_seconds`; a local clock regression
is visible in `previous_observed_at` and the reset's `at`.

For health/source loss, local clock regression or a polling gap,
`source_context=previous_window_blockers` retains the preceding window's blockers
as historical evidence. Those source clocks are not re-stamped at reset time,
and do not identify a cause of unavailable health. This also preserves the
preceding wait when a final API read fails at the deadline. Repeated unknown
polls retain the latest actual reset rather than replacing it on every poll.
Only one reset preview is retained; this is not a complete reset history.

Human output prints `stability_evidence`, `stability_blockers`, `stability_reset`
and `stability_reset_sources`. Timeout, cancellation and write-error results
preserve the last observed diagnostics. These additions use the existing reads
and qualification rules. They share the existing 1 MiB report envelope; advisory
notification events yield space before primary health and stability evidence.
Older reports can omit `diagnostics`.

See [ADR-20261114050000](../adr/20261114050000-deployment-stability-diagnostics.md).

## Interpret the report

| Exit | State | Meaning |
|---|---|---|
| 0 | `passed` | All three checks have complete usable evidence and any requested stability window is satisfied. |
| 1 | `degraded` | A current confirmed fault remains when the budget expires. |
| 3 | `unknown` | Evidence is incomplete, unavailable, unsupported or cancelled. |
| 2 | local error | Invalid options/credentials or a report could not be written. |

The three named checks are `serving_probes`, `notification_pipeline` and
`notification_receivers`. Each includes a stable reason and runbook link.
Confirmed faults take precedence over unknown peers. Transient faults can
recover during polling; `had_confirmed_faults=true` preserves that history even
if the final observation passes or becomes unavailable. The report retains the
latest validated source snapshot and a bounded timeline of check assessments.

## Inspect recovered faults and state changes

### Diagnose unavailable source reads

New reports add optional `sources.serving_health` and
`sources.notification_health` summaries for the two operator API reads. The
notification read serves both notification checks and is counted once per poll.
These are read diagnostics; health qualification still comes from the existing
three checks. An HTTP failure leaves affected checks unknown and does not set
`had_confirmed_faults` by itself.

Each source begins `state=not_read`, with zero attempts and null observation
clocks. After a read it is `succeeded`, `failed` or `cancelled`. Success means a
bounded HTTP 200 response was decoded and passed the existing basic projection
checks. It can still contain stale, partial or failing health evidence. An
invalid serving contract or missing notification generation clock is recorded
as `invalid_response`; optional advisory metadata and partial notification
evidence retain their existing independent validation behavior.

| Reason | Meaning |
|---|---|
| `unauthorized`, `forbidden` | Operator API returned HTTP 401 or 403. |
| `endpoint_not_found` | Operator API returned HTTP 404. |
| `rate_limited` | Operator API returned HTTP 429. |
| `server_error` | Operator API returned HTTP 5xx. |
| `redirect_rejected` | A redirect response was refused. |
| `http_error` | Another HTTP response was not the required 200. |
| `timeout` | Request/body collection exceeded a client or context deadline. |
| `cancelled` | Request/body collection was cancelled. |
| `transport_unavailable` | The request failed before a usable response. |
| `response_read_failed` | The response body could not be completely read. |
| `response_too_large` | The response exceeded the existing 1 MiB read limit. |
| `invalid_response` | JSON/type decoding or a basic source contract failed. |
| `invalid_url` | The configured API address could not form a request. |
| `unavailable` | No reviewed error classification is available. |

`http_status` belongs to the operator API response, including HTTP 200 when
body reading or validation failed. It is absent when no response status was
observed. Raw error strings are never parsed to guess a status or subtype.
Only fixed reason codes, numeric status and clocks enter these diagnostics;
request URLs, proxy credentials, response bodies and headers are excluded.

`reads`, `failed_reads`, `cancelled_reads` and `consecutive_failures` describe
attempted collection. An already expired context can prevent an attempt from
reaching the server. Cancellation is counted separately, preserves an open
failure streak and does not manufacture recovery. `last_success_at`,
`first_failure_at`, `last_failure` (reason/status/time) and `last_recovery_at`
survive later polls. A readable response after failed reads records read recovery
and clears the failure streak even when its health evidence is still unknown.
These clocks do not establish platform availability or notification receipt.

`last_read_started_at`, `last_read_completed_at` and `last_read_seconds` describe
the latest individual request and body decode, using monotonic elapsed time
when available. `last_read_at` uses that completion clock; readers without
request timing use the coordinator's observation time. The health timeline's
`at` remains the coordinator's observation after both reads complete. No clock
is extrapolated on timeout, cancellation or report-write error.

Affected check reasons use `source_<reason>`, such as `source_forbidden`.
Current check rows and historical transitions can include optional
`source_http_status`. A changed reason or HTTP status adds a transition even
while health stays unknown; repeated identical failures update source counters
without adding entries. Thus HTTP 502 changing to 503 is retained despite both
being `source_server_error`. Read history shares the existing 256-transition
cap, with two fixed summaries that survive rollover. There is no additional
unbounded event list or new API read.

Human output prints `source_read`, optional timing/last-failure rows and retained
`source_read_transition` entries. Source summaries and check transitions remain
available when report fitting omits older detailed fault snapshots. Older
schema-version-1 reports can omit these fields. Polling, wait budgets,
authorization and notification behavior retain their existing rules.

See [ADR-20261114070000](../adr/20261114070000-deployment-source-read-diagnostics.md).

### Find evidence that cannot qualify a check

New reports add optional `qualification_evidence`, with the latest assessment's
`observed_at` and one entry for each of the three checks. It explains readable
evidence that cannot yet qualify, including before a stability window starts.
It also applies when a peer check is degraded and the overall result is degraded.
The existing check assessments and success criteria continue to decide the result.

| Diagnostic state | Meaning |
|---|---|
| `not_observed` | No health snapshot has been assessed yet. |
| `passed` | The check has qualified; no current blockers are attached. |
| `faulted` | The check has a confirmed fault; inspect its retained fault evidence. |
| `blocked` | The check is unknown, with a readable source and specific unusable evidence. |
| `unavailable` | The source read or basic response validation prevented assessment. |

A `blocked` entry includes `blockers`. Items identify a fixed source `kind`,
pipeline `name`, validated node and hashed `url_id`, or approved receiver and
integration. `reason` distinguishes missing timestamps/signals/samples,
`before_deployment_boundary`, `stale`, `future_timestamp`, unavailable signals,
unknown probe state, unready collection, unavailable expectations and missing
or invalid metadata. Reviewed `source_state` / `source_reason` provide context
when available. Addresses, unreviewed labels/reasons and metric values are excluded.

`observed_at` on an item is its original source clock. `age_seconds` is measured
at the coordinator's assessment and remains null without a usable timestamp.
Future clocks have negative age. `max_age_seconds` and
`allowed_clock_skew_seconds` expose the existing policies. Probe run diagnostics
use the same millisecond age comparison and per-probe result-age policy as the
check; other observations use the existing 120-second freshness and 30-second
future-clock tolerance. Equality at a freshness or deployment boundary remains
eligible. A timestamp before `evidence_after` is identified before its age is
classified as stale; these are not observations of outage onset.

For example, an API read can succeed and the probe evaluation can be current,
while a `probe_run` item for `fsn-2` reports
`reason=before_deployment_boundary`. Receiver
`last_success_observation` diagnostics use the gauge's **observation timestamp**,
not its historical successful-delivery value. A quiet receiver with fresh
observations still qualifies without sending a notification.

Diagnostics follow the existing prerequisites. An unusable snapshot or serving
inventory clock is explained before descending into probes. Receiver collection
or inventory blockers are explained before group observations. Once those
prerequisites are usable, every expected probe/group and required pipeline
observation is inspected, including identities outside other previews. Totals
refer to the blocking stage inspected, not unassessed downstream evidence.

Each check retains at most **four items** and **4 KiB** of conservatively
indented item-array JSON, declared in `pkg/api/limits.go`. `total`, `omitted`,
`truncated`, `item_limit`, `byte_limit` and `retained_bytes` disclose those bounds.
Ordering is deterministic by kind and reviewed identity. Missing samples and
absent clocks retain their unknown meaning; they never become zero values or
healthy observations. Structurally invalid metadata is described without
copying its source labels or clocks. Otherwise valid stale pipeline timestamps
remain diagnosable even when the current source projection excludes that signal.

On recovery, a confirmed fault or a later unavailable read, optional
`last_blocked` retains the latest readable blocker observation and its original
assessment time. It is historical and does not assert that those blockers remain
active. Current `blockers` and `last_blocked` are mutually exclusive in each
entry, so there is at most one current-or-last preview per check. The separate
change history below has its own shared budget. Later polls cannot
rewrite earlier saved previews or re-stamp their source clocks/ages.

Human output prints `qualification_evidence`, `evidence_check`,
`evidence_blockers` and, when applicable, `last_evidence_blockers` with
`historical=true`. Timeout, cancellation and write-error results retain the last
assessment and readable history. A failed final read can therefore explain both
its current unavailability and the preceding evidence wait. These diagnostics
add no qualification gate, API read or notification send. Report fitting keeps
them within the existing 1 MiB envelope while disclosing historical omissions.
Older schema-version-1 reports can omit this section.

See [ADR-20261114080000](../adr/20261114080000-deployment-qualification-evidence.md).

### Explain verification wait time

Each qualification check now adds optional `progress`. These summaries survive
recovery and history eviction, so a passing report can explain which checks
spent time waiting for usable evidence.

| Field | Meaning |
|---|---|
| `first_observed_at` | First coordinator assessment, including an unavailable read. |
| `first_blocked_at` | First readable blocked observation, or null if none. |
| `first_passed_at` | First raw health-check pass, or null if none; stability may still be waiting. |
| `state_since` | When the current diagnostic state began. |
| `blockers_since` | When the current blocker set began; resets on a changed cause or re-entry after source loss, and is null outside `blocked`. |
| `observed_blocked_seconds` | Estimated sampled time attributed to readable blocked observations. |
| `observed_unavailable_seconds` | Estimated sampled time attributed to unavailable source observations. |

Each interval between assessments is attributed to its preceding diagnostic
state. No time before the first assessment or after the final assessment is
invented. Available monotonic clocks measure elapsed time; a regressing clock
cannot add negative time or count an interval twice. Unavailable intervals are
tracked separately from readable blockers. Confirmed faults retain their
existing health-timeline duration, and a fault never becomes a first pass.
These are polling estimates, not continuous availability or outage-onset claims.
Checks run together, so their waiting times can overlap and must not be summed
into an overall deployment delay. A later fault does not erase a first pass.

`qualification_evidence.history` records first assessments, diagnostic-state or
reason changes, source HTTP-status changes, and changes in blocked evidence.
For example, a pipeline check can move from a stale metric to missing integration
samples while remaining `unknown`; both causes remain visible after it passes.
Each event records `at`, `check`, `state`, optional `reason` /
`source_http_status`, and, when blocked, `cause_sha256` and a bounded `blockers`
preview. Events follow observation order. Historical item clocks and ages remain
those captured when the event was recorded, while current/last-readable previews
continue to follow the latest observation.

The cause fingerprint covers the complete inspected, reviewed blocker set,
including identities outside the four-item preview. It includes identities,
reasons, source state and freshness policy; routine timestamp/age updates do not
produce new events. A changed identity, reason or policy can produce an event
even when the visible preview stays the same. Source-read failure details remain
fixed reviewed categories rather than raw errors or provider responses.

History retains the latest **16 events** within **16 KiB** of conservatively
indented event-array JSON, independently of current previews and existing fault
histories. Limits live in `pkg/api/limits.go`. `total_events`, `retained_events`,
`dropped_events`, `truncated`, `event_limit`, `byte_limit` and `retained_bytes`
disclose retention. The first-blocked/first-passed timestamps and duration
summaries survive the eviction of their original events.

When fitting the complete 1 MiB report, advisory direct-notification history
yields first, then the oldest qualification events, then older primary detailed
fault snapshots. Current/last-readable previews, progress summaries, health
transitions and source summaries remain. `envelope_omitted_events` discloses
report-only qualification omissions separately from recorder drops; fitting a
saved view cannot permanently evict recorder history.

Human output adds `evidence_progress`, `qualification_history historical=true`
and `evidence_change` rows. Timeout, cancellation and report-write failure retain
the last assessed timing and history without a synthetic terminal observation.
These additions use the same source reads and health decisions. Older reports
can omit progress and history.

See [ADR-20261114090001](../adr/20261114090001-deployment-qualification-progress.md).

### Check assessment timeline

New schema-version-1 reports include an optional `timeline` object. Its
`transitions` array records each check's first assessment and subsequent state,
reason or serving-probe cause changes, with the poll's `attempt` and UTC `at` timestamp. Repeated
identical assessments do not add transitions. These are local assessment times,
not the source's event time or the actual onset of an outage.

The timeline keeps the latest **256 transitions across all three checks**.
`transition_limit`, `truncated` and `dropped_transitions` make that bound
explicit. Per-check summaries survive truncation and retain `first_fault_at`,
`first_fault_reason`, `last_recovery_at`, `last_observed_at`,
`observed_fault_seconds` and `unrecovered_fault`. A recovery requires a `passed`
assessment after a confirmed fault; unknown evidence leaves the fault unresolved.
A check that has only passed has no fault or recovery timestamp.

`observed_fault_seconds` is a cumulative polling estimate. Each interval is
assigned to its previous assessment: a degraded observation accrues time up to
the next observation; an unknown observation stops accumulation until degradation
is observed again. For example, degraded at 0 seconds, unknown at 2 seconds and
passed at 5 seconds records two fault seconds and recovery at 5 seconds.
This does not establish continuous downtime or count the unknown interval as
healthy. Durations use the process's monotonic clock when available and never
become negative. No duration is extrapolated beyond the last assessed poll.

Timeouts, handled cancellation and report-write errors retain the timeline in
the final in-memory/JSON result. Cancellation changes the overall outcome; it
does not invent a new health observation or recovery. Previously saved snapshots
are independent of later polling. Atomic report writes retain the last completed
file if a later write fails; always honor exit status and report timestamps.

Human output shows the retained transition count, truncation and per-check fault
summaries; JSON includes each transition. The timeline contains reviewed check
metadata and bounded probe-failure details, with no full historical source payloads.
The platform workflow summary also flags confirmed faults, including after a
passing final assessment, and points to the evidence artifact for details.
Older CLI reports and workflow fallback reports can omit `timeline`; absence
does not mean no faults occurred. Qualification uses the latest assessment and
any requested stability window, so a recovered fault can finish `passed` with
`had_confirmed_faults=true` and its earlier cause intact.

See [ADR-20261113180002](../adr/20261113180002-post-deployment-monitoring-timeline.md).

### Retained probe failure details

A degraded `serving_probes` transition can include `probe_failures`. Its `items`
identify the node, hashed `url_id`, coverage state/reason, recording evaluation
time, actual run clock and structured failure/retry diagnostics. Only validated
faults with a fresh post-boundary evaluation are eligible. A missing diagnostic
section stays unknown; it never becomes an HTTP status of zero or a passing
attempt. Raw URLs, response bodies and provider error text are excluded.

These are historical observations. A later recovery or unavailable API does not
rewrite earlier details, and an unknown/passing transition does not present an
old fault snapshot as current evidence. `at` is the verifier's observation time;
`evaluated_at` and `last_run_at` retain their source meanings. The source run may
precede the rollout boundary even when a fresh evaluation observes its fault;
these reports do not attribute an outage to the requested release.

The verifier fingerprints the semantic causes of **all** eligible failing pairs.
Changing a failure code, status, pair identity or diagnostic availability adds a
transition even while the check remains degraded with the same general reason.
Refreshing source timestamps alone does not. `cause_sha256` permits comparing
cause sets when a change is outside the retained preview; it is not a provider
receipt or proof of complete retained evidence.

Each snapshot retains at most **four** details and declares `total`, `omitted`
and `truncated`. Across retained transitions, details share a **64-item** and
**64 KiB** budget. The byte budget covers added transition properties in the
pretty JSON report, including their counts and fingerprints. It excludes the
small fixed retention summary and the existing state-transition fields.

`timeline.probe_evidence` declares these limits, retained counts/bytes,
`dropped_items`, `dropped_snapshots` and `truncated`. Item counts refer to
observations, not unique probes. Drops include preview/budget omissions and
details evicted with the oldest transitions. When even a snapshot's metadata
cannot fit, it is omitted and `dropped_snapshots` increases. Missing detail never
establishes zero faults. Rollover frees space for newer evidence while preserving
the existing first-fault summaries and disclosing lost detail. Human output
shows retained historical entries and truncation; older reports can omit these
optional fields. Health gates, notification behavior and exit codes are unchanged.

When the complete report needs more space after advisory history has yielded,
older detailed fault snapshots can be omitted from the report view.
`envelope_omitted_items` and `envelope_omitted_snapshots` disclose those omissions
separately from recorder drops. Check transitions, summaries and current source
snapshots remain available; the recorder can expose retained detail again in a
later, smaller report.

See [ADR-20261113200001](../adr/20261113200001-deployment-probe-failure-evidence.md).

The latest `serving` snapshot can also contain optional
[`probe_notifications`](serving-health.md#inspect-direct-notification-submissions).
It summarizes direct compute-node submissions with full channel counts and a
32-item preview. These are independent submission observations, outside the
three qualification checks and the retained probe-failure timeline. Their
failed, disabled or unknown outcomes do not change deployment pass/fail or
`had_confirmed_faults`. Malformed optional submission metadata is replaced by
an unknown section with `reason=metadata_inconsistent`; safe serving evidence
can still qualify. Absence on an older server remains unassessed.

### Retained notification fault details

Degraded `notification_pipeline` and `notification_receivers` transitions can
include optional `notification_failures`. These preserve the confirmed causes
seen at that poll after later recovery or unavailable evidence replaces the
latest `notifications` snapshot. Passing or unknown transitions do not attach
an old failure snapshot as current evidence.

Each item uses reviewed signal, receiver and integration names. Pipeline
details cover Alertmanager availability/reload values, dropped or failed
notifications, zero queue capacity, queue length/capacity with the busy threshold,
and integration failure counters. Receiver details identify failed completed
requests, or the validated `telemetry_missing` / `unexpected_group` assessment
with current collector and inventory observations. Coverage failures preserve
intent and collection evidence; missing per-group counters do not become zero
request failures. `expected` is absent when deployment intent is unknown.

`signals` retains named values, evidence quality, original `observed_at`,
`window_seconds` and `max_age_seconds`. A signal state of `healthy` describes
measurement quality; its positive failure count can still establish a fault.
Counter increases are extrapolated over the existing ten-minute window, which
can include events before `evidence_after`. Fresh post-boundary observations do
not attribute failures to the requested release or establish per-alert delivery
or human receipt. Historical successful-delivery values, raw addresses, provider
errors and arbitrary labels are excluded from these details.

`cause_sha256` covers all eligible fault identities/categories and relevant
expectation/threshold policy, including causes outside the preview. A changed
failing receiver, integration, signal or coverage reason adds a transition even
while the check stays degraded with the same general reason. Reordering sources,
refreshing timestamps or changing a positive counter within the same cause does
not. Retained values describe the first observation of that semantic cause;
the latest source snapshot holds subsequent values. Hashes are comparisons of
incomplete retained evidence, not delivery receipts or incident counts.

Each snapshot declares `total`, `omitted` and `truncated`, retaining at most
**four details**. Both notification checks share an independent **16-item** and
**8 KiB** budget for added, indented transition properties, including snapshot
metadata. `timeline.notification_evidence` exposes these limits, retained
items/bytes, dropped items/snapshots and truncation. Counts refer to observations,
not unique receivers. If metadata cannot fit, the whole detailed snapshot is
omitted and the drop is counted. This budget does not consume serving-detail
budgets. All check scopes share the existing 256-transition timeline; rollover
evicts the oldest transition and its details, returns budget space, and preserves
the first-fault/recovery summaries.

The complete report still fits the existing 1 MiB envelope. Fitting first trims
advisory direct-notification events, omitting their optional section with an
explicit limitation if needed. If primary evidence still exceeds the envelope,
it omits the oldest detailed fault snapshots across serving and notification
checks, preserving current source payloads, check transitions/summaries,
stability diagnostics and qualification. Each affected evidence summary declares
`envelope_omitted_items` and `envelope_omitted_snapshots`, adjusts retained counts
and sets `truncated=true`. These report-only omissions do not change permanent
drop counters, recorder contents or previously saved reports.

Human output prints `notification_evidence` and retained `notification_failures`,
including source clocks and window scope. Timeout, cancellation and report-write
failure retain the last captured evidence. The existing health checks decide
qualification and `had_confirmed_faults`; this history adds no new gate, API
read, probe or notification send. Older schema-version-1 reports can omit these
fields. Direct compute-node webhook/heartbeat history below remains advisory
and separate from this primary notification evidence.

See [ADR-20261114060000](../adr/20261114060000-deployment-notification-fault-evidence.md).

## Retain direct notification observations

New reports add optional `timeline.probe_notifications`, an advisory history of
the direct compute-node webhook and heartbeat observations already fetched
during verification. Human output prints `probe_notification_history` and its
retained entries. JSON keeps node/channel identity, outcome, fixed failure
code, `reason` and optional `evidence_reason` when available.

This history has no qualification vote. The same three checks, pass/fail
criteria, exit codes, polling and wait budgets still apply. The verifier can
finish `passed` with `had_confirmed_faults=false` and
`timeline.probe_notifications.had_submission_failures=true`. The latter means a
submission failure was observed in this advisory scope, including a historical
attempt; it is not a deployment-regression or fleet-wide availability claim.
The recorder makes no additional API reads, probes or notification sends, and
stops when verification finishes.

Each entry records `at` and verifier `attempt`, plus the original nullable
`evaluated_at`, `last_run_at`, `last_attempt_at` and `last_success_at` clocks.
`attempt_at_or_after_boundary` is null without an attempt clock, false for an
attempt before `evidence_after`, and true at or after that boundary. A current
evaluation can observe an old failed submission: the attempt is never re-stamped
or attributed to the requested release. Snapshot and inventory evidence must be
fresh and at or after the boundary; an older channel evaluation is recorded as
`unobserved` with `awaiting_post_deployment_evidence`, without a fabricated fault.

State/reason changes and genuinely newer failed submissions add entries.
Refreshing evaluation/run clocks or repeating unchanged successful outcomes
does not. A first successful completion at or after the boundary can add an
entry when earlier successful history predates it. Unknown evidence, a quiet
channel after credential rotation, explicit disablement and retirement from
intent never manufacture a successful submission.

An observed failed channel remains `unresolved_observed_failure=true` until a
later observation reports a successful completed submission with an attempt
clock strictly newer than its latest observed failure. Only that entry sets
`later_success_after_failure=true`. Equal or earlier success clocks do not
close it. `latest_observed_failure_at` remains available as historical context,
and `had_submission_failures` remains true after a later success. This compares
logical channel observations; it does not identify a destination credential,
establish same-provider recovery, or prove external receipt.

The source preview contains at most **32 channels**. Absence from a truncated
preview becomes `unobserved` with `not_in_preview`. When a complete preview
excludes a previously tracked channel, the reason is
`not_in_current_expectations`. An unavailable API, older server, invalid optional
metadata or unavailable inventory likewise produces an explicit unobserved
reason for previously tracked channels. None clears a pending failure. Raw URLs,
provider text and arbitrary labels never enter this history.

Retention is independent of the health-check timeline and serving fault details:

- Keep the latest **96 entries**, with at most **48 KiB** of charged event data.
  The byte budget conservatively includes pretty-JSON indentation and separators;
  the fixed history metadata is separate. Evict the oldest entries when either
  bound is reached, incrementing `dropped_events`.
- Track at most **2,000 node/channel identities** internally, matching the full
  serving-probe inventory bound. `tracked_channel_limit` and `tracked_channels`
  disclose that limit. Further identity observations increment
  `dropped_channel_observations`; this counts observations, not unique channels.
- `max_preview_omitted` retains the largest source-preview omission count.
  `incomplete_observations` counts polls with unavailable, partial, truncated or
  pre-boundary channel evidence. `source_state`, `source_reason` and
  `last_observed_at` describe the latest read.

`item_limit`, `byte_limit`, `retained_bytes`, the drop counters and `truncated`
disclose retention. `truncated=true` also covers omitted source-preview channels
or dropped identity observations. `unresolved_tracked_failures` covers only
tracked channels and survives event eviction; it is not the current fleet's
failure count. No retained entries, no observed failures, or no event truncation
cannot establish complete coverage. Inspect source quality, preview omissions
and tracking drops too. An initial report has no last-observation time and makes
no notification assessment.

The entire pretty-JSON report, including its final newline, remains within the
existing **1 MiB** envelope. Large degraded source snapshots can leave less than
48 KiB for advisory events. In that case, omit the oldest advisory entries from
the report view and disclose the current count in `envelope_omitted_events`,
with `truncated=true` and adjusted `retained_bytes`. Primary snapshots, health
transitions, serving fault details and qualification keep their existing values.
These view omissions do not count as permanent `dropped_events`: a later smaller
report can show internally retained entries again. If even fixed advisory
metadata cannot fit, omit the optional section and add the explicit limitation
`direct_notification_history_omitted_report_envelope`; human output prints an
unassessed envelope note. Such absence never means an empty successful history.

Earlier saved snapshots remain immutable as polling and buffer rollover continue.
Deadline, cancellation and report-write failure returns preserve collected
observations; cancellation before the first read leaves an empty history.
Older CLI reports can omit this optional section.

See [ADR-20261114030000](../adr/20261114030000-deployment-notification-history.md).

## Evidence freshness

Fresh means both post-boundary and no older than 120 seconds for source
observations and serving evaluations. A healthy serving probe must also have
run after the boundary and remain within its own configured result-age policy.
A fresh evaluation of an old run cannot pass. All expected pairs must pass;
missing or unconfigured intent and explicitly empty inventories stay unknown
for deployment qualification.

Receiver counters and the observation of each last-success gauge must be fresh
after the boundary. The success-clock **value** may be historical: it records
when the collector previously confirmed a successful completed request. A
quiet route can therefore pass with a freshly observed historical confirmation.
A route whose success has never been observed stays unknown. Verification does
not send a notification to manufacture that evidence. Follow the
[receiver qualification procedure](notification-delivery.md) when needed.
Ten-minute notification counter windows can include pre-deployment failures;
a detected fault does not establish that this release caused it. This is a
convergence check and does not establish sustained availability, per-alert
delivery, provider acceptance or human receipt.

Schema version 1 records the requested SHA, evidence boundary, start/update
times, wait budget, attempts, checks, the optional timeline, the latest validated
source evidence and limitations. `--report` first replaces prior evidence with unknown, then
atomically replaces an owner-readable file after each poll, including
cancellation when the process can handle the signal. JSON
stdout contains the final report. Raw serving URLs, query tokens, addresses,
arbitrary source labels and provider error text are excluded. Node names and
SHA-256 URL IDs retain their [existing meaning](serving-health.md).
Always honor the command's exit status and report update time. Invalid local
arguments or report I/O failures cannot guarantee a replacement file.

## Platform workflow and activation

Use [production monitoring qualification](monitoring-qualification.md) for
repeatable, test-bound operator attestations of notification receipts. Health
verification itself remains read-only and sends no qualification messages.

The canonical `.github/workflows/cd-platform.yml` job initializes an unknown
report, confirms control-plane and compute release convergence, and records
`MONITORING_NOT_BEFORE`. Existing health and acceptance gates run next. Monitoring
verification runs after the existing rollback step, so its own failure cannot
trigger rollback. A nonzero result fails the job and requires investigation.
Standalone control-plane, compute and unit repair workflows retain their
existing gates; use the CLI explicitly for those paths.

The wrapper comes from the workflow commit, while the remote CLI comes from the
requested signed release. It uses pinned host keys, disables PTY allocation,
passes the token through SSH stdin, reads the loopback API and checks the active
release pointer before and after collection. A changed pointer, missing token,
old CLI/API, invalid report or transport error leaves unknown evidence. It
never provisions tokens or bypasses API authorization.

Before activation:

1. Deploy the APIs and CLI described in [serving health](serving-health.md) and
   [notification health](notification-delivery.md#operator-api-and-cli).
2. Converge independent serving/receiver inventories, collectors and rules.
   Qualify expected receiver routes, including external watchdog delivery.
3. Configure `MONITORING_OPERATOR_TOKEN` as an environment secret in each target
   GitHub deployment environment. Use an authorized operator bearer with the
   existing admin scope, enrolled MFA and allowlist policy; verify it can read
   both endpoints through the control-plane loopback API. Keep it separate from
   customer canary credentials. Missing or expired credentials fail as unknown.
4. Review the first deployment report and its source clocks. The normal
   five-minute probe cadence should converge within the default 17-minute budget.

The job attempts an artifact upload with `if: always()`, named
`post-deployment-monitoring-<run_id>-<run_attempt>`, retained for 14 days. An
earlier gate failure retains `verification_not_started` with a null evidence
boundary if convergence was never reached. A killed runner or failed checkout
can prevent artifact creation/upload; absence of an artifact is never a pass.
Remote collection interruptions retain the initialized unknown fallback rather
than a partial remote stdout stream. Direct CLI use with `--report` preserves
each completed local poll.

Run local checks with `make post-deployment-monitoring-test` and
`go test ./cmd/gregalectl`. Tests use local HTTP fixtures and an SSH stub; they
do not contact production or send real notifications.

### Advisory command-cron admission timing

V2 profiles also expose `runtime_builds.command_cron_timing`, using existing
`schedd_command_cron_admission_lag_seconds_bucket`, `_count`, and
`schedd_command_cron_admission_lag_unknown_total` metrics. Single-box observes
`schedd`; control-plane also observes `schedd-compute`.

The 13 ordered signals are `le_1_seconds`, `le_5_seconds`, `le_15_seconds`,
`le_30_seconds`, `le_60_seconds`, `le_120_seconds`, `le_300_seconds`,
`le_600_seconds`, `le_1800_seconds`, `le_3600_seconds`, `le_inf`, `timed`, and
`unknown_timestamps`. Buckets are cumulative and overlap: do not sum them.
`le_inf` equals `timed`. Exact target deltas describe local observed returns,
not unique tasks. Summary counts classify signals across targets and retain
full-set coverage when target details are clipped.

Delay is returned occurrence `CreatedAt - ScheduledFor`, including catch-up
history and policy dispositions. It does not measure database commit time,
execution start or continuous eligibility. Unknown timestamps are explicitly
counted without fabricating zero lag. No timed observations establish no latency
claim. No percentile, threshold verdict or new deployment gate is inferred.

The histogram requires all buckets and count, coherent original clocks,
nondecreasing cumulative counts, and a possible interval distribution.
Comparisons retain the existing process, freshness, boundary and uptime-window
guards. Baselines, resets, changed processes, repeated samples and incomplete
histograms establish no timing delta. The unknown-timestamp counter remains
independent when histogram samples are missing or inconsistent.

The CLI, projected report and compact bounded history carry this advisory
section. Sticky history preserves observed signal categories, baselines and
uncertainty without summing overlapping poll deltas. New source reads yield to
existing loop reads under the shared deadline; protected HTTP poll counts and
verification gates are unchanged.

### Advisory HTTP cron delivery outcomes

V2 profiles expose `runtime_builds.http_cron_delivery` using existing
`schedd_cron_delivery_total` counters. Single-box observes `schedd`; control-plane
also observes `schedd-compute`. The five ordered outcomes are `success`, `retry`,
`app_error`, `failed`, and `dead_letter`.

These counters record settled HTTP delivery attempts, including scheduled fires
and manual runs. They are process-local observations, not unique schedules,
invocations or deliveries. A retry does not establish its cause or a final
result. Application-error classification (`app_error`) remains separate from
the producer's delivery-failure classifications (`failed`, `dead_letter`).
Success does not prove unique or exactly-once execution. No new failure-rate
threshold or deployment gate is inferred.

Summary counts classify signals across all inspected targets; exact bounded
target deltas count attempts. First samples are baselines, including historical
failures. Differences require unchanged sampled process proof, fresh original
sample clocks and coherent uptime advance. Resets, process changes, repeated
samples, uncertain windows and missing/stale evidence establish no delta.
Usable sibling outcomes and scheduler processes survive independent gaps.

The CLI, projected report, SDKs and compact history retain these categories.
Sticky progress preserves previously increased outcomes, baselines and source
gaps after later quiet observations and history eviction, without summing
overlapping poll deltas. New preview details yield first under the existing
shared response/report limits, preserving full-set summaries and explicit
omissions. Source reads yield to loop evidence within the shared deadline;
protected HTTP poll counts and verification gates are unchanged.

### Targeted runtime diagnostic views

Use `--runtime-diagnostics` with an explicit `--expected-role` to focus advisory
collection. Omit it to retain existing behavior. `all` selects supported groups,
`none` requests core evidence only, and a comma-separated list selects groups:

```sh
gregalectl obs verify-deployment \
  --release "$release" --after "$boundary" --expected-role single-box-v2 \
  --runtime-diagnostics http_cron_delivery --token-stdin --json
```

Build identity, readiness, process evidence and loop health remain collected.
The option does not change verification gates, extend the three-second API
budget or increase the 64 KiB response cap. Fewer advisory queries leave more
room for the requested target details; selected previews remain bounded.

V2 supports `builder_health`, `handoff_health`, `build_recovery_health`,
`app_task_health`, `command_cron_health`, `command_cron_outcomes`,
`http_cron_delivery`, `delayed_task_dispatch`, `command_cron_timing`, `app_task_results`,
`app_task_operations`, `post_capture_health`, `snapshot_prime_health` and
`preparation_health`. V1 has no advisory jobs; both `all` and `none` retain its core-only scope. Named
groups require v2.
Empty, duplicate, unknown or unsupported choices are rejected. `loop_health`
is always included and is not an advisory selector.

The API accepts the same comma-separated value in `diagnostics` on
`/v1/admin/obs/runtime-builds`. Node and Python preview SDKs expose the optional
string parameter. Explicit requests receive a `diagnostics` descriptor containing
canonical `selected` and `not_selected` lists. Omitted requests preserve the
previous response shape. `not_selected` describes intent, not source health;
a selected group can still be missing, stale or incomplete.

The CLI and projected artifact add `diagnostics_source=server` when the API
acknowledges the requested selection. On an older API's 400 response, the CLI
retries the runtime-build read once without `diagnostics`, caches that fallback
for the verifier run, and filters locally. Missing or inconsistent
acknowledgements also use `diagnostics_source=client`. Client filtering preserves
core evidence and requested details but does not establish reduced source load.
Normal polls retain their existing protected reads; the compatibility fallback
adds at most one runtime-build read on the first unsupported request.

For the SSH collector, set `MONITORING_RUNTIME_DIAGNOSTICS` alongside
`MONITORING_EXPECTED_ROLE`. The value is validated before SSH and passed as a
canonical CLI argument. An empty environment value leaves the default intact.
No operational activation is implied by adding this optional setting.

Selection intent stays inline in bounded history, including compacted events.
Past observed outcomes remain in progress when a group is later deselected.
Intentional omissions do not create source-gap flags, but they interrupt
continuous claims and sampled state transitions. Selected missing evidence
still produces uncertainty. A first selection after intentional omissions is
not itself a source failure.

### Advisory delayed-task dispatch outcomes

V2 profiles expose `runtime_builds.delayed_task_dispatch` from existing
`schedd_delayed_task_dispatch_total` counters. Single-box observes `schedd`;
control-plane also observes `schedd-compute`. Focus collection with
`--runtime-diagnostics delayed_task_dispatch`, or set
`MONITORING_RUNTIME_DIAGNOSTICS=delayed_task_dispatch` for the SSH collector.
V1 remains core-only and rejects the named group.

The four ordered outcomes are `success`, `retry`, `failed`, and `dead_letter`.
They describe settled process-local attempts, not unique tasks or deliveries.
Failed includes application errors and cannot alone identify a platform
incident. Retry does not establish its cause or a final result. Success does
not prove unique or exactly-once execution. Dead-letter remains a separate
producer disposition. No new health threshold or deployment gate is inferred.

First samples are baselines, including historical retries and failures.
Comparable points retain exact nonnegative differences, including zero, only
with unchanged sampled process proof, fresh original sample clocks and coherent
uptime advance. Resets, changed processes, repeated samples, uncertain windows
and missing/stale data establish no delta. Sibling outcomes and scheduler
processes can remain usable independently.

Summary counts classify signals across the full inspected target set; exact
bounded target deltas count attempts. The CLI, preview SDKs and projected
reports carry these fields. Sticky progress keeps previously increased outcome
categories, baselines and source gaps after later quiet observations and
history eviction, without summing overlapping poll differences. Selection
intent remains separate from source availability; intentionally unselected
collectors do not create false missing-evidence progress.

New source queries yield to loop evidence under the shared deadline. Target
previews yield first under the existing response/report caps, preserving
full-set coverage and explicit omissions. Protected HTTP read counts and
qualification behavior are unchanged.
