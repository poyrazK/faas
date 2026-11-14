# ADR-182: Declarative internal event subscriptions

- Status: Accepted
- Date: 2026-09-18
- Issue: #1278, Workstream B

## Context

ADR-181 defined the tenant-scoped event matcher, but there was no customer
configuration surface for declaring which event patterns an application wants
to receive. The first Workstream B parser increment added a strict event-only
TOML surface; the full deployment manifest still needed equivalent YAML
support.

## Decision

YAML deployments declare subscriptions with the top-level `event_triggers`
list:

```yaml
event_triggers:
  - source: billing.*
    type: invoice.paid
    filter: '{"data":{"amount":{"$gt":100}}}'
```

Event-only projects may use the equivalent TOML declaration:

```toml
[[triggers.event]]
source = "billing.*"
type = "invoice.paid"
filter = '{ "data": { "amount": { "$gt": 100 } } }'
```

The YAML decoder remains strict, and the TOML parser rejects every other TOML
key so unsupported configuration cannot be silently ignored. Filters remain
JSON strings and are validated by the canonical `pkg/events` matcher. `app` is
optional at parse time for a single-app deploy; the authenticated apply path
must bind it to an app before persistence.

Existing `gregale.yaml`/`gregale.yml` trigger entries are unchanged; the new
`event_triggers` list is additive. Persistence, account ownership assignment,
and schedd delivery are separate follow-ups.

## Consequences

Customers can author the Workstream B subscription shape in either manifest
format without bypassing the same pattern/filter validation used by the router.
Source-ref reconciliation applies YAML and TOML declarations through the same
authenticated path. The durable subscription rows are reconciled on canonical,
resumable, and legacy deploys, and the schedd fan-out worker uses the persisted
rows for account-scoped matching and ordinary async delivery. The app-scoped
`GET /v1/apps/{slug}/event-subscriptions` endpoint and
`gregale events subscriptions <app>` command expose the reconciled result so a
customer can verify the router configuration without opening deployment
artifacts.

## Amendment (2026-11-07): filter size and depth caps

Events hunt #9 found subscription filters bounded only by the 1 MiB source
manifest, while the publish trigger copies every candidate recipient's filter
into the event's `recipient_snapshot`. A few apps with 1 MiB catch-all filters
made every event in the account, including uncharged `gregale.*` platform
events, store megabytes of filter copies.

The snapshot keeps the filter: routing evaluates it against the event from the
snapshot, so membership stays the one captured at publish time. Filters are
bounded instead. A filter persisted as a subscription must be at most
`EventSubscriptionFilterMaxBytes` (8,192) bytes as compact JSON and at most
`EventSubscriptionFilterMaxDepth` (8) levels of object/array nesting, the
top-level object counting as 1. The manifest validator enforces both on every
path that applies `event_triggers` (deploys, stage deploys, project and GitHub
deploys, batch trigger create) and returns `422
event_subscription_filter_too_large` or `event_subscription_filter_too_deep`
with the limit and observed value; the store repeats the check before it
writes. `event_subscriptions` carries a `NOT VALID` CHECK on
`octet_length(filter::text) <= 16384` (twice the API cap, since jsonb's text
form adds a space after each `:` and `,`). Evaluation of stored filters is not
capped, so subscriptions created before the cap keep matching until they are
declared again. Realistic filters are tens of bytes and three or four levels
deep; 8 KiB matches the realtime consumer-group filter cap.
