# ADR-20261017100853 · Customer self-service Operation signals

- **Status:** accepted for implementation
- **Date:** 2026-10-17
- **Owner:** Operations maintainers
- **Related:** [ADR-1197](1197-operation-event-waits-and-signals.md),
  [ADR-20261016100929](20261016100929-webhook-delivered-operation-signals.md),
  [ADR-1194](1194-operation-waits-and-approvals.md)

## Context

A customer (platform tenant) can start, read, cancel and approve its own
Operations with tenant credentials. Signals (ADR-1197) were account-only: to
resume its own waiting Operation, a customer's backend needed the account's API
token, which also reaches every other customer. Operations a provider webhook
started for the customer (ADR-1193) are the common case, and the customer never
saw their IDs.

## Decision

Two tenant-self routes, both requiring `platform_tenant:operations:manage`:

- `POST /v1/platform-tenant-self/customer-operations/{id}/signals` takes the
  same body and returns the same receipt as the account route. Another
  customer's Operation is not found.
- `POST /v1/platform-tenant-self/customer-operation-signals`
  `{definition_id, subject, signal_id, event, payload}` signals the customer's
  own active Operations of that definition about one business subject. The
  definition fixes the app, environment and Operation name, as tenant-self
  start already does. Matching uses the tenant filter from ADR-20261016100929, so another
  customer's Operation about the same subject ID is never reached. At most 16
  Operations, as for the account route.

Validation, idempotency, conflicts and limits are the ADR-1197 ones. A
`signal_id` is per Operation whichever route delivers it, so a retry through
another route is the same signal.

**History records the source.** The `signal_received` event gains `source`:
`account`, `customer` or `webhook` (ADR-20261016100929 deliveries). The source is history,
not identity: a repeat from another source is still a duplicate. The
`app.operation_signal_delivered` audit event carries it too.

**CLI.** `gregale customer-operations signal --self` uses the tenant routes; a
subject signal names `--definition` instead of `--app`, `--scope` and
`--operation`.

## Consequences

- A customer's backend resumes its own Operations with a token scoped to that
  customer.
- The tenant token's route allow-list grows by these two routes.
- Readers of an Operation's history can tell who resumed it.

## Rejected alternatives

- **Scoping the subject route by app and environment.** A customer may be
  linked to several apps; the definition is the unambiguous handle tenant-self
  start already uses.
- **Making the source part of the signal's identity.** A provider webhook and
  the customer's backend may deliver the same business event; deduplicating
  them is the point of `signal_id`.
