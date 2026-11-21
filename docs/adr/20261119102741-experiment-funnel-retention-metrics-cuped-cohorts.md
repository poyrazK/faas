# ADR 20261119102741 · Experiments on funnels and retention, CUPED, and cohort targeting

- **Status:** accepted
- **Date:** 2026-11-19
- **Amends:** ADR-20261115163113 (flag experiments), ADR-20261116190208
  (guardrails and numeric metrics), ADR-20261117084505 (product analytics),
  ADR-20261114150000 (flag attributes)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Experiments measure one `track()` event: whether a customer sent it after
exposure (conversion) or the sum of its values (mean). Product analytics
(ADR-20261117084505) keeps raw events with properties, so funnels and
retention can be computed per customer, but experiments cannot use them, and
nothing analytics finds can be targeted by a flag. Experiments also need
many customers to detect small lifts, because each customer's own baseline
behavior is noise.

The owner chose before this record:

1. **Metrics:** funnel and retention metrics, both.
2. **Cohorts:** dynamic cohorts, recomputed hourly.
3. **CUPED:** on by default with a 14-day pre-period; results show adjusted
   and unadjusted numbers.
4. **Cohort bounds:** per plan, Hobby 1,000 / Pro 10,000 / Scale 100,000
   members per cohort.
5. **Cohort path:** the gateway stamps a request's cohorts, because member
   lists at these bounds do not fit the 256 KiB flag bundle.

## Decision

### 1. Funnel and retention metrics

`metric_kind` gains two values. Both are binary per customer, so they use
the existing conversion mSPRT, sample ratio check and guardrails.

- **funnel:** 2 to 6 steps (event name and optional property filter each)
  and a window (1 h to 720 h). A customer converts at its last step when it
  sends step 1 after its first exposure and each later step after the
  previous one, all within the window after its first exposure.
- **retention:** a return event (optional filter), a period (`day` or
  `week`) and a period number *N* (1 to 12). A customer converts when it
  sends the return event in (exposure + N·period, exposure + (N+1)·period].

Each customer therefore has a deadline: exposure plus the window, or plus
(N+1) periods. Results count, in every arm, only customers whose deadline
is at or before the experiment's telemetry cursor, so a customer's outcome
is final when it is counted. Funnel and retention results appear one window
or N+1 periods after the first exposures.

Both read `analytics_events`, so an experiment with either metric can only
be created where product analytics is on and the plan keeps events. Each
pass the reconciler checks unconverted customers whose deadline is less
than 25 hours past (events may arrive up to 24 hours late), at most
`ExperimentAnalyticsUnitsPerPass` per experiment.

### 2. CUPED

Each customer gets a pre-period value from `analytics_events` in the 14 days
before the experiment started (`ExperimentCupedPreDays`): the sum of the
metric event's values for mean metrics; otherwise 1 when it sent the metric
event, the funnel's first step or the retention return event, else 0. The
reconciler fills the value once per customer.

With per-arm sums of *Y*, *X*, *Y²*, *X²* and *XY*, θ = cov(X, Y) / var(X)
over all customers, each arm's adjusted mean is ȳ − θ(x̄ − x̄_all) and its
variance var(Y) − 2θ·cov(X, Y) + θ²·var(X). The mSPRT runs on the adjusted
difference with the same mixing scale. The adjusted results drive status and
the recommendation; the unadjusted comparison is reported beside them.
Adjusted p-values keep their own running minimum.

CUPED applies only when every counted customer has a pre-period value and
var(X) > 0; otherwise results are unadjusted and say why. It is on by
default and can be turned off when the experiment is created.

### 3. Cohorts

A cohort is a saved, named query of one environment:

- `did`: customers that sent an event (optional filter) at least *M* times
  in the last *D* days;
- `did_not`: customers with any event in the last 30 days that did not send
  the event in the last *D* days;
- `funnel`: customers that completed a funnel within its window, starting
  in the last *D* days.

*D* is at most the plan's analytics retention and 90 days. An environment
has at most `AnalyticsCohortsMaxPerEnvironment` (20) cohorts. Membership is
customers only; subjects are stored as hashes and cannot be matched.

apid's reconciler recomputes each cohort when it is created, on request, and
hourly. A refresh replaces the members in one transaction, keeps at most
`Limits.AnalyticsCohortMaxMembers` (Free 0, Hobby 1,000, Pro 10,000,
Scale 100,000) customers in customer ID order, records the count and whether
it was truncated, and notifies `analytics_cohorts_changed`.

### 4. Gateway stamps cohorts

gatewayd-internal stamps `X-Faas-Flag-Cohorts` on each guest request with a
verified customer: the sorted, comma-separated keys of the cohorts of the
target deployment's environment that contain the customer, or `-` for
none. Entries are cached per deployment and customer for
`flagCohortsTTL` (60 s), emptied on `analytics_cohorts_changed`, and a
lookup that fails or exceeds 100 ms stamps nothing; it never delays or
fails a request.

The SDKs expose the header as the gateway-verified attribute
`gregale.cohort`. A rule condition `gregale.cohort in [a, b]` matches a
member of any listed cohort; `not_in` matches a customer in none of them.
Without the header the attribute is missing, so neither operator matches.
`ends_with` is refused for cohorts. Cohort keys follow flag key syntax.
SDKs advertise `cohort-targeting-v1`.

### 5. Surfaces

- API: `metric_kind`, `funnel` and `retention` on experiment create, and
  `cuped` (default true); results gain `cuped` and per-variant
  `unadjusted`. Cohorts under
  `/v1/projects/{slug}/environments/{environment}/cohorts` (list, create,
  get, delete, refresh), behind `product_analytics`.
- CLI: `gregale flag experiment create ... --metric-kind funnel --step a
  --step b --window 72h`, `--metric-kind retention --return-event e --period
  week --period-number 1`, `--no-cuped`; `gregale insight cohort
  list|create|get|delete|refresh`.
- MCP: `gregale_list_insight_cohorts` (read-only, diagnose toolset).

## Consequences

- Experiments can test what customers do over days, not one event.
- CUPED usually narrows confidence sequences for customers with history; new
  customers get no reduction, and results never get worse than unadjusted
  in expectation.
- Cohort targeting costs one cached lookup per request and customer on the
  gateway, bounded and fail-open to "missing attribute".
- Members are customer IDs held by apid for as long as the cohort exists;
  account deletion cascades.
- Old SDKs ignore the header, so cohort rules never match for them.
