# ADR 20261119173702 · Subject units, layers and holdouts, richer metrics, bandits, metric-driven rollouts

- **Status:** accepted
- **Date:** 2026-11-19
- **Amends:** ADR-20261115163113 (flag experiments), ADR-20261116190208
  (guardrails and numeric metrics), ADR-20261119102741 (funnels, CUPED,
  cohorts), ADR-20261114150000 (flag usability: progressive rollouts)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Experiments count verified customers. An application that serves many end
users under one customer cannot experiment on them. Experiments on the same
users also overlap, so one experiment's change becomes noise in another's,
and there is no long-running group that sees none of them. Results report one
comparison per variant for the whole population. Every experiment splits
traffic evenly for its whole run, even when one variant is clearly losing.
Progressive rollouts advance on request health alone, never on what the
change does to the product metric it was meant to move.

The owner chose before this record:

1. **Subject units:** a unit is the pair (verified customer, hashed subject).
   The same person under two customers counts twice; nothing links end users
   across customers.
2. **Isolation:** named layers with mutually exclusive slices, plus one
   optional environment holdout that sees control of every experiment.
3. **Bandits:** Thompson sampling with a 10% floor per arm; results report
   best-arm probability and expected loss, not p-values.
4. **Metric-driven rollouts:** automatic both ways. A stage advances when its
   metric is not significantly worse and rolls back when it is.

## Decision

### 1. Subject units

An experiment has `unit` `customer` (default) or `subject`. A subject
experiment needs a weighted rule with `rollout_unit: subject`, so the SDK
already allocates by (customer, subject).

Request evidence carries no subject, so exposures come from the SDK. A rule
under a running subject experiment carries the experiment's key in its
bundle (`experiment.key`, §2). SDKs with capability `subject-exposures-v1`
report one exposure per flag, customer, subject and variant, at most once an
hour per process (a bounded in-memory set of 10,000 entries), on
`POST /v1/runtime/flags/events` as `exposures`. An exposure names the flag,
rule, variant, customer and subject; apid keeps only the subject's hash
(`sha256(environment_id NUL subject)`, as product analytics does). Exposures
do not count against the event quota. A request may carry at most 100
(and then needs no events). apid keeps exposures of the account's active
customers that match a running subject experiment, only while product
analytics is on, and returns how many it kept as `exposures`.

Units stay in `flag_experiment_units`. A subject unit's `customer_id` is a
UUID derived from the customer and the subject hash (the first 16 bytes of
`sha256("unit" NUL customer NUL hash)` with version 8 bits), so the existing
key, arms, horizon and CUPED queries count it unchanged. New columns
`unit_customer_id` and `subject_hash` keep the real customer and the hash.

Request telemetry and `flag_conversion_daily` are per customer, so subject
experiments read metrics from product analytics (`analytics_events` by
subject hash) and need it on. They support `conversion`, `funnel` and
`retention` metrics, not `mean`. Guardrails need per-unit request counts and
are off for subject experiments; asking for them is refused. A subject's
conversions match analytics events by subject hash across the environment,
not only its customer's. CUPED is not applied to subject experiments
(`cuped.reason` `not_supported`): the pre-period covariate is per customer.

### 2. Layers and the environment holdout

An environment has at most `FlagLayersMaxPerEnvironment` (10) layers, each a
key and a seed. An experiment may join a layer with `traffic` basis points.
apid gives it the lowest free contiguous slice `[from, to)` of the layer when
it starts and frees the slice when it stops. Experiments in one layer never
share a unit.

The environment holdout is a seed and `holdout` basis points (at most
2,000, i.e. 20%). It is per customer: held-out customers see control in every
experiment, subject experiments included.

When an experiment starts, apid publishes a flag configuration whose rule
carries `experiment`: its key, control, unit, and layer slice when it has
one. Stopping, shipping or deleting the experiment publishes the rule
without it. Evaluation of a rule with `experiment`, after its customer,
group, attribute and subject constraints match:

1. With a layer: the unit's layer bucket,
   `SHA-256(layer seed NUL "layer" NUL customer [NUL subject])` modulo 10000,
   must be in `[from, to)`. Otherwise the rule does not match and evaluation
   continues with the next rule.
2. With a holdout: a customer whose holdout bucket,
   `SHA-256(holdout seed NUL "holdout" NUL customer)` modulo 10000, is below
   the holdout gets the experiment's control with reason `holdout`.
3. Otherwise the rule's rollout and weighted variants apply as before.

Exposures count only `rule_match` decisions: the unit queries leave out
evidence with reason `holdout`, so held-out customers never enter results. SDKs need capability `experiment-layers-v1` for a bundle
that has a rule with `experiment`; older SDKs get
`426 flags_sdk_capability_required`, so they never serve a layered or
held-out rule to everyone.

The holdout report compares held-out customers with all other customers
that sent any analytics event in a range, on one metric event: conversion
rates, their difference and a 95% interval. Changing the holdout seed or
size changes who is held out, so it is refused while an experiment runs.

### 3. Ratio and percentile metrics and segment results

- `ratio`: the sum of a numerator event's values over the sum of a
  denominator event's values per variant (revenue per order). Its variance
  uses the delta method over customers. Customer units only; it reads
  `flag_conversion_daily`.
- `percentile`: the variant's p-th percentile (50 to 99) of per-customer
  metric value sums, compared with control by a bootstrap interval (1,000
  resamples, seeded by the experiment ID) when results are read, over at
  most 50,000 units per arm. It has no sequential guarantee: results mark it
  `fixed_horizon` and keep no running minimum p-value.
- CUPED is not applied to ratio or percentile metrics (`not_supported`).
- Segments: results can be split by a cohort (current members against
  everyone else). Each segment is compared separately; p-values
  across segments and variants are adjusted with Benjamini–Hochberg at 5%.
  Segments are read-only analysis and never drive the recommendation.

### 4. Bandits

An experiment has `allocation` `fixed` (default) or `bandit`. A bandit needs
a binary metric (`conversion`, `funnel`, `retention`) and its rule must be
the flag's only weighted rule, because variant weights belong to the flag.

Each reconciler pass draws 10,000 samples from each arm's
Beta(1 + converted, 1 + exposed − converted) posterior (seeded by the
experiment ID and pass time) and stores each arm's best-arm probability and
expected loss. At most hourly, once every arm has the minimum units, it sets
each arm's weight to its probability of being best, raised to at least 10%
(`ExperimentBanditFloorBasisPoints`), and publishes the new variant weights
when they moved by at least 5 points in total. The step runs after the
guardrail check, so a failed telemetry read does not stop it. Units whose bucket moves to another variant become crossed and leave
the results; which units move depends only on their hash, not their outcome,
so this loses data but adds no bias.

Bandit results report each arm's best-arm probability and expected loss
(the expected conversion rate given up by choosing it). The sample ratio
check and p-values do not apply. The recommendation is `ship_variant` when
an arm's best-arm probability is at least 95% and its expected loss below
0.1 percentage points. Guardrails apply as for fixed experiments.

### 5. Metric-driven rollouts

A progressive rollout's plan may name a `metric`: `event`, `max_decrease`
(absolute conversion rate, default 0.01) and `min_units` per group (default
100). Customer-unit rules only.

For the current stage, units that got the rule since the stage's
configuration version are the treatment; units that evaluated the flag with
another outcome in the same window are the comparison. A unit converts when it
sends the metric event at or after its first exposure in the window (from
`flag_conversion_daily`). Once the stage's full window has passed, the
auto-advancer checks the metric before the request-health gate:

- rolls back when the mSPRT 95% interval on treatment minus comparison lies
  wholly below `-max_decrease`: it publishes the rule with rollout 0 and no
  progression, and audits `flags.rollout_auto_rolled_back` with the numbers;
- lets the stage go on to the request-health gate, which advances it as
  before, only when both groups have `min_units` and the interval's upper
  bound is above `-max_decrease` (not significantly worse);
- otherwise holds.

A rule without a metric advances on health alone, as before.

### 6. Surfaces

API: experiment create gains `unit`, `layer`, `traffic`, `allocation`, and
`metric_kind` `ratio` (`denominator_event`) and `percentile` (`percentile`);
results gain `segments`, `bandit` and per-variant `best_probability` and
`expected_loss`. Routes for layers and the holdout under
`/v1/projects/{slug}/environments/{environment}/experiment-layers` and
`.../experiment-holdout`. Rule progression gains `metric`. CLI: `gregale
flag experiment create --unit subject --layer L --traffic 30 --allocation
bandit`, `gregale flag experiment layer list|create|delete`,
`gregale flag experiment holdout get|set|report`, `gregale flag experiment
result get --segment KEY` (a cohort). A progression's `metric` is set in the
flag configuration (`gregale flag apply`). MCP: the existing read-only
results tool returns the new fields, with guidance for bandit and
fixed-horizon results.

## Consequences

- End-user experiments work in single-tenant apps, at the cost of SDK
  exposure reporting and product analytics.
- Layered and held-out rules need new SDKs; old SDKs fail closed with 426.
- Bandits spend less traffic on losing variants but give no always-valid
  test; teams that need one keep `fixed`.
- Percentile and segment results are labelled as fixed-horizon or
  exploratory and never ship a variant on their own.
- A rollout can now roll itself back on its product metric, so a stage's
  metric choice is a production decision, audited like a manual one.
