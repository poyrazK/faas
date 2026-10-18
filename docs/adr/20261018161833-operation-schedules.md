# Scheduled Operations

- **Status:** accepted for implementation
- **Date:** 2026-10-18
- **Owner:** Operations maintainers
- **Related:** [ADR-521](521-customer-operations.md),
  [ADR-666](666-operation-execution-preview-admission.md),
  [ADR-1196](1196-operation-deadlines-and-budgets.md)

## Context

A customer can start an Operation now, or a provider webhook can start one
(ADR-1193). Starting one later, or on a recurring timetable — a nightly export,
an invoice sent at 09:00 in the customer's timezone, a follow-up check in three
days — needs the caller's own scheduler, which then has to handle retries,
duplicates and its own outages.

## Decision

A gated **Operation schedule** resource starts ordinary Operations at chosen
times. It never changes an admitted Operation: when a schedule fires, it admits
an Operation exactly as `POST /v1/platform-tenant-self/customer-operations`
would, with the same definition checks, input schema, plan limits, preview
admission and customer status. Before it fires, no Operation exists.

A schedule belongs to one customer (platform tenant) and one definition, and
carries a fixed input. It is either:

- **once** — `at`, an RFC 3339 time at most 366 days ahead; or
- **cron** — a five-field expression and IANA timezone in Gregale's canonical
  cron grammar (`pkg/cronexpr`), firing no more often than the plan's minimum
  interval.

**Exactly once per fire.** apid fires due schedules. One transaction claims the
schedule row, admits the Operation with idempotency key
`schedule:<schedule id>:<fire time>` and advances the schedule. A retry or a
second apid replica replays the same admission instead of starting another.

**Missed fires do not pile up.** After downtime a cron schedule fires once for
the latest missed time, then continues from the next future time. A once
schedule fires late rather than never.

**A refused fire is recorded, not retried.** If admission is refused (preview
closed, customer suspended, plan limit, definition retired, input no longer
valid), the schedule keeps the reason in `last_error` and moves on. A cron
schedule waits for its next time; a once schedule ends `failed`. Storage
errors roll back and retry on the next tick.

**States:** `active`, `paused`, `completed` (a once schedule that fired),
`failed` (a once schedule whose fire was refused) and `cancelled`. Pausing
holds a cron schedule; resuming continues from the next future time.

**Surfaces.** Customer routes under
`/v1/platform-tenant-self/customer-operation-schedules` create, list, read,
pause, resume and cancel the customer's own schedules (read scope for reads,
manage scope for writes). The account lists and cancels schedules for an app
at `/v1/apps/{slug}/operation-schedules`.

**Gated.** `FAAS_OPERATION_SCHEDULES_ACCOUNTS` (apid) names the accounts that
may use schedules; empty, the default, admits none, and every schedule route
is indistinguishable from an absent one. The routes stay out of the OpenAPI
document and SDKs until launch. Schedules of an account removed from the list
refuse their fires: a cron schedule skips each time, a once schedule fails.

**Firing.** Every apid replica runs the firer every 10 seconds. It leases up
to 100 due schedules for one minute, so another replica skips them; a replica
that dies mid-batch delays those fires until the lease lapses. apid checks its
own gates (the rollout list and the preview admission policy) and passes a
refusal to the store; the store checks the rest inside the admission.
`<prefix>_customer_operation_schedule_fires_total{result}` counts `admitted`,
`refused` and `error` fires.

**Limits** (`pkg/api/limits.go`): schedules per account by plan (Hobby 10,
Pro 100, Scale 1,000), minimum cron interval by plan (Hobby 5 minutes, Pro and
Scale 1 minute), 366 days for a once schedule, and the existing submission
input bound.

## Consequences

- Timed and recurring work needs no external scheduler, and a missed fire or
  retry never runs business work twice.
- Fired Operations are ordinary: their deadlines, budgets, timelines and
  telemetry are unchanged, and pending-work limits still apply when they fire.
- A schedule's input is fixed at creation; changing it means a new schedule.

## Rejected alternatives

- **A `start_at` field on the submission.** It would admit an Operation that
  then waits, changing deadline and pending-work semantics for every caller,
  and a gated field cannot be hidden the way a gated route can.
- **Firing from schedd.** schedd does not hold apid's preview admission policy;
  firing there would bypass the cohort gate.
- **Catching up every missed cron time.** After an outage that could start
  hundreds of Operations at once.
