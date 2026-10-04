# HTTP Operations preview implementation

The staged release starts with ordinary HTTP handlers under ADR-521. The
contracts and initial SQL ledger landed in #3935 and #3943. Private result
storage receipts landed in #3951 as `2fc339ede`. Customer admission remains
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

This slice adds transactional methods and invocation lifecycle hooks. The HTTP API and manifest slice adds route registration and typed SDK contracts.
Admission configuration, execution propagation, result copy/download services,
and frontend subscription helpers belong to following slices. A state-layer pass alone does not qualify a customer preview rollout.

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

Local qualification passed on 2026-10-04: 39 PostgreSQL/MemStore acceptance
groups, 102 state and migration regression groups, and 822 API groups, with the
race detector and no skips. Coverage includes replay of both new migrations,
ordinary invocation behavior, rollout and rollback, long-running retention,
and the managed-contract boundary. Linux lint, SQLC v1.31.1 reproduction and
static policy checks passed. The qualification receipts and source hashes are
retained under `operations-checks-20261001/staged-release-20261004/evidence`.

The state slice landed in [PR #4173](https://github.com/poyrazK/faas/pull/4173)
on 2026-10-04 as `b0cf1feb178ae9383438d3616fe73c40ca59c133`. Its final
source head passed 30 applicable checks, with one verified non-applicable
base-image skip; aggregate exact-package state coverage was 73.8% against the
unchanged 70% floor. Before enabling the
preview, real-handler HTTP and SDK acceptance must pass with the default
admission switch off, a bounded opt-in path, and documented rollback. The code-retention migrations use fresh generated IDs
`20261004123536799` and `20261004123650815`. They replace this slice's former
IDs claimed by preserved drafts #3973 and #3979. Those drafts must be reconciled
before landing any duplicate schema changes.

The original full implementation worktree remains preserved with its pending
main merge. Native Job and workflow adapters, KVM qualification, and leakcheck
remain part of that separate paused release scope.

## HTTP API and manifest slice

The next slice exposes immutable HTTP definitions, account-owned status and
cancellation, and tenant-owned submission, status, cancellation and event pages.
The events route also provides resumable SSE with bounded stream leases, periodic
credential reauthentication and durable cursor replay. PostgreSQL notifications
are wake hints; a missed hint falls back to polling durable state. Public status
omits original input and execution credentials. Delivery state stays separate
from the business result. Account routes use the existing MFA and scope boundary.
Only tenant bearer routes have browser CORS, without credentialed cookies.

Source deployments resolve `operations` declarations and source-local JSON
schemas from the selected immutable archive. A single archive pass retains only
the selected app's bounded schema files; duplicate files, symlinks, missing files
and invalid schemas fail the bundle. Definitions install before a build becomes
claimable. Retrying a deployment retains its immutable contract and creates new
deployment pins. Existing deployments without Operations keep their current path.

**Production admission remains closed.** The server's private admission field
defaults to false and has no environment, flag or startup configuration path in
this slice. Definition registration and customer submission return 503, and
source deployments containing selected Operations definitions fail before source
publication or deployment creation. Tests enable the private field to qualify
the contract. Retained status, events and cancellation remain available with the
gate closed. HTTP execution integration must qualify a bounded activation path
before any customer can submit work.

The canonical and embedded OpenAPI documents define these seven routes. The Go
API client and generated Node/Python SDK contracts cover them. This slice does
not add runtime reporting, result downloads, a reconciliation endpoint, native
Job/workflow declarations, a customer demo or a rollout switch.
