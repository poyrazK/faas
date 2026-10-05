# ADR-586: Transactional managed HTTP workflow steps

- Status: Accepted for implementation
- Date: 2026-10-03
- Related: ADR-081 (durable workflows), ADR-585 (transactional operation handler SDK)

## Context

Workflow HTTP steps are delivered at least once. A handler can commit business
state and lose its response before the workflow ledger records success. The
workflow retry then reaches application code again, leaving each application
to implement its own request receipt and saved-result recovery.

## Decision

Add the opt-in `managed_operation: true` property to executable workflow steps.
The scheduler derives one operation UUID from the immutable run UUID and step
name. It sends the current workflow attempt as the operation generation over
the authenticated scheduler-to-gateway dispatch. The gateway admits the active
run and attempt, verifies the flag in the saved workflow definition, and
derives the account identity from the owning app. Customer request headers
cannot choose the operation identity or account scope.

The handler uses the existing Node, Go, or Python operation transaction SDK.
The customer database receipt makes business writes and the saved response
atomic. A later workflow attempt uses the same operation ID and input, with a
new generation, so the SDK can replay the committed response. Gregale validates
the negotiated response envelope and stores only its `result` value as the
workflow step output.

On success, one platform transaction verifies the active run and attempt,
validates every effect against an enabled app-owned webhook explicitly
subscribed to `operation.effect`, inserts the effect records and signed-webhook
delivery rows, and completes the workflow step and attempt. Effect and delivery
IDs are deterministic from the operation ID and effect name. The existing
dispatcher signs, retries, and records delivery attempts; it rechecks account,
app, receiver, and subscription state before each send. The attempts API and
`gregale workflows attempts` expose effect identity and current delivery
status. Workflow effects are account/app scoped; tenant-owned receivers remain
outside this slice.

The customer database transaction and Gregale's platform transaction are
separate. If the handler commits its receipt but the platform rejects a removed,
disabled, or otherwise invalid receiver, the workflow step fails while the
business transaction remains committed. Configure and keep the receiver active
before dispatch, and reconcile that failure with application-level business
uniqueness. Delivery remains at least once, so receivers must deduplicate the
stable delivery ID.

## Consequences

Applications can use the same transaction receipt SDK for workflow handlers as
for other managed HTTP operations. They no longer need to build request
deduplication, saved-result storage, webhook signing, or delivery retries for
this path. Identity remains bound to the workflow owner account, and normal
handler authorization is still application logic. Customer-specific workflow
scope remains future work.

The feature adds a platform effect identity ledger so delivery authorization
survives workflow-run and delivery-history retention. Applications install the
existing `public.gregale_operation_inbox` schema as described in
`docs/operation-transactions.md`.
