# ADR-451: Continuous route policy checks and safety transition events

- Status: Accepted
- Date: 2026-10-02
- Related: ADR-449, ADR-450, ADR-344 (webhook event outbox)

## Context

A passing deployment check can become invalid after an edge rule or app policy
edit. Lookup detects stale evidence and canary advancement requests a refresh,
but a completed release needs rechecks without customer polling scripts.

## Decision

Queue automatic checks in the same transaction as edge rule create/update/delete,
relevant app configuration edits, and account plan/eligibility changes. Compare
the inputs used by the configuration fingerprint, excluding timestamps and
unrelated manifest fields. PostgreSQL triggers cover ordinary API, manifest and
reviewed policy writes; memory stores mirror the hooks under their mutex. Policy
monitoring targets live deployments with retained captures or existing check
jobs and saved app requirements. Reaching live also schedules their checks.
Historical deployments retain explicit checking without policy-triggered alerts.

Changed inputs supersede pending/running leases, coalescing into the existing
one-row-per-deployment queue. The existing bounded worker and retry schedule
evaluate current inputs. There is no new application traffic or policy mutation.

Track each deployment's last confirmed aggregate safety state independently of
the latest check. A first/current `violated` verdict opens a violation. A later
`satisfied` verdict recovers it. `unknown`, failed work and plan/eligibility loss
do not clear it. Repeated checks in the same confirmed state do not emit events.
A recurrence after recovery produces another independently identified transition.
Successful initial checks do not emit recovery events.

Completion locks account and owned app before the queue row. Use NO KEY UPDATE
locks so concurrent child inserts can take FK key-share locks; do not lock rules
in completion, since their triggers already invalidate claims. Completion checks
current captured-contract entitlement from the central plan table. Only live,
non-deleted, owned deployments can change confirmed safety state or notify.

Create `routes.requirements.violated` and `routes.requirements.recovered` intent in
the existing app webhook event outbox inside check completion. Capture enabled,
matching app-owned recipients at the transition; account and platform-tenant
receivers are excluded. Event/source UUID uniqueness plus claim fencing prevents
duplicate intent. The existing relay and dispatcher own fan-out, signatures,
retries and replay. Later subscriptions do not receive past events.

Payloads contain app/deployment IDs, current/previous confirmed state, transition
request UUID, check timestamp, requirements revision and evidence fingerprints,
and the authenticated latest-result API path. They exclude route inventory,
private intent, rationale, rule actions and request data. Customers inspect the
existing result API for affected routes, expected/actual policy and review actions,
and use existing reviewed throttle/budget plans for supported repairs.

## Consequences

Checks cover captured contracts and configured policy, not runtime authorization.
Unknown evidence remains visible in results without a false recovery webhook.
The API path returns the latest result, which may differ from an older event;
compare event provenance before acting. Customers subscribe through existing
app webhooks and can inspect current results before relying on future events.
Migration schedules current live captures rather than replaying old verdicts.

## Validation

Memory and real PostgreSQL tests cover mutation handoffs, no-ops, tenant/app scope,
historical deployments, superseded leases, durable recipient snapshots, unknown
evidence, repeated verdicts, recovery and recurrence. API tests exercise policy
edits through the check worker and webhook subscription validation. Validate SQL
generation, SDK event types, OpenAPI parity, docs and scoped lint.
