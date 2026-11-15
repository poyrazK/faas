# ADR: An app's event source is its own, and publishing has a rate

Date: 2026-10-26
Status: Accepted
Amends: [ADR-929](929-application-scoped-producer-key-publication.md) (the `app.<UUID>` namespace)
Related: [ADR-1190](1190-application-auth.md) (Auth app workflow starts), [ADR 20261025211616](20261025211616-event-routing-company-boundary.md)

## Context

Two GA blockers from the 2026-10-25 event audit:

- **Forgeable app sources.** ADR-929 made `app.<app UUID>` "a convenience
  namespace, not an authorization boundary": any account key could publish an
  event with that source through `POST /v1/events:publish`, and an app's
  runtime could publish as any other app. Since then the Auth app publishes
  `auth.workflow.start` only after checking `workflow:start`, and its docs tell
  workflows to subscribe from its source. Anyone in the account could start
  those workflows without the check by publishing the same event themselves.
- **No publish rate.** Publication takes the account's publication lock and
  holds a database connection while it waits, and nothing bounded how fast an
  account publishes. Storage budgets bound the sustained rate only after the
  events are written.

## Decision

1. **`app.<UUID>` is reserved to the app.** Only the app's producer route
   (`/v1/apps/{slug}/events:publish`), the app's own runtime and server-side
   app publication (commit relay) publish from it. `POST /v1/events:publish`,
   `:publish-batch` and the platform-tenant route refuse such sources (400, or
   a non-retryable rejected batch item); an app's runtime may use only its own.
   Any UUID spelling counts (`eventcontract.AppSource`), so upper case, braces
   or `urn:uuid:` cannot slip past.
2. **A per-account publish rate.** `api.EventPublishLimits` per plan:
   5/s burst 100 (Free), 20/s burst 200 (Hobby), 100/s burst 1,000 (Pro),
   500/s burst 5,000 (Scale). Every publication path charges each event once,
   a batch all its events before it starts; over the rate the request accepts
   nothing and answers `429 event_publish_rate_limited` with the limit, the
   events carried and `Retry-After`. Bursts are at least the 100-event batch
   maximum.
3. **Per process.** The token buckets live in each apid and vmmd process
   (`events.PublishLimiter`; vmmd caches an account's plan for a minute). This
   protects each process's connection pool, which is what the lock contention
   exhausts; an account using several processes gets their sum. Storage
   budgets keep bounding the sustained rate across the fleet.

## Consequences

- Workflows and consumers can trust an `app.<UUID>` source as that app's word,
  including the Auth app's permission-checked starts.
- A customer that published `app.<UUID>` sources from account keys must move to
  the app producer route or the app's runtime. ADR-929's "account-scoped legacy
  publication of the same source/ID" no longer exists.
- Bursts above the plan rate get 429 and retry; the event identity makes a
  retry with the same ids safe.
- Release N-1 apid or vmmd beside N still accepts what it accepted before until
  it is replaced; the routes behave per process during the rollout.

## Alternatives

- **A database-wide rate per account.** Exact across processes, but adds a
  write per publish to the path this protects.
- **Trust the producer stamp instead of the source.** The `producer` extension
  (ADR 20261025211616) is server-stamped, but subscriptions match sources, and
  the Auth app contract is written against its source.

## Amendment 2026-11-14: one shared counter as well (event-driven hunt #16)

Per process, the plan rate multiplied with the fleet: every apid replica and
every vmmd node admitted the full rate on its own, so a Free account publishing
from functions spread over N nodes got N × 5/s. Item 3 changes:

- The process bucket stays and runs first: a burst over the rate is refused
  without touching the database, as before.
- What it admits is then charged to one shared counter per account
  (`pg_ratelimit_counters`, scope `event_publish`, the token bucket the
  central edge limiter already uses), all or nothing per request. Over it the
  request gets the same `429 event_publish_rate_limited`.
- The charge is its own short autocommit statement before publication, not
  inside the publication transaction, so the counter row lock is held for one
  statement. A batch is one statement for all its events.
- If the counter cannot be read the request is judged on the process bucket
  alone; publication needs the same database anyway.

This takes the "database-wide rate" alternative above after all: one extra
statement per publish request is small next to the rows publication already
writes, and the process bucket keeps a burst from reaching it.
