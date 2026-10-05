# ADR-574: Start customer automations from verified Stripe webhooks

Status: Accepted — 2026-10-03.

## Context

Signed inbound webhooks durably invoke an app or complete an existing workflow
callback. Customers otherwise need an app relay to publish an event or start a
workflow. The workflow executor and durable event fanout already provide run
admission, retries, retention and inspection.

## Decision

Expose an app-owned endpoint's `automation-binding` resource through PUT, GET
and DELETE, and `automation-receipts/{event_id}` through GET. One binding selects
a published automation on the same app, an event-type pattern and an optional
event content filter. PUT requires `expected_version` and explicit
`take_over_delivery: true`; DELETE requires the current revision. Revisions
come from a monotonic sequence and survive removal/recreation without ABA.
Normal ownership, MFA and deployment-write/read scopes apply. Request bodies
permit 64 KiB, filters 32 KiB, workflow names 128 bytes and event identifiers
256 bytes, centralized in `pkg/api/limits.go`. Existing endpoint quotas bound
binding counts. Draft-only definitions cannot be bound.

A bound endpoint routes to automation starts instead of ordinary app delivery.
Callback and managed-operation bindings are mutually exclusive with this mode;
database triggers serialize their creation on the endpoint row, including
writes from older binaries. Removing a binding restores ordinary delivery for
new events. Already accepted events retain their original route. A retained
app invocation accepted before binding is never converted into a workflow by
a provider retry. Stripe remains the only supported provider in this slice.

The existing raw-body signature verifier runs before automation acceptance.
The store locks app admission/configuration and the endpoint, rechecks its
enabled state and the exact sealed secret used during verification, and checks
account eligibility, runtime availability, app maintenance, live deployment,
plan and managed integration bindings. Signature rotation during verification
forces a retry against the new configuration. A receipt deduplicates the
endpoint/provider-event pair and compares a canonical JSON hash; changed
content conflicts. Duplicate receipts retain their original timestamp and
routing decision, including after binding removal or runtime disablement.

Acceptance atomically writes the receipt, canonical event, and an outbox row
containing only the bound automation recipient with its published definition
and filter. The source `gregale.inbound.stripe.<endpoint UUID>` is platform
reserved and cannot be forged through the customer event publishing API.
The event data is the complete verified Stripe JSON body; the envelope carries
the account, event ID, type and acceptance time. Input mapping uses the existing
template language. No provider token or signing secret enters run input.

Paused/unpublished automations and nonmatching event types receive durable
ignored receipts with an empty recipient snapshot. Content filters use the
existing scheduler matcher and report `routing_status: filtered` without a run.
Pause, publication, removal and binding changes affect future admissions;
already accepted snapshots continue unchanged. API-managed manual definitions
also honor their enabled flag for webhook starts; manual sample runs retain
their existing behavior.

Schedd uses its existing event fanout worker and atomic workflow admission
receipt, shared plan concurrent-run quota, retry/backpressure and failure replay
surfaces. The captured webhook recipient identity validates against the
endpoint and automation name without changing the captured definition's
original trigger. The same published automation may still have its normal
manual, scheduled or event start. Receipt inspection links to the admitted run
and reports recipient progress. Receipts cascade when their endpoint or retained
outbox identity is removed; deduplication uses the existing 30-day delivered
event identity retention. Step effects remain at least once and require the
existing action idempotency contract.

## Rollout and rollback

Apply `20261003200000001_workflow_webhook_starts.sql`, update all apid and schedd
workers, then configure endpoint bindings. Older API workers would continue
ordinary delivery; older schedulers cannot interpret webhook recipients.
The workflow runtime gate remains unchanged. No production flag or migration is
applied by this change.

Before downgrade, stop new ingress for bound endpoints, drain/replay accepted
fanout work and complete or cancel admitted runs. Export bindings and receipts,
remove bindings to restore intended app delivery, and stop updated binaries
before the down migration. Down removes bindings, receipts and routing guards;
it retains runs, attempts and event outbox rows. Never let older schedulers
process undrained webhook recipients.

## Validation

Memory/PostgreSQL tests exercise concurrent receipts, immutable inputs and
definitions, admission deduplication, quota pressure, eligibility, pause,
rotation, routing-mode conflicts, retention and revocation. Signed API ingress
tests exercise invalid signatures, ownership, request bounds, revisions and
idempotent retries without duplicate app delivery. Go/Node/Python clients and
OpenAPI contracts cover the binding and receipt resources.
