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
