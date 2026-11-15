# ADR 20261115163113 · Flag experiments: conversion events and sequential results

- **Status:** accepted
- **Date:** 2026-11-15
- **Amends:** ADR-377 (customer-aware application feature flags),
  ADR-20261114150000 (flags usability)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Gregale Flags allocates customers to variants and records every decision in
request telemetry (`request_telemetry.flag_evidence`, next to the verified
customer in `platform_tenant_id`). A builder can see who got which variant,
but not whether a variant worked: nothing records what the customer did
afterwards, and nothing compares variants. Builders run that comparison in a
second product (Statsig, PostHog, LaunchDarkly Experimentation) and wire the
same allocation twice.

Three choices were made by the owner before this record:

1. **Statistics:** sequential, always-valid results, so a builder may look at
   any time without inflating false positives.
2. **Retention:** conversion events follow the plan's request telemetry
   retention (Free off, Hobby 3 d, Pro 7 d, Scale 14 d,
   `DebugTelemetryRetentionDays`).
3. **Pricing:** included in the plan with a monthly event quota in
   `pkg/api/limits.go`; events over quota are dropped and counted, never
   billed.

## Decision

### 1. An experiment is a flag rule plus a metric

```json
{"key": "checkout-v2", "flag": "checkout", "rule_id": "everyone-percent",
 "control": "legacy", "metric": {"event": "order_completed", "kind": "conversion"},
 "status": "running", "started_at": "2026-11-15T16:00:00Z"}
```

- The allocation is the flag's own: a variant flag's **weighted rule** (a
  rule without a fixed `value`, allocating by the flag's variant weights).
  Every customer that rule allocates carries its rule ID and variant in
  evidence, so every arm is observable. A boolean flag's percentage rule is
  not accepted: customers it leaves out get reason `default` with no rule ID,
  the same as customers the rule never targeted, so it has no clean control
  arm. A boolean A/B test is a two-variant flag (`off`, `on`). An experiment
  never changes the configuration; it reads decisions from that rule.
- The **unit** is the verified customer (platform tenant), the identity
  allocation is sticky on and the one request telemetry carries. Rules that
  allocate by subject are refused. Subject units need subject IDs in evidence
  and are a later decision (owner choice: customers first).
- `control` names the baseline variant.
- A unit is **exposed** on its first request whose evidence carries a
  decision from the experiment's rule. Evidence reaches request telemetry only
  when the app returns it (`flags.responseEvidence()` in the response header,
  ADR-377), so the experiment guide says so.
- `metric.kind` is `conversion` (did the unit send the event at least once
  after its first exposure) in this record. Value metrics (sum, mean) come
  later; the event already carries a value for them.
- `status` is `draft`, `running` or `stopped`. One running experiment per
  flag rule. Stopping freezes the results; it does not change the flag.
- At most `FlagExperimentsMaxRunning` running experiments per environment.

### 2. Conversion events from the runtime SDKs

`flags.track('order_completed', {value?})` inside `runRequest` (Node),
`flags.track(...)` (Python, Go) records an event for the request's verified
customer. The SDK never accepts a customer from the caller: the unit is the
one the gateway attached to the request, as for evaluation. An event outside
a request with a verified customer is dropped and counted by the SDK.

The SDK batches events (at most 100 per call, at least every 5 s) to
`POST /v1/runtime/flags/events` with the same workload identity token as
`GET /v1/runtime/flags` (audience `gregale:flags`). apid derives account, app
and environment from the token, never from the body.

Event names match `^[a-z][a-z0-9_.-]{0,63}$`; at most
`FlagEventNamesMaxPerEnvironment` distinct names per environment.

### 3. Storage follows request telemetry retention

apid does not keep raw events. It folds each accepted event into one row per
(environment, event name, customer, UTC day): `count`, `value_sum`,
`first_at`, `last_at`. `flag_conversion_daily` lives as long as the plan's
request telemetry and is swept by the same retention loop.

Because telemetry retention is shorter than most experiments, apid also
keeps one row per experiment and unit, `flag_experiment_units`:
`variant`, `first_exposed_at`, `converted_at`. A reconciler (every 5
minutes) adds units from new request telemetry evidence for the experiment's
flag and rule, and marks conversions from
`flag_conversion_daily` that happened at or after `first_exposed_at`. These
rows live until the experiment is deleted, so results cover the whole
experiment while raw telemetry and daily events age out on the plan's
schedule. A unit's variant is the one of its first exposure; later exposures
to another variant (a rule edit) mark the unit `crossed` and exclude it.

### 4. Quota

`FlagEventsPerMonth` per plan: Free 0 (request telemetry is off), Hobby
100,000, Pro 1,000,000, Scale 10,000,000. Events over quota are dropped
before storage; the runtime endpoint answers with the accepted and dropped
counts and the account's usage, and the results report says when events were
dropped in the experiment's window. No overage is billed. The numbers are
provisional until the financial model carries them (owner action).

### 5. Results: always-valid sequential inference

For each non-control variant against control, `GET .../experiments/{key}/results`
and `gregale flag experiment results` report: exposed units, converted units,
conversion rate, absolute and relative lift, a 95% **confidence sequence**
for the difference in rates, and an always-valid p-value.

The test is the mixture sequential probability ratio test (mSPRT,
Johari et al. 2017) on the difference of two proportions with a normal
approximation and a normal mixing distribution: with `Δ` the observed
difference, `V` its estimated variance and `τ²` the mixing variance,

    Λ = sqrt(V / (V + τ²)) · exp(τ² Δ² / (2 V (V + τ²)))

the always-valid p-value is the running minimum of `min(1, 1/Λ)` (the
reconciler stores the minimum per arm after each pass), and the confidence
sequence is `Δ ± sqrt((V (V + τ²) / τ²) · (ln((V + τ²)/V) + 2 ln(1/α)))`.
`τ` is fixed per experiment at creation (`mixing_lift`, default
`FlagExperimentMixingLift` = 0.02, a 2-point absolute lift), so it is never
tuned after looking. A variant is reported `significant` when the
p-value is below 0.05; results also say `insufficient_data` until each arm has
`FlagExperimentMinUnitsPerArm` exposed units and 10 conversions.

A **sample ratio mismatch** check (chi-square on exposed units against the
rule's configured weights, p < 0.001) is reported first; results with a
mismatch say so and are not called significant.

### 6. Surfaces

- API under `/v1/projects/{project}/environments/{environment}/flags/experiments`:
  list, create, get, stop, delete, results.
- CLI: `gregale flag experiment create|list|get|stop|delete|results`.
- Audit events `flags.experiment_created`, `flags.experiment_stopped`.
- Everything stays behind `customer_flags` (ADR-1700).

## Consequences

- New tables `flag_experiments`, `flag_experiment_units`,
  `flag_conversion_daily`; one expand migration.
- New limits in `pkg/api/limits.go`: `FlagEventsPerMonth` (per plan),
  `FlagExperimentsMaxRunning`, `FlagEventNamesMaxPerEnvironment`,
  `FlagExperimentMinUnitsPerArm`, `FlagExperimentMixingLift`, runtime event
  batch bounds.
- The SDKs gain `track` and the capability `track-v1`; older SDKs are
  unaffected.
- Experiments with customer units suit B2B products and per-account
  launches. Consumer products whose users are not platform customers need
  subject units, which is follow-up work.
- Results describe correlation between allocation and conversion within the
  window; they do not attribute revenue and do not replace billing data.
