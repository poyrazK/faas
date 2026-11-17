# ADR: Typed Stripe webhook handoff into event pipelines

Status: Accepted (SDK preview)

## Context

Native inbound webhooks already verify exact Stripe bytes, rotate secrets,
retain provider receipts and queue app invocations. Typed SDK event outboxes and
pipeline start receipts already make business-event publication and run recovery
portable. Applications still hand-write the boundary between verified payment
notifications, current business ownership and atomic event intent.

## Decision

Add matching preview Node/Python Stripe snapshot adapters, using existing native
app delivery and management APIs. Introduce no platform endpoint, schema, quota,
scheduler or alternate Stripe verifier. Require an application callback to
authenticate the trusted delivery boundary before protected header parsing.
Validate deterministic native receipt/replay lineage and body digest, and pin
account/app/endpoint/path, API version, mode, connected account and event types.

Use shared application-owned PostgreSQL SQL, generated into both SDKs. Reserve
one receipt per account/app/endpoint/provider-event ID; adapter names cannot
bypass identity. Lock this identity and a trusted business row. Current owner
authorization runs on every handle and inspection. New deliveries commit business
SQL, validated typed event intent and a payload-free final handoff receipt in
one branded outbox transaction. Business logic must also deduplicate distinct
provider IDs for one effect and tolerate reordering. No remote callback effects.

Bind receipt configuration and body digests. Conflicting bytes or changed
bindings fail closed. Native secret rotation does not alter bindings. Receipts
store no payload or credential. Explicit ignore dispositions are durable; an
unsupported event type is ignored before local reservation. Existing outbox
workers own bounded retry/publication and payload erasure; event pipeline
receipts own potentially unknown workflow starts. Local staging does not imply
remote acceptance or workflow completion.

Use one total deadline and no implicit network retries. Unacknowledged commits
remain unknown. Inspection uses the same identity lock before a fresh
READ COMMITTED read, preserving safe cross-language recovery after a lost
commit acknowledgement. Historical inspection rechecks native scope and current
business access without requiring a still-enabled endpoint. Never replay a
callback automatically. Preserve existing callers by adding only preview APIs.

## Consequences and evidence

Applications explicitly migrate receipt/outbox schema, supply a trusted boundary
and current-owner callbacks, and drain/version bindings when changing contracts.
SDKs default to a conservative one-day outbox retry window, without overriding
native limits. Effect-level idempotency and safe receipt retention remain
application responsibilities.

Shared fixtures and unit tests cover identity, exact timestamps, duplicate IDs,
reordering, authorization, rollback, scope/version/mode pins, replay lineage,
unknown commits, deadlines and cancellation. Installed package consumers check
payload/principal types. The paid CSV example uses actual PostgreSQL and mixed
SDKs, then completes private browser approval and verifies the report with
native ingress/control-plane/event fixtures. This does not qualify deployed
Stripe, brokers, VM lifecycle or production credentials.
