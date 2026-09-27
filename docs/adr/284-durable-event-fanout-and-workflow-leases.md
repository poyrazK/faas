# ADR-284: Durable event fanout and bounded scheduler dispatch

- Status: Accepted
- Date: 2026-09-27
- Amends: ADR-017, ADR-180, ADR-191

## Context

ADR-180 used the `events` ledger as the publish receipt and a Postgres
notification as the matcher wake. The scheduler reread only the newest 1,000
events within ten minutes. A longer outage or a larger backlog could leave an
accepted event without a delivery. Workflow executor calls and broker polls
also ran on the scheduler's select goroutine, delaying unrelated work.

## Decision

An `AFTER INSERT` trigger on `events` creates an `event_fanout_outbox` row in
the same transaction for every `event.published` event, including events from
`apid`, `vmmd`, and route discovery. `(account_id, source, event_id)` is the
CloudEvents identity. Identical type, schema version, and JSON data are idempotent; changed
content conflicts. The scheduler claims due rows with `FOR UPDATE SKIP LOCKED`,
routes them using deterministic invocation IDs, and acknowledges with a claim
token. A failed route returns to pending with backoff. Notifications remain
advisory wakes. The scheduled sweep drains bounded batches from the outbox
without a time window.

The scheduler's existing bounded work pool now owns workflow dispatch,
trigger polling, and event fanout. Four workflow slots claim distinct runs;
trigger polling and event fanout each have one slot and coalesce redundant
ticks. An executing workflow extends its run lease to its declared step
timeout plus five minutes. A newly claimed run gets five minutes to begin a
step. Pre-migration running rows retain the previous 2 h 5 min stale rule.

Published and inbox events serialize CloudEvents `datacontenttype` and
`accountid`. Ingress still decodes the previous `data_content_type` and
`account_id` aliases. The new `events:publish` and `queues:send` API-key scopes
allow event producers to avoid `deploy:write`; existing deploy keys keep
working.

An account may register immutable Draft 2020-12 JSON Schema versions for a
source/type pair. The first registration makes the CloudEvents
`schemaversion` extension mandatory on public and in-guest publishes of that
pair. Both ingress paths validate data against the selected schema before
appending the event. Schemas are capped at 64 KiB and cannot resolve external
references. Platform `gregale.*` sources remain reserved and are produced
through trusted internal paths.

The new PgStore methods keep static, parameterized SQL in focused state
files. This extends ADR-017's hand-written PgStore exception until the
event tables and queries are included in the generated sqlc schema snapshot.
No query interpolates caller data into SQL text.

## Consequences

The outbox adds a database write per published event. Delivered receipts and
their deduplication identities are retained for 30 days, then pruned in
bounded batches. It starts guaranteeing durable fanout for events inserted
after this migration; historical events are not automatically replayed because
that could duplicate old application side effects. Delivery remains at least
once. Invocation IDs and workflow step idempotency keys remain stable on
recovery.

Fanout reads the currently enabled subscriptions when it processes a receipt.
Disabling or deleting a subscription during an outage excludes its backlog;
preserving the subscription set at publish time needs a separate snapshot
design. Producer scopes are account-wide, without per-source or per-app
restrictions.
