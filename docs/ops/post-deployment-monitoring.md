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
The SHA labels the report; these monitoring APIs do not prove runtime identity.
The platform workflow separately verifies fleet release convergence.

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
