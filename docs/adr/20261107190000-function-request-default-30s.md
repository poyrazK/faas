# ADR-20261107190000: Functions default to a 30 s request timeout

Date: 2026-11-07
Status: Accepted (owner decision, 2026-11-07)
Amends: ADR-093 (request budgets: the Function default), ADR-1150 decision 3
("Defaults unchanged")
Related: ADR-1180 (async invocations run for the request timeout, floor 30 s),
ADR-974 (functions idle after 30 s), spec §4.9

## Context

ADR-093 gave a function request 3 s to start its response unless the app set
`request_timeout_s`, an edge rule set a budget, or the caller sent
`x-faas-budget-ms`. Apps got 30 s. ADR-1150 raised what each plan may ask
for and kept both defaults.

The 3 s default is the first limit most new functions hit, and they hit it
before their authors know it exists. A first database connection, a model
call, a large upload to object storage or a cold dependency import after a
cold boot each pass 3 s. The result is a 504 `request_budget_exceeded` on a
handler that is correct. The runner itself already lets every invocation
run for at least 30 s (ADR-1150 decision 4), and async, queue and cron
invocations get at least 30 s (ADR-1180), so the 3 s value applied only to
synchronous HTTP requests.

## Decision

1. `api.RequestBudgetDefault`, the Function budget when nothing else sets
   one, is 30 s on every plan. It equals the Free plan's ceiling, so no
   plan's default exceeds what that plan may ask for. A test asserts this
   for every plan.
2. Apps keep their own constant, `RequestBudgetAppDefault`, also 30 s. The
   two stay separate so either can change later.
3. Everything that sets a budget keeps working and still wins:
   `request_timeout_s` (per app or per function in `gregale.yaml`),
   `kind=budget` edge rules and the override header. A function that needs
   a tight bound sets one, for example `request_timeout_s: 3`.

## Consequences

- Synchronous function requests that took 3–30 s now succeed instead of
  returning 504.
- Billing is unchanged in practice: a function instance is billed per
  running second and stays up for its idle timeout (30 s by default) after
  each request, so a request that used to be cut at 3 s ran on an instance
  that was billed for that time anyway.
- A function that hangs holds its concurrency slot for up to 30 s instead
  of 3 s before the gateway answers 504. Plan concurrency limits still bound
  how many run at once; callers that want a fast failure set a timeout.
- `docs/functions.md`, `docs/per-instance-concurrency.md` and the generated
  `docs/plans.md` state the new default.

## Alternatives

- **10 s.** Fixes most surprises but keeps a function default unlike the app
  default for no stated reason. Rejected by the owner.
- **Keep 3 s and only make it visible.** Leaves the failure in place.
  Rejected.
- **Default to the plan's ceiling.** A Scale function would wait up to
  15 minutes on a hung handler by default. Rejected.
