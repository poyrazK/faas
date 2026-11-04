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
