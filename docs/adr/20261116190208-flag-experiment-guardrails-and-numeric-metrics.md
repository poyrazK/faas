# ADR 20261116190208 · Flag experiments: guardrails, numeric metrics and recommendations

- **Status:** accepted
- **Date:** 2026-11-16
- **Amends:** ADR-20261115163113 (flag experiments)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-20261115163113 compares the variants of a weighted flag rule by one
conversion event. Three gaps remain:

- **Nothing protects production while an experiment runs.** A variant that
  raises server errors or slows requests keeps receiving its share of
  customers until someone reads the results.
- **Only conversions can be measured.** A builder who cares about revenue per
  customer, or items per order, cannot use the `value` that `track()` already
  accepts and stores (`flag_conversion_daily.value_sum`).
- **Results are numbers, not a decision.** A builder or an agent has to work
  out what a p-value and a confidence interval mean for the flag.

The owner chose before this record:

1. **On a guardrail breach,** stop the experiment and send every customer of
   the rule to control.
2. **Guardrails** are server error rate and p95 latency, on by default for
   every experiment, read from request telemetry with no instrumentation.
   Owners may turn them off or change the tolerance when they create the
   experiment.
3. **Numeric metrics** compare the mean value per exposed customer, with
   outliers capped.
4. **On a winner,** Gregale only recommends. It says what to do and gives the
   command; only guardrail breaches act on their own.

## Decision

### 1. Guardrails

Every experiment carries two guardrails unless the create request turns them
off:

| Guardrail | Per-request outcome | Default tolerance |
|---|---|---|
| `server_error_rate` | status ≥ 500, platform refusals excluded | +0.5 percentage points |
| `p95_latency` | latency above the control arm's p95 | +2 percentage points of slow requests |

```json
{"guardrails": {"server_error_rate": {"max_increase": 0.005},
                "p95_latency": {"disabled": true}}}
```

**Unit.** The unit stays the customer. The reconciler already reads each
exposure window of request telemetry per customer and variant; it now also
adds that customer's requests, server errors, latency-tested requests and
slow requests to the unit row (`flag_experiment_units`). Only requests that
evaluated the experiment's rule count, because those are the requests the
variant can change.

**Statistic.** Each guardrail is a ratio of sums over customers (errors per
request), so its variance comes from the delta method over customers, not
from requests: requests of one customer are not independent. The comparison
is the normal mSPRT of ADR-20261115163113 with mixing standard deviation
equal to the tolerance and α = 0.01 (`FlagExperimentGuardrailAlpha`). A
guardrail is **breached** when, for any variant, the lower bound of the
always-valid confidence sequence on (variant − control) is above the
tolerance, and both arms have at least `FlagExperimentMinUnitsPerArm`
customers and `FlagExperimentGuardrailMinRequests` requests. Testing against
the tolerance, not against zero, keeps a large experiment from stopping on a
harmless change.

**p95 latency as a share.** A sequential test on a quantile is hard to make
always-valid; the share of requests above a fixed threshold is a proportion,
which the same test handles. The threshold is the control arm's p95 latency,
fixed once control has `FlagExperimentGuardrailMinRequests` requests, and
kept in `flag_experiments.latency_threshold_ms`. Before then no request is
latency-tested. Under no difference both arms see about 5% slow requests; a
slower variant sees more. Results also report each arm's p95 from a per-arm
latency histogram (`flag_experiment_latency`, 5% log buckets), which outlives
telemetry retention like the unit rows.

**Action.** On a breach the reconciler, in apid (the owner of flag
configuration), publishes a new flag configuration in which the rule serves
the control variant (`rule.value = control`), as actor
`system:flags-experiment-guardrail`, with the observed version as the
expected version. It then stops the experiment with
`stop_reason = guardrail_breach` and records the breach (guardrail, variant,
observed values, bounds, configuration version). A version conflict retries
on the next pass. When the rule is gone or no longer weighted, the experiment
stops without a configuration change and says so. The owner undoes the
rollback by restoring the previous configuration version. Every breach emits
the audit event `flags.experiment_guardrail_breached`.

Guardrails also act during a sample ratio mismatch: the action only moves
customers to control, the safe direction.

### 2. Numeric metrics

An experiment's `metric_kind` is `conversion` (the default) or `mean`. For
`mean`, a unit's value is the sum of the metric event's `value` the
customer sent from first exposure on. A customer who sent nothing counts as
0, so the metric is value per exposed customer, not per buyer.

- **Accumulation.** Daily rows age out with telemetry retention, so the
  reconciler adds each closed UTC day (before today) to the unit's
  `value_sum` once, and advances `flag_experiments.value_through_day`. Results
  add the open days on read. The exposure day counts when its daily row ends
  at or after the first exposure, the same rule as conversions.
- **Outliers.** Unit values are clamped to the 1st and 99th percentile of all
  units' values before the means and variances are taken (winsorizing), once
  there are `FlagExperimentMinUnitsPerArm` units.
- **Statistic.** The normal mSPRT on the difference of means. The mixing
  standard deviation is `mixing_lift` times the pooled standard deviation, so
  `mixing_lift` is a standardized effect size for means (default
  `FlagExperimentMixingEffect` = 0.1) and an absolute rate lift for
  conversions, as before.
- **Floors.** Each arm needs `FlagExperimentMinUnitsPerArm` customers and
  `FlagExperimentMinConversionsPerArm` customers with a non-zero value.

### 3. Recommendations and shipping

Results carry a recommendation:

| `action` | When |
|---|---|
| `rolled_back` | a guardrail stopped the experiment |
| `fix_allocation` | sample ratio mismatch |
| `ship_variant` | at least one variant is significantly better than control and none breaches; the best one is named |
| `keep_control` | every significant variant is worse |
| `keep_running` | otherwise, while running |
| `decide` | stopped by its owner with no significant variant |

The recommendation has a one-line summary and the CLI commands that carry it
out. Gregale never ships a winner on its own.

**Shipping.** `POST .../experiments/{key}/ship {"variant": "b"}` stops a
running experiment, if it is still running, and publishes the rule serving
that variant, through the same code path as the guardrail rollback, with the
caller as actor. `gregale flag experiment ship <key> <variant>` calls it.

**MCP.** Two read-only management tools, `gregale_list_flag_experiments` and
`gregale_get_flag_experiment_results`, return the same results and
recommendation, so an agent can report a decision without parsing
statistics.

## Consequences

- Experiments protect production by default. The cost is one guardrail check
  per running experiment per reconciler pass, on unit rows already in hand.
- A guardrail can roll back a variant the owner wanted to keep. The tolerance
  is configurable per experiment, the rollback is a normal configuration
  version that can be restored, and the breach is recorded and audited.
- Numeric results lag closed days only for the reconciler's accumulation;
  reads include the open days.
- A rule's behavior can now change without an owner request. This is the
  owner's explicit choice and is limited to sending customers to control.
