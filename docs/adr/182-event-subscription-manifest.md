# ADR-182: Declarative internal event subscriptions

- Status: Accepted
- Date: 2026-09-18
- Issue: #1278, Workstream B

## Context

ADR-181 defined the tenant-scoped event matcher, but there was no customer
configuration surface for declaring which event patterns an application wants
to receive. The existing `gregalemanifest` loader is YAML-only and explicitly
rejected the `gregale.toml` format called out by the EPIC.

## Decision

`gregale.toml` is accepted for the event-subscription declaration below:

```toml
[[triggers.event]]
source = "billing.*"
type = "invoice.paid"
filter = '{ "data": { "amount": { "$gt": 100 } } }'
```

The loader parses only the `triggers.event` TOML table in this increment and
rejects every other TOML key so unsupported configuration cannot be silently
ignored. Filters remain JSON strings and are validated by the canonical
`pkg/events` matcher. `app` is optional at parse time for a single-app deploy;
the authenticated apply path must bind it to an app before persistence.

Existing `gregale.yaml`/`gregale.yml` manifests and their trigger schema are
unchanged. Persistence, account ownership assignment, and schedd delivery are
separate follow-ups.

## Consequences

Customers can author the Workstream B subscription shape without bypassing the
same pattern/filter validation used by the router. The strict, event-only TOML
surface makes the format safe to expand later while keeping this parser PR
independent of database and dispatch migrations.
