# HTTP Operations preview implementation

The staged release starts with ordinary HTTP handlers under ADR-521. The
contracts and initial SQL ledger landed in #3935 and #3943. Private result
storage receipts are being qualified in #3951. Customer admission remains
disabled while the HTTP execution, ingress, and SDK slices are qualified.

## Durable state slice

The state layer commits an invocation, owner-scoped operation, idempotency
receipt, and initial event together. Equivalent JSON submissions share an
identity across definition revisions; conflicting input is rejected. Progress
and result attachments use current invocation, instance, attempt, lease, and
capability authority. Backend outcome and completion webhook delivery have
separate state, backed by the existing webhook outbox.

An expired uncertain dispatch enters reconciliation. Confirmed outcomes and
an explicitly authorized safe retry have fenced recovery receipts. Active work
keeps its identity and code through long waits. Separate private code pins and
owned release references protect cleanup and rollout without enlarging public
revision grants. File cleanup receipts survive owner deletion.

This slice adds transactional methods and invocation lifecycle hooks. HTTP
route registration, admission configuration, manifest integration, result
copy/download services, and frontend SDK methods belong to the following
slices. A state-layer pass alone does not qualify a customer preview rollout.

## Managed-operation compatibility

[PR #4133](https://github.com/poyrazK/faas/pull/4133), reviewed at
`6d79fa09b18e1d3e8603e04eb4a7b7cfab781fa8`, adds managed operation business
transactions and named webhook effects. The HTTP preview preserves its
execution contract:

- Customer claims carry `X-Gregale-Customer-Operation-Id`, plus the customer
  attempt and capability headers. Managed exclusive execution keeps
  `X-Gregale-Operation-Id` and its result-version negotiation.
- The ordinary HTTP adapter validates the complete response body. It does not
  implicitly unwrap `gregale_operation_result` or create a managed exclusive
  operation from a customer operation identity.
- Completion notification and named business effects can share webhook
  transport while retaining their independent authorization and delivery
  policies. Retrying delivery does not regenerate a business result.
- A future adapter must explicitly associate the customer operation with a
  managed backend receipt, negotiate its result contract, and retain its
  uncertainty semantics. Customer-database transaction helpers do not infer
  this association from a frontend operation header.

Acceptance tests cover the header boundary and preserve the underlying
completed invocation when a managed envelope violates an ordinary output
schema. The subsequent ingress and SDK slices must also qualify reserved-header
stripping and negotiated result handling.

## Source and qualification

The HTTP state methods and eleven acceptance suites were extracted from
`fbbde67fda1daf4e396b407b95dd692de49fca1c`, then reconciled with main. Private
code-retention hardening and its tests come from
`e0ee42d8df5fb5bb782ea5ee890ffd8e9c0a3138`. Only HTTP-compatible named queries
and state methods are included. SQLC generates all database bindings.

Before landing, the final source needs PostgreSQL/MemStore race acceptance,
migration replay, existing invocation/rollout regression checks, SQLC drift,
static validation, and fresh applicable GitHub CI against main. Before enabling
the preview, real-handler HTTP and SDK acceptance must pass with the default
admission switch off, a bounded opt-in path, and documented rollback.

The original full implementation worktree remains preserved with its pending
main merge. Native Job and workflow adapters, KVM qualification, and leakcheck
remain part of that separate paused release scope.
