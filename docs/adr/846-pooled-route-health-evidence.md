# ADR-846: Stage-pooled evidence for low-traffic critical routes

- Status: Accepted
- Date: 2026-10-09
- Related: ADR-454, ADR-455, ADR-844, ADR-845
- Amends: ADR-454 and ADR-498 (fixed one-minute observation windows)

## Context

Route health compares candidate and stable in two closed one-minute windows
and needs at least 20 requests per route, side and window. A critical route
with a few requests per minute, or a candidate at an early 1–10% stage, never
meets that bound. Its evidence stays `unknown`: in report mode it is never
judged, and in enforce mode it holds the rollout until someone removes the
selector. ADR-844 now seeds selectors by customer reach, not request volume,
which makes such routes more common.

## Decision

Keep the one-minute evaluation unchanged. When a selected route is `unknown`
only because its windows lack requests (`insufficient_requests` or
`insufficient_latency_requests`, with no regressed window and no other unknown
reason), re-evaluate that route over two pooled windows:

- The span starts at the observation anchor rounded up to a minute and ends at
  the newest closed one-minute window, capped to the newest
  `RouteHealthPooledMaxSpan` (30 minutes).
- It is split into two equal, consecutive, minute-aligned halves, and needs at
  least `RouteHealthPooledMinSpan` (4 minutes) in total.
- Each half applies the same request minimums, error and latency thresholds,
  and the existing two-window confirmation rule.

Pooled counts are stored in a separate `pooled_windows` field; the finding's
`windows` always remain the shared one-minute windows, so every check that
aligns customer cohorts, watched status codes, investigations or incident
escalations with those windows is unchanged. The verdict is adopted inside
`Evaluate` only when the one-minute summary is sparse and the pooled summary
reaches `healthy` or `regressed`; the finding is then labeled
`evidence_window: pooled`. Any client that re-derives verdicts (the CLI report
check, saved-report validation) therefore reaches the same result, and pooled
window bounds are validated against the anchor. Pooling uses one extra
observation read for the affected routes only, in the same snapshot.

Production route monitoring (ADR-498) applies the same rule to its absolute
budgets, also treating `insufficient_errors` as sparse. Its span starts at the
monitor's observation anchor, so a long-serving deployment is judged over the
newest 30 minutes in two 15-minute halves. A pooled error-budget violation can
open an incident and, within ADR-845's 30-minute window, request a rollback.
Customer cohorts, watched status codes and investigations keep one-minute
windows.

## Consequences

- A route at about five candidate requests per minute reaches a verdict after
  about eight minutes of a stage instead of never.
- Pooled windows weight the whole stage equally, so a regression that starts
  late is diluted. Two consecutive halves must still both regress, and a
  regressed one-minute window is never pooled away.
- Saved decisions and history record the pooled windows they used.
- Synthetic probes for routes with no organic traffic remain future work.
