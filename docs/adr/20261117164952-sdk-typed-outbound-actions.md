# ADR: Typed outbound actions with reviewed dispatch and provider reconciliation

Status: Accepted (SDK preview)

## Context

Typed pipelines can consume payment events and compensate successful SQL Jobs,
but application code still wires outbound provider calls, uncertain responses
and business authorization separately. A database transaction cannot roll back
an external refund, and provider idempotency windows are finite.

## Decision

Add matching Node/Python outbound action definitions over existing Job contracts,
pipeline tool starts, human gates, native code workflows and the managed outbound
credential relay. Persist an immutable business intent per scope/name/request
UUID, binding owner, revision, contract, configuration and exact encoded request.
Keep a separate workflow attempt identity and current access checks.

Require an exact stored human approval and an active native action run before
dispatch. Recheck backend business permission and the provider account before
consuming one acknowledged SQL compare-and-swap dispatch claim. Commit the claim
as unknown before issuing the POST. A lost claim acknowledgement, process restart
or lost provider reply never authorizes a second SDK POST. Provider idempotency
is an additional defense rather than a replacement for the retained intent.

Provide bounded read-only reconciliation and monotonic observation updates.
Start with Stripe API v1 refunds: explicit payment/amount/currency/reason, pinned
account/version, action/input metadata binding, refund retrieval and bounded
listing. Ambiguous evidence requires review. Existing verified webhook handlers
may wake reconciliation; webhook payloads cannot grant execution or directly
confirm provider state.

Expose typed preparation/current observation/reconciliation/discard MCP tools
alongside the existing workflow lifecycle. Human decisions stay in the existing
authenticated review service. Keep request encoders and decoders application
owned, and route credential injection, egress policy and budgets through the
existing relay. Introduce no native endpoint, platform migration or plan quota.

## Consequences and evidence

Applications explicitly migrate private receipt tables, retain evidence and
derive authorization from immutable business records and current access. A new
business UUID may repeat an effect and needs application policy. Code semantics
need explicit revision changes; existing definitions must remain available for
retained intents. Cancellation cannot recall an inflight external request.
Historical workflow results may differ from later confirmed action observations.

Shared fixtures and generic installed consumers cover both SDKs. Tests exercise
concurrent prepare/dispatch, current access, approval/native binding, lost SQL
and provider acknowledgements, unknown/pending outcomes, deadlines and cancellation.
The paid CSV journey verifies native signed payment ingestion, report failure,
completed approved SQL cleanup, separate exact refund approval and recovery of
one simulated accepted refund whose response was lost. PostgreSQL and Chrome are
real; native control-plane and Stripe transports are fixtures.
