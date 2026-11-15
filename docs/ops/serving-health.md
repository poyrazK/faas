# Operator serving-probe health

```bash
gregalectl obs serving-health
gregalectl obs serving-health --json
```

GET `/v1/admin/obs/serving-health` and the CLI report coverage of expected
node/URL pairs from the shared
[serving-probe fleet policy](serving-probe.md#coverage-from-deployment-intent).
They use the existing operator read authorization, MFA and operator allowlist.
The endpoint accepts no query parameters and performs no writes, direct HTTP
probes, notification sends or changes to retained incident history.

## Read the result

`inventory` describes the independent control-plane intent. A missing, stale,
ambiguous or inconsistent inventory leaves `configured`, `expected_pairs` and
aggregate `counts` unknown, with an empty probe list. It cannot establish zero
expected probes or healthy coverage. A fresh inventory explicitly marked
unconfigured has `configured=false`, but the overall assessment remains
partial. An explicitly configured empty fleet has zero counts and overall
`not_applicable`; it establishes no serving availability claim.

For configured inventory, every expected pair appears in `probes`, ordered by
stable node name and URL ID. The count summary covers the entire expected set,
including unknown evaluations. Reporting counts pairs with a known result;
missing and unknown pairs never count as healthy. One healthy node or URL
cannot satisfy another expectation.

| Probe state | Meaning |
|---|---|
| `healthy` | The current coverage evaluation passed content, latency, freshness and uniqueness checks. |
| `missing` | An expected pair had no result in the evaluated coverage snapshot. |
| `stale` | A result or node scrape is stale, or the probe loop's run clock exceeds policy. |
| `invalid_telemetry` | Telemetry is ambiguous, duplicated, incomplete or invalid according to the coverage rules. |
| `content_disabled` | The probe is configured without the required response-body check. |
| `budget_mismatch` | Its reported first-byte budget differs from deployment intent. |
| `failing` | The expected probe failed response validation. |
| `unknown` | Evaluation evidence is missing, stale, invalid or inconsistent with the current policy. |

Known faults produce overall `degraded`, even when another expected pair is
unknown. Otherwise unknown evaluations produce `partial`, and complete passing
coverage produces `healthy`. The CLI preserves `reporting=unknown` and unknown
evaluation/run times rather than substituting false or an epoch-zero time.
A successful read exits normally for degraded or partial assessments; inspect
the JSON states when making operational decisions.

## Inspect failure and retry diagnostics

Each probe can include an optional `diagnostics` section for the last evaluated
run. It does not change the coverage assessment. Human output shows it below
each node/URL pair; deployment reports retain it in their latest serving snapshot
and can preserve bounded fault details in the
[deployment timeline](post-deployment-monitoring.md#retained-probe-failure-details).

`state=available` requires four observations from the same current coverage
evaluation: final failure code, first failure code, attempt count and HTTP status.
Rules bind each field to the discovered compute target, require a unique series
and match its scrape timestamp to the result. The API also checks the run clock,
code ranges and consistency with coverage.

| Metric code | API failure code | Meaning |
|---|---|---|
| 0 | `none` | The configured checks passed on this attempt. |
| 1 | `dns` | A typed name-resolution error. |
| 2 | `connection` | A typed connection or network error. |
| 3 | `tls` | A typed TLS error, including certificate validation. |
| 4 | `timeout` | A request/read timeout or body completion over budget. |
| 5 | `http_status` | A non-2xx HTTP response, including a refused redirect. |
| 6 | `latency` | First-byte latency exceeded the configured budget. |
| 7 | `body_mismatch` | The exact expected body did not match. |
| 8 | `unknown` | An observed failure could not be classified safely. |

`first_failure_code` describes the first attempt of this run; `failure_code`
describes its final attempt. A first-attempt success has `attempts=1` and both
codes `none`. A retried run has `attempts=2` and a non-`none` first code.
`recovered_on_retry=true` means the second attempt passed. That remains a passing
probe and does not mark a confirmed deployment-verification fault; it makes the
intermittent failure visible without changing the existing retry policy.

`http_status=0` means no response headers were observed. HTTP 200 can accompany
a latency, body or subsequent read failure. Codes identify the observed category,
not the responsible service or network hop. Classification never parses provider
exception strings to guess a cause.

`state=unknown` means evidence is missing, inconsistent, unavailable or from
another evaluation. Numeric values and retry recovery remain null; codes are
absent. This differs from `state=available, failure_code=unknown`, a real
unclassified failed attempt. Older probes/rules produce unknown diagnostics;
older APIs can omit the section. Human CLI output labels an omitted section
`server_does_not_report_diagnostics`, without inventing zero failures.

The two extra strict queries share the existing three-second budget. Four
scalars per expected pair fit the 4,096-series bound at the full 1,000-pair fleet.
A diagnostic query failure leaves coverage assessment intact. Updated Node and
Python SDKs accept older responses without the section.

See [ADR-20261113190000](../adr/20261113190000-serving-probe-failure-diagnostics.md).

## Inspect direct notification submissions

The response adds optional `probe_notifications` for the compute-node probe's
direct webhook and heartbeat path. Human output prints the same section after
the serving probes. It uses the [notification recordings](serving-probe.md#warnings-from-completed-notification-failures)
without sending a message, contacting a provider or reading its credentials.

Each unique expected node contributes two channel slots, `heartbeat` and
`webhook`, regardless of how many probe URLs that node has. The optional
heartbeat can be explicitly disabled. A channel reports:

| State | Meaning |
| --- | --- |
| `disabled` | A fresh source reports the channel disabled with no submission history. |
| `unknown` | Evidence is missing, stale, unavailable, inconsistent, or the recording reports no usable outcome. |
| `last_submission_succeeded` | Its last completed HTTP submission passed the transport checks. |
| `last_submission_failed` | Its last completed HTTP submission failed, with a fixed failure code. |

For known outcomes, `evaluated_at` is the recording evaluation clock and
`last_run_at` is the original probe-completion clock. The API requires a fresh
evaluation, a run within the node's result-age policy and matching evaluation
timestamps on all used observations. `last_attempt_at` and `last_success_at`
remain historical completion clocks. A quiet run never advances them; an old
successful submission does not prove current reachability, provider processing,
external watchdog receipt or human acknowledgement. The CLI's delivery/receipt
limitations still apply after submission evidence becomes available.

Failures use `dns`, `connection`, `tls`, `timeout`, `http_status` or `unknown`.
The last category is an observed unclassified failure; `state=unknown` instead
means the outcome cannot be established. Disabled or unknown channels have null
configuration/history fields as appropriate and no fabricated failure code or
epoch-zero success. A fresh recording of unknown evidence can still provide
`evaluated_at`; stale or missing recordings do not acquire a new timestamp.

For `reason=recording_unknown`, optional `evidence_reason` explains the current
notification evidence:

| Evidence reason | Meaning |
| --- | --- |
| `no_submission_yet` | A fresh enabled channel reports no attempted request and no retained history, including after credential rotation. Quiet webhooks can remain in this state. |
| `telemetry_missing` | A fresh probe source lacks required notification fields. Missing optional success history on a failed submission is allowed. |
| `telemetry_invalid` | Notification fields are duplicated, stale relative to the source scrape, malformed or contradictory. |
| `source_unavailable` | The discovered probe source, run clock or node timing policy cannot establish usable evidence. |

The coarse `reason` remains compatible with older clients. Older rules omit
the detail and retain `recording_unknown`. Query failures remain `query_failed`;
absent, stale or mismatched recordings retain `recording_missing` or
`recording_not_current` as applicable. Detailed evidence requires a current
recording evaluation; channel configuration and submission history remain null.
An evaluated source-unavailable
reason describes the monitoring assessment, without inventing a run clock.
Missing or invalid telemetry sustained for five minutes raises
[`FaasServingProbeNotificationTelemetryDegraded`](../runbooks/FaasServingProbeNotificationTelemetryDegraded.md).
Valid channels without history and explicitly disabled channels do not raise
that warning. Source and inventory failures remain covered by their existing
warnings. A telemetry warning clearing does not establish a completed success.

The section's `counts` cover **all** expected channels, up to 2,000, independently
of the preview. `state=available` means all channel outcomes are known, even if
some failed; `partial` means some are unknown; `unknown` means all outcomes or
the inventory are unavailable. An explicitly configured empty inventory is
`not_applicable`. Unknown inventory has null `expected_channels` and `counts`.

`channels` retains at most **32** entries, ordered failed first, then unknown,
successful and disabled; ties use node/channel order. `limit`, `omitted` and
`truncated` disclose the bound. An omitted channel is not evidence of success;
use the full counts and the direct node lookup below when the preview is
truncated. The bounded preview keeps the maximum deployment evidence report
inside its existing response envelope.

Eight notification queries run concurrently under the existing shared
three-second deadline, for fourteen serving-health queries in total. Each
returns at most 4,000 series at the maximum fleet, below the 4,096-series
transport limit. Raw URLs, scrape addresses, arbitrary
labels and provider text are excluded; malformed or partial queries become
unknown evidence without changing serving coverage. Optional metadata is
validated before either human or JSON CLI output. The generated Node and
Python SDKs support this section; an older server can omit it, which the CLI
labels `server_does_not_report_probe_notifications`.

Submission evidence is separate from the serving assessment and the existing
deployment-verification checks. A failed, quiet, disabled or unknown channel
does not add a serving qualification gate. Deployment reports retain the safe
preview in their latest serving snapshot; malformed optional metadata is
replaced by an unknown section without copying unsafe values or blocking
otherwise valid serving evidence. The standalone CLI rejects malformed responses.
An alert can stop firing because evidence disappeared or a credential changed;
inspect state and completion clocks before treating resolution as recovery.

See [ADR-20261113233000](../adr/20261113233000-serving-probe-notification-operator-evidence.md).

## Inspect one node beyond the fleet preview

```bash
gregalectl obs node fsn-2
gregalectl obs node fsn-2 --json
```

The node inspector includes `probe_notifications` from GET
`/v1/admin/obs/nodes/fsn-2/probe-notifications`. This direct lookup returns both
channel slots for an expected node, including a node omitted from the fleet's
32-channel preview. It checks the full independent inventory first, then reads
notification recordings filtered to that exact node. It uses the same operator
authorization, MFA and allowlist as serving health and accepts no query parameters.

| `expected` | Meaning |
| --- | --- |
| `null` | Inventory is unavailable, stale or unconfigured; expectation is unknown and `channels` is empty. |
| `false` | Fresh configured monitoring intent excludes this node; state is `not_applicable` and `channels` is empty. This says nothing about whether the node exists in the registry. |
| `true` | Monitoring intent includes this node; both `heartbeat` and `webhook` slots are returned, even when their evidence is unknown. |

`state=available` describes complete evidence, including known failed
submissions. `partial` and `unknown` preserve incomplete outcomes. Read the
channel states and completion clocks using the historical-submission semantics
above; a previous success does not establish current provider health or receipt.
The node response also carries the inventory observation time, evaluation time,
freshness limit and the node's result-age policy.

Two inventory queries and, only for an expected node, eight filtered channel
queries share one evaluation time and one three-second deadline. Scoped vectors
are bounded to four rows each; foreign-node or malformed observations become
unknown. The lookup performs no writes or notification sends. The CLI treats
this additional read as optional: an older server, unavailable endpoint or
invalid response produces a fixed unknown note while the existing node detail
remains usable. It validates the node identity and metadata before human or
JSON output and refuses redirects for this read.

See [ADR-20261114010000](../adr/20261114010000-node-probe-notification-lookup.md).
Detailed unknown reasons and telemetry warnings are specified in
[ADR-20261114020000](../adr/20261114020000-serving-probe-notification-telemetry.md).

## Match a URL without exposing its credentials

`url_id` is lowercase SHA-256 of the **exact configured UTF-8 URL bytes**.
Different paths, query parameters, percent encoding or trailing slashes have
different identities. Raw URLs, query tokens, response bodies, scrape targets,
source labels and upstream error text are excluded from both API output and
CLI diagnostics. Node names are validated against the fleet policy's stable
name syntax.

To identify a configured URL locally without putting it in shell history:

```bash
python3 -c 'import getpass, hashlib; print(hashlib.sha256(getpass.getpass("Exact probe URL: ").encode("utf-8")).hexdigest())'
```

The same URL ID can appear on multiple nodes; the node/URL-ID pair is the
identity. The fleet serving-health endpoint and CLI accept no URL or node filter.

## Evidence and timing

Four strict Prometheus queries share one evaluation time and a three-second
budget. Raw inventory values and source timestamps must form one coherent,
fresh control-plane snapshot. The read checks configured state, declared count,
unique expected pairs, complete per-node timing policy and the inventory node's
scrape health. The result is bounded to the inventory's existing 1,000 pairs;
an over-bound or partial inventory cannot be truncated into a healthy result.

Serving states come from the deployed `faas_serving_probe_coverage` recording
rules. Their semantics are trusted: those rules bind compute results to
discovered scrape identities, check the probe's own run clock, reject
duplicates and assess validation settings and results. The operator read
requires evaluations no older than 120 seconds. Recorded budget and maximum
age must match current independent intent and have the same evaluation time
as the pair's state. A new policy awaiting evaluation therefore stays unknown.

`evaluated_at` is the recording sample time, not a new HTTP probe time.
`last_run_at` is the probe loop clock. A previously passing evaluation cannot
hold green after that run ages beyond `max_result_age_seconds`. Missing,
stopped or inconsistent recording data stays unknown. Retired pairs outside
current inventory are excluded, and no cached response supplies missing data.

This is point-in-time coverage evidence. It does not prove a fresh request
from the operator's location, notification delivery, or receipt by an external
watchdog. Use [notification health](notification-delivery.md#operator-api-and-cli)
and the established external-monitor qualification procedures separately.

## Rollout and verification

Converge the probe helper's notification fields before enabling the telemetry
warning rules. A fresh older helper without these fields is classified as
`telemetry_missing` and will warn after five minutes. Older rules without the
detailed recording remain usable with the coarse unknown reason.

Use [post-deployment monitoring verification](post-deployment-monitoring.md)
to wait for post-rollout serving and notification evidence and retain a
release-linked report.

Deploy the existing inventory role and coverage rules, then the API and CLI.
With the shared fleet policy configured on both control-plane and compute
convergence, allow a probe run, scrape and rule evaluation to complete. Compare
`inventory.expected_pairs`, `counts` and the per-pair states. Investigate
`policy_not_evaluated` by checking convergence and rule evaluation; investigate
`coverage_stale` by checking the coverage group before relying on its last
passing value.

Real Prometheus fixtures pin query semantics and deployed coverage states.
Backend and CLI tests cover states, source freshness, policy transitions,
unknown versus empty inventory, redaction, cancellation and the full 1,000-pair
bound. Protected endpoint tests cover authorization, query refusal and
read-only history. Preview SDK models preserve null evidence fields.
