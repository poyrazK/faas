# Per-customer Operation concurrency

- **Status:** accepted for implementation
- **Date:** 2026-10-18
- **Owner:** Operations maintainers
- **Related:** [ADR-521](521-customer-operations.md),
  [ADR-1194](1194-operation-waits-and-approvals.md),
  [ADR-374](374-application-keyed-work-policy.md),
  [ADR-20261018183000](20261018183000-child-operations.md)

## Context

All customers of an app share its Operation capacity. One customer that
submits a thousand exports, or fans out a large import, takes every instance
the app can run and everyone else's Operations wait behind it. Providers
answer this today with their own per-customer queues in front of Gregale, which
duplicates admission and hides the backlog from the platform.

## Decision

The account sets an **Operation concurrency policy**: how many Operations of
one name, in one app environment, a single customer may have in flight. Work
beyond that is admitted and **queued**, not refused, and starts in submission
order as the customer's earlier Operations finish.

**Policies.** A policy names the app, environment scope and Operation name and
sets `max_running`. Without a customer it is the default for every customer;
with `platform_tenant_id` it overrides the default for that customer (a larger
plan, an internal tenant). The effective limit is the customer's override, else
the default, else none. Policies change at runtime without a deploy.

**In flight** means admitted and not queued, in `accepted`, `running` or
`requires_reconciliation`. An Operation waiting on a timer, approval or event
holds no instance and no slot; when it resumes it runs even if the customer is
briefly over its limit.

**Queueing.** Admission (every path: customer start, webhook, schedule fire,
child start) counts the customer's in-flight Operations of that name. When the
count has reached the limit, or the customer already has queued Operations of
that name, the new Operation is admitted in `accepted` with its execution held:
an HTTP invocation or workflow run is created with a due time that never
arrives, and a job task waits while its queue row exists. Nothing else changes:
it counts toward the pending-Operations limit, its deadline runs while it
waits, and cancelling it works as for any pending Operation. Admission and release both hold the account's Operation lock, so
concurrent submissions cannot overshoot the limit.

**Release.** Every apid replica runs a releaser every two seconds. For each
customer and Operation name with queued work it takes the account lock,
recomputes the limit and the in-flight count, and makes the oldest queued
Operations due now, up to the free slots. schedd's dispatchers already poll for
due work every second, so a freed slot starts the next Operation within a few
seconds. Raising or removing a policy releases waiting work on the next pass.
While anything is queued, the releaser also wakes on `customer_operation_changed`,
which every Operation save notifies, and runs within 250 ms; the two-second
pass remains the fallback for a dropped notification.

**Every execution kind queues.** An HTTP invocation or workflow run is held by
its due time; a job run's task is held because the customer-Operation job claim
skips Operations that still have a queue row. Release clears the due time or
removes the row.

**Surfaces.** The account manages policies with
`PUT`/`GET`/`DELETE /v1/apps/{slug}/operation-concurrency` (MFA; deploy write
scope to change, read scope to list). The list also reports, per customer with
queued work, its limit, in-flight and queued counts. A customer reads its own
queue, with each Operation's position, at
`GET /v1/platform-tenant-self/customer-operation-queue`.

**Gated.** `FAAS_OPERATION_CONCURRENCY_ACCOUNTS` (apid) names the accounts that
may use policies; empty, the default, admits none. For an account not listed,
admission ignores policies, the releaser releases anything still queued, and
every route is indistinguishable from an absent one. The routes stay out of the
OpenAPI document and SDKs until launch.

**Limits** (`pkg/api/limits.go`): policies per app by plan (Hobby 10, Pro 100,
Scale 1,000); `max_running` between 1 and 1,000.

**Telemetry.** `<prefix>_customer_operation_queue_releases_total` counts
queued Operations the releaser started; `<prefix>_customer_operation_queued`
and `<prefix>_customer_operation_queue_oldest_seconds` report the fleet's
backlog. `FaasCustomerOperationQueueNotDraining` warns when the oldest queued
Operation has waited thirty minutes with no release; the account's concurrency
view shows each customer's backlog.

## Consequences

- One customer's backlog no longer delays other customers' Operations of the
  same name; its own work runs in order at its limit.
- Callers never retry a refusal: queued work is accepted and visible, with a
  position.
- Release adds up to a few seconds between a slot freeing and the next start.
- Time spent queued counts against the Operation's deadline.

## Rejected alternatives

- **Refuse admission over the limit.** Every caller would need its own retry
  loop, and fan-out (child Operations, schedules) would fail instead of
  waiting.
- **Enforce the limit in schedd's claim queries.** It would touch every
  dispatcher (HTTP, workflow) and their fairness scans; holding the due time
  keeps dispatch unchanged.
- **A field on the Operation definition.** Limits would need a deploy to change
  and could not differ per customer.
