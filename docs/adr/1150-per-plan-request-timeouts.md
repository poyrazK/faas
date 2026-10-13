# ADR-1150: Per-plan request timeout ceilings

- **Status:** accepted
- **Date:** 2026-10-12
- **Related:** ADR-093 (request budgets), ADR-999 (function streaming),
  spec §4.9 (function runners)

## Context

ADR-093 gives every customer request a wall-clock budget: 3 s by default
for functions and 30 s for apps. An app can set its own
`request_timeout_s`, an edge rule (`kind=budget`) can set one per route,
and a request can ask for one with `x-faas-budget-ms`. Every source was
capped at `RequestBudgetMax`, 30 s, on every plan, Scale included.

Inside the guest, the function runner bounded every invocation by a
separate fixed 30 s, whatever the request's budget. A function could
therefore never run longer than 30 s. Once functions could stream
(ADR-999), this became the next limit people hit: long model-token
streams, report generation and large exports all run past 30 s.

## Decision

1. **Per-plan ceilings.** `Limits.RequestBudgetMaxMs` is set per plan:
   Free 30 s, Hobby 60 s, Pro 5 min, Scale 15 min. It caps every budget
   source: `request_timeout_s`, edge rules and the override header. apid
   rejects a `request_timeout_s` above the plan's ceiling.

2. **Cross-plan ceiling.** `RequestBudgetPlanMax` (15 min) is the largest
   ceiling any plan may set.
   - Edge-rule and route-requirement budgets are validated against it,
     and the plan's ceiling clamps them again at request time.
   - The public edge's `CustomerRequestEnvelopeTimeout` includes it.
   - `RequestBudgetMax` (30 s) stays the default ceiling for plans that
     set none, and apid's own request limit.

3. **Defaults unchanged.** Functions still start with 3 s and apps with
   30 s. The ceilings raise what an app may ask for, not what it gets.

4. **The guest learns its deadline.** The gateway forwarder appends
   `X-Faas-Deadline-Ms`, the request budget left at dispatch, to every
   request it hands a guest. Customer-supplied `x-faas-*` headers are
   dropped first, so the guest only ever sees the platform's value.
   - The function runner bounds the invocation, a streamed response
     included, by the longer of that deadline and 30 s, capped at
     15 min. The 30 s floor keeps every function that works today
     working.
   - Handlers may read the header to stop early.

5. **Async invocations unchanged.** Async, queue and cron invocations
   carry no request budget, so the runner keeps its 30 s default for
   them. Lengthening those means extending invocation leases; it is a
   follow-up. *Superseded by ADR-1180, which gives them the app's
   request timeout.*

6. **A request in flight keeps its instance.** A function's idle timeout
   (30 s by default, ADR-974) can now be shorter than its requests. The
   idle reaper already skips an instance with an in-flight request, but
   on a single-box install schedd's stats poller read only the
   vmmd→schedd capacity stream. vmmd publishes that stream only when it
   has a node name, so the poller saw no in-flight requests at all and
   the reaper parked instances mid-request. A poller with no owner node
   now reads vmmd `Stats` directly, as it did before the stream existed.

## Consequences

- A Scale function can answer, or stream, for up to 15 minutes with
  `gregale app APP --request-timeout 900`; Pro for up to 5 minutes.
- Longer runs are billed as running seconds like any other (§4.7), and
  plan concurrency limits still bound how many run at once.
- `docs/plans.md` gains a **Max request timeout** column.
- The public edge's envelope timeout grows by about 14.5 minutes to carry
  the longest budget. Slow uploads remain bounded by the upload allowance
  and the idle read deadline.

## Alternatives

- **Raise `RequestBudgetMax` to 15 minutes.** Rejected: plans without an
  explicit ceiling and apid would silently inherit it.
- **A separate function-timeout field.** Rejected: `request_timeout_s`
  already exists end to end (API, CLI, manifest, gateway) and means the
  same thing.
- **Stamp the deadline into the runner's environment at deploy time.**
  Rejected: plan or timeout changes would need a redeploy, and the
  gateway already knows each request's budget.
