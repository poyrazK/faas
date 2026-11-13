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
identity. No URL or node filter is accepted by the endpoint or CLI.

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
