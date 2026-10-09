# ADR-846: Owner recovery for exhausted durable entity work

Status: Implemented locally; verification pending.

## Decision

Add `POST /v1/apps/{slug}/entities/retry` to re-arm exactly one exhausted alarm
or outbox FIFO head. Require account ownership, deploy-write/admin scope, the
existing MFA requirements, preview engine and app allowlist, supported workload
class and execution plan, no account abuse hold, and any selected customer's
active status. Diagnostic read exceptions in ADR-845 do not authorize recovery.
Customer self-service tokens remain excluded.

The request names the same namespace/key/environment/optional customer as
inspection, selects `target: alarm` with `alarm_at` or `target: outbox` with
`head_id`, and includes `expected_version` and `expected_recovery_revision`.
The inspection response adds `recovery_revision`, a SHA-256 projection of the
manifest's opaque revision. This comparison value grants no ownership authority
and exposes neither provider ETags nor lease tokens. Any manifest write changes
it, including ownership, maintenance and reservation updates. It prevents an
old retry request from resetting a later exhausted cycle at the same business
version and message identity. Clients must take all fields from one inspection.

## Bucket transaction and fencing

Recovery reads an existing manifest, checks version/revision, rejects a live
owner, verifies its committed snapshot and checks the exact exhausted target.
It clears only the selected manifest retry reservation using the existing
conditional write. It does not call Acquire and cannot create missing state.
An acquisition or concurrent writer racing the write wins or loses the same
manifest CAS; stale owners cannot overwrite the resulting revision.

Committed state, snapshot references/hash, business version, generation,
receipts, storage accounting, deadlines, pending-message identities/payloads,
FIFO order and transport acceptance receipts remain intact. Expired ownership
metadata is retained; existing expiry checks deny the predecessor authority.
Only the chosen retry budget resets. A successful commit publishes a best-effort
discovery hint; bounded authoritative sweeps repair absent hints. No guest,
VM allocation, direct receiver call or SQL delivery creation runs in recovery.

The response `{version, target, rearmed: true}` acknowledges the metadata reset,
not execution or delivery. Existing alarm/relay workers retain their separate
operator enablement and admission checks. Recovery may be staged while workers
are disabled. Accepted outbox work retains its original deduplication identity:
re-arming transport acceptance does not resend a terminal receiver delivery.

## Failures and audit

A stale observation, changed target or non-exhausted target returns 409 with
`durable_entity_recovery_conflict`. A live owner returns 503; inspect again after
release/expiry. A missing entity returns 404 without creation. Invalid request
shape returns 422, corrupt committed state fails closed with 503, and the
existing bounded request deadline returns 504. Unknown write outcomes return
503 with instructions to inspect before deciding whether to retry. Blind replay
of a successful recovery is a conflict, never a second reset.

Bodies use the central durable-entity manifest metadata byte bound; response
caching is disabled. Successful acknowledged recovery emits
`durable_entity.retry_rearmed` through the existing audit emitter with account,
app/environment/customer scope, target, version, comparison revision and head
identity or alarm deadline. Logical keys, payloads and ownership tokens are not
logged. This existing audit path is best effort and is not an atomic transaction
with the bucket commit; audit completeness after a crash or lost acknowledgement
is not promised by this slice.

Go exposes `RetryDurableEntity`; Node and Python generated invocation services
expose the same operation. The Node `retryDurableEntity` helper checks the
comparison fields and safe integer versions before transmission. Go preserves
exact uint64 versions. No SQL migration or new worker enablement flag is needed.

## Verification handoff

No tests, builds, lint, native acceptance or live bucket checks were run, per the
user's instruction. Source generation, formatting and diff review are not
qualification evidence. Work remains local; no PR or deployment is created.

Written cases cover both targets, unchanged snapshots/accounting, repeat and
same-version later-cycle rejection, active ownership, acquisition races,
conditional-write rejection, committed writes with lost acknowledgements,
missing/corrupt state, API read-scope denial and mutation admission, success
audit events, no guest dispatch, and Go/Node comparison field transport.

The testing agent should run the relevant `pkg/durableentity`, `cmd/apid` and
nested Go SDK tests, Node SDK suites including inspection/recovery and the
reservation example, and Python regeneration/import checks. Complete the
end-to-end commit → exhausted dispatch → inspection → recovery → worker handoff
flow with stable SQL acceptance deduplication, including history pruning,
receiver dead letters, hint failures/restart, cross-account/environment/customer
isolation, and old-owner acknowledgements. Native KVM/private provider
qualification remains a separate gate. Existing feature defaults remain off.
