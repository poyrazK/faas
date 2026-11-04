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

For the callback-receipt rollout, quiesce inbound webhook delivery and drain
in-flight requests before applying the new migration. Keep delivery quiesced
until every API replica understands callback receipts, then resume it. Older
API replicas cannot write or honor these receipts, so a mixed-version ingress
window must not accept provider traffic. Existing callback deliveries whose
bindings and payloads were already removed cannot be reconstructed.

Ordinary inbound invocation admission and exclusive-operation admission do not
yet share the callback endpoint lock. When changing an endpoint between those
routes and callbacks, also stop ingress and drain in-flight requests before
changing the binding. Exclusive-operation receipts are not yet consulted by
the callback path; avoid reusing a provider event across those routes until
that receipt is fenced as well.

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

## Amendment: webhook size and failure-pause handling

The 32 KiB filter limit applies to a normalized JSONB-compatible filter,
including numeric expansion. The binding request limit is 200704 bytes, which
allows a supported filter to round-trip through JSON encoders that escape HTML
characters. This replaces the original 64 KiB binding-request limit.

All newly accepted webhook envelopes, including ignored deliveries, must fit
the workflow event envelope limit after JSONB numeric expansion. Previously
retained receipts remain available for duplicate retries. When a webhook
preparation error follows a newly detected failure threshold, PostgreSQL
commits the failure pause before returning the validation error.

Verified workflow callbacks use a separate per-account acceptance budget from
new webhook starts, so a burst of starts cannot consume completion capacity.
Callbacks remain available to settle existing runs during account suspension.
Previously accepted automation receipts retain precedence after rebinding.
Each callback also has a bounded check budget. Once its durable completion is
visible, provider redeliveries use that callback's budget rather than consuming
the account-wide capacity reserved for other completions.
Callback acceptance holds the endpoint and binding while checking prior
automation receipts and completing the run. A compact callback receipt stores
the provider event identity and body hash for 30 days, so a provider retry
after rebinding recovers the callback outcome instead of starting an automation
or an ordinary invocation. The receipt does not retain the webhook payload.
