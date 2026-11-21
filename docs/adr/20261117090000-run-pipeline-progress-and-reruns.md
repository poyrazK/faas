# Live Run pipeline progress and explicit failed-branch reruns

Status: accepted for default-off preview; scoped control-plane qualification passed.
Production activation remains separate. See [the evidence](../ops/evidence/20261117-run-pipeline-progress/README.md).

## Decision

Store a bounded, metadata-only progress journal on the pipeline sidecar. Worker
updates and terminal completion commit the snapshot and monotonically numbered
events atomically. Repeated observations do not append duplicate events. Retain
256 events with an explicit cursor gap and current snapshot; reject future
cursors. Events describe observed transitions, not a guarantee to capture every
physical Run transition. Existing Run event streams retain logs and finer detail.

Expose authenticated cursor pages and optional SSE by pipeline ID, plus CLI and
Go/Node/Python watches. The pending submission response exposes its pipeline ID.
Reads retain account/key-family isolation and remain available with writers off.

A rerun creates a distinct workflow and receipt. The client supplies the original
manifest; verify its normalized digest before opening task definitions. Terminal
plans remain erased. Recompile exact task versions and compare immutable pins.
Rerun all unsuccessful steps and every affected descendant; reuse other successful
receipts. Every previously started affected step requires an explicit label in
`repeat_steps`, including successful fan-in that observed a failed predecessor.
This acknowledgement does not make external effects idempotent.

Reuse is limited to 24 hours after the producing Run finishes. It requires an
owned, successful receipt, valid contract verdict, matching
output fingerprint and intact artifacts. Stored artifacts must remain ready, unexpired,
not revoked, and owned by the same key family and producing Run. Recheck during
continuation; ordinary file admission rechecks actual handoffs. Do not copy result
or artifact bytes to the policy, manifest or progress journal. Only new physical
attempts consume execution budgets/usage. Keep parent and reused Run references
in the child receipt and completion. Reused usage is not counted again.

An exact child replay precedes current catalog/output availability and writer
checks. Different intent under its workflow ID conflicts. Atomic child creation
also verifies the same-family immutable terminal parent. Existing budget, task
retirement, file and cancellation fences apply to every new attempt. Agent callback
pipelines require a new delegated tool invocation; this account/Run rerun API does
not inherit a conversation callback or grant new Agent authority.

## Compatibility and rollout

Expand the sidecar with a bounded journal. New submissions mark progress v1.
New claim statements set a transaction-local protocol marker; a database trigger
skips incompatible old claimers for marked jobs. This protects reused work from
old workers and avoids silently terminalizing plans they cannot decode. Maintain
capable workers while these pipelines drain. Existing terminal/read contracts and
legacy progress-free policies remain valid. Gates stay off by default.

No guest or VM lifecycle protocol changes. Qualify cursor replay/gaps, worker and
API reconstruction, cancellation, stale leases, parent isolation, affected fan-in,
output expiry/revocation, idempotent rerun admission, retained-plan erasure and
new-attempt usage settlement on memory/PostgreSQL stores and the internal node.
