# ADR: Data API change hooks

Date: 2026-10-22
Status: Accepted
Related: [ADR-650](650-schema-generated-data-api.md), [ADR-929](929-application-scoped-producer-key-publication.md), [ADR-432](432-event-workflow-starts.md), [ADR 20261021101510](20261021101510-data-api-file-storage.md), [ADR 20261019102108](20261019102108-data-api-usage-and-owner-caps.md)

## Context

A Data API turns tables into REST endpoints, but an application usually has to
do something when its data changes: send a welcome email when a user row is
inserted, charge a card when an order is paid, refresh a search index. Today
the owner must poll, write an outbox by hand (the `data-api-outbox` example) or
call another service from a SQL function.

Gregale already delivers events well. The internal event router accepts
app-scoped events with producer-key deduplication (ADR-929), and delivers them
to `event_triggers` subscriptions and workflow event starts (ADR-432) with
retries, replay, ordering diagnostics and delivery inspection. What is missing
is a safe way to turn committed row changes into those events.

Constraints that shaped the design:

- **The serving login is restricted.** A Data API's `data_api` login may hold
  table privileges only in the exposed schema, may not execute SECURITY
  DEFINER functions there, and is replaced on every credential rotation.
  Anything the relay needs must reach a new login without per-login grants.
- **Neon scales to zero.** A logical replication connection keeps a compute
  awake, and a replication slot needs a role the Data API does not have.
- **Data APIs park.** A runtime only runs while it serves traffic.

## Decision

### Capture: a transactional outbox filled by an owner trigger

`gregale data-api hooks sql` prints a migration that the owner applies with
the existing migration binding:

- **`<schema>.gregale_change_events`** in the first exposed schema records one
  row per change: relation, operation, primary key, old and new rows (as
  JSONB), changed column names, transaction ID, capture time and relay state.
- **`gregale_hooks.capture()`**, a SECURITY DEFINER row trigger function in a
  private schema owned by the schema owner, writes those rows inside the
  writing transaction. EXECUTE is revoked from PUBLIC; PostgreSQL does not
  check EXECUTE or schema USAGE when a trigger fires, so writes by the
  restricted login still record changes. A rolled-back write records nothing.
- **`gregale_hooks.enable(relation, payload, exclude)`** adds the
  `gregale_change_hook` trigger to a table with a primary key. `payload` is
  `row` (old and new rows) or `keys` (primary key and changed column names
  only); `exclude` drops columns such as password hashes from events.
  `disable(relation)` removes it. Updates that change no included column emit
  nothing.
- **`SET LOCAL gregale_hooks.skip = 'on'`** suppresses capture for one
  transaction, for backfills.
- **`gregale_hooks.retry_failed()`** and **`discard_failed()`** let the owner
  handle events the platform rejected.

### Isolation: the gateway-owned `hooks` access kind

The outbox lives in the exposed schema so the Data API login reaches it
through the existing default privileges, including after rotation. Row-level
security admits only requests whose claims carry `gregale_access = 'hooks'`.
The gateway already overwrites `gregale_access` on every request
(`user`, `anonymous`, `server`); it never stamps `hooks`, so clients, server
keys and live reads see no rows. In addition:

- the gateway answers 404 for the table's REST path, removes it from OpenAPI,
  and type generation and the serving fingerprint skip it;
- the relay functions `gregale_change_claim` and `gregale_change_settle` are
  SECURITY INVOKER, carry no `@gregale:rpc` marker (so the gateway never routes
  them) and refuse any caller without the `hooks` claim.

### Relay: inside the Data API runtime

When `DATA_API_CHANGE_EVENTS_URL` is set, the runtime runs a relay:

- **Engine use.** The relay calls PostgREST over loopback with an internal
  token, like file storage. It takes an admission slot only when one is free
  and never queues ahead of requests; it waits briefly for a slot only to
  settle work it has already published.
- **One relay at a time, in order.** `gregale_change_claim` serializes on a
  transaction advisory lock and refuses to claim while another relay holds an
  unexpired lease (60 seconds), so one instance publishes at a time, in outbox
  order. A crashed relay's lease expires and another instance takes over.
- **Head-of-line retries.** The relay stops a batch at the first retryable
  failure and backs off; later rows wait. A row's changes are captured in
  commit order (a later writer waits on the row lock), so each row's events
  are published in commit order. Permanent rejections become failed rows and
  do not block others. Published rows are deleted.
- **Triggers.** A successful write through the API (REST, RPC or storage)
  wakes the relay; otherwise it polls with backoff from 1 to 30 seconds while
  the instance runs. Changes made outside the API are published the next time
  the Data API runs.
- **Restores.** A snapshot restore, seen as the wall clock jumping against
  the monotonic clock, gives the relay a new identity so restored copies never
  share a lease.

### Publication: a private runtime route onto ADR-929

`POST /v1/runtime/data-api/events` accepts at most 100 events and 1 MiB from a
`gregale:data-api:events` workload token of a live instance of an active app.
Each event becomes an app-scoped producer-key publication: source
`app.<app UUID>`, key `data-api.change.<outbox event UUID>`. Retries of the
same row therefore deduplicate while the acceptance receipt is retained, and
the relay is at-least-once. The route stops at the first retryable failure and
reports the rest as retryable rejections (`event_publish_not_attempted`),
matching the relay's ordering; the batch result vocabulary is unchanged.

The route grants no authority beyond the calling app's own event namespace,
which its owner can already publish to (ADR-929). It does not require the app
to be a Data API.

### Event shape

- `type`: `<schema>.<table>.inserted|updated|deleted`, so subscriptions can
  match one table (`api.orders.*`), one operation (`*.inserted`) or all.
- `time`: capture time.
- `data`: `schema`, `table`, `op`, `key` (primary key columns), `new`, `old`,
  `changed` (updates), `sequence` (outbox ID) and `txid`. The trigger leaves
  out rows over 64 KiB together, and the relay drops `new` and `old` when
  `data` would still exceed 64 KiB; either way `truncated` is true and
  consumers read the row by key. This also bounds the outbox and each claim.

### Consumers and CLI

Consumers use existing `event_triggers` and workflow event triggers on source
`app.<UUID>`. `gregale data-api hooks enable|status|disable NAME` stores or
removes the setting, requests a fresh restart and prints the source with a
subscription example.

## Consequences

- Delivery is at-least-once with per-row commit order; consumers must
  deduplicate side effects (event IDs are stable).
- Hooked writes cost one extra row insert in the same transaction. Each event
  counts toward the account's event quotas and storage like any publication.
- While events are pending or the instance is resident, the relay polls the
  database, which can keep a scale-to-zero compute awake until the instance
  parks (its idle timeout).
- Retrying failed events republishes them after newer events of the same row.
- Pending and failed counts are visible only in the outbox table for now;
  reporting them to the dashboard is a follow-up.

## Rejected alternatives

- **Webhooks from SQL (`pg_net`).** At-most-once, no retries or replay, and
  outbound HTTP from the database bypasses Gregale's delivery controls.
- **Logical replication through livequeryd.** Captures every writer without
  triggers, but needs a replication role per database, keeps Neon computes
  awake, and makes durable slot positions a platform responsibility.
- **A private schema with per-login grants.** Grants would have to follow each
  credential rotation, as SQL deadline enrollment (ADR-1735) does; the
  exposed-schema table with a gateway-owned claim needs none.
- **A separate relay app.** Costs an app per Data API and a broader binding.
- **Account API keys stored as app secrets.** Long-lived, MFA-exempt
  credentials inside a workload; the workload identity is short-lived and
  bound to the app.
