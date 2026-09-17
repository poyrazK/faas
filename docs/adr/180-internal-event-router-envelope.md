# ADR-180: Internal event-router ingress envelope

- Status: Accepted
- Date: 2026-09-17
- Issue: #1278, Workstream B

## Context

Gregale has durable audit events and app-facing outbound webhooks, but no
tenant-scoped ingress contract for a future content-based internal event
router. Producers should not need to know which scheduler, trigger, or app
will eventually consume an event. The first increment must establish that
contract without coupling publishing to matching or delivery availability.

## Decision

`POST /v1/events:publish` is the authenticated customer ingress. It accepts a
structured envelope with these fields:

- `specversion`: CloudEvents version, currently `1.0`;
- `id`, `source`, and `type`: producer identity and matching keys;
- `time`: occurrence time, defaulted at ingress when omitted;
- `data_content_type`: currently `application/json`;
- `data`: valid JSON payload; and
- `account_id`: a tenancy extension, server-owned after an optional equality
  assertion from the caller.

The handler validates the envelope, rejects cross-account assertions, and
appends the canonical JSON to the existing durable `events` ledger as
`actor=apid`, `kind=event.published`, and `subject=<authenticated account>`.
An optional `Idempotency-Key` uses the existing API replay middleware. The
endpoint returns `202 Accepted` only after the ledger write succeeds.

The ledger row is the durable ingress seam for the next router increment. It
is not a claim that matching or delivery already exists: those consumers will
be added in follow-up work and will preserve the account subject when they
fan out events. No endpoint accepts a caller-selected account or writes an
event for another tenant.

## Consequences

The API and Go SDK now share a stable publish contract, and the canonical
payload can be consumed by an in-memory matcher or a Postgres-backed worker
without changing producers. Existing outbound CloudEvents delivery remains a
separate contract; this ingress uses Gregale's public `data_content_type`
spelling and account extension consistently with EPIC #1278.

The current implementation does not declare subscriptions, evaluate filters,
wake workloads, or route terminal failures to the DLQ. Those concerns remain
explicit follow-ups so the ingress contract can ship and be reviewed in
isolation.
