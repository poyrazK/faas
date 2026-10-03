# ADR-457: Critical route health hold and resume notifications

- Status: Accepted
- Date: 2026-10-02
- Related: ADR-344, ADR-451, ADR-454, ADR-455, ADR-456

## Context

Critical route guards hold canary advances and retain exact decision evidence.
Customers need to learn about a held release without polling. Existing route
requirement notifications describe configured policy rather than runtime health.

## Decision

Emit app webhook events from real enforced APID canary advance evaluations:
`routes.health.blocked` when a hold opens, and `routes.health.resumed` when healthy
evidence permits a successful traffic advance after a comparable hold. An initial
healthy check, report mode, disabled selectors, a read or an abort emits neither.

Maintain one notification state per deployment independently of bounded history.
Context pins candidate/stable identity, canary step, revision, observation anchor,
mode and the captured evaluation policy. Changed context starts a new comparison
and cannot imply recovery. Repeated holds, telemetry window movement and source
changes stay quiet. Unknown-to-regressed evidence emits one additional blocked
event. Unknown evidence after confirmed regression retains that confirmed hold;
it neither emits a recovery nor reopens the same regression later.

Write history, notification state and outbox intent within the owned app/deployment
traffic transaction. A held check commits these together without traffic or traffic
audit changes. A resume commits only with its traffic advance; later audit, traffic,
lease or outbox failures roll back its history, baseline and event together. A
producer persistence failure leaves traffic unchanged. MemStore prepares all
fallible serialization before traffic mutation and publishes after success or hold.

Capture only enabled, matching app-owned webhook recipients at the transition.
Account and platform-tenant receivers remain outside these app-only events.
Track state even without recipients so later subscriptions cannot turn retries
into historical notifications. Do not backfill or replay pre-migration holds.
Reuse the existing outbox relay and signed delivery dispatcher for fan-out,
delivery idempotency, retries and replay; no new network sender or polling worker.
All new production SQL is generated through sqlc.

The versioned payload contains app/deployment/stable IDs, exact saved decision ID,
status, health status, reason, manual/worker source, stage, revision, observation
anchor, check timestamp, previous/requested traffic and an authenticated history
path. Resume includes its prior comparable blocked decision ID. Do not include
route inventory, observations, request data, arbitrary actors or secrets. Snapshot
links are subject to existing retention and app lifecycle. Events do not authorize
traffic changes or establish continuous incident start/end times.

Publish event vocabulary and typed payloads through OpenAPI and Go/Node/Python SDKs.
Customers subscribe with existing app webhook commands and inspect authenticated
saved evidence before acting. Notification state is one bounded row per deployment;
outbox/delivery retention and quotas use the existing central webhook limits.

## Validation

Pure transition and memory tests cover holds, escalation, report mode and context
changes. Real PostgreSQL tests cover restart, concurrent retries, recipient scope,
late subscriptions, history pruning, escalation, resume and transaction rollback.
API tests resolve notification provenance to saved evidence and enforce event
vocabulary. SDK round trips, generated SQL/spec mirrors and scoped lint are checked.
