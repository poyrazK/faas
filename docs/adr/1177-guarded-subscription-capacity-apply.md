# ADR-1177: Guarded subscription capacity apply

Status: Accepted
Date: 2026-10-13
Related: ADR-1174, ADR-1176

## Decision

Extend the existing capacity PUT request with optional expected configuration.
An expectation object requires explicit nonnegative max_pending_deliveries and
positive plan_max_pending_deliveries. Compare the saved configured value, including
zero for plan default, rather than the effective clamped ceiling. A saved value
above a downgraded plan remains a valid expectation. Empty or partial expectation
objects fail validation. Requests without expectations preserve legacy semantics.

PostgreSQL takes an account row FOR SHARE lock before the existing capacity mutex,
app target and subscription control locks. Hold that plan row lock until commit;
plan updates cannot interleave between reading the allowance and applying capacity.
Under the existing capacity/subscription serialization, read the current saved
setting and compare both expected values before updating it. MemStore compares
plan and setting under its mutex before changing maps or control records. Existing
legacy setter signatures remain available; a separate optional guarded-store
interface adds support without changing existing test-double contracts. Guarded
requests never fall back to an unguarded setter on unsupported stores.

Reject changed expectations with HTTP 409 and stable code
`event_subscription_capacity_changed`. No capacity/control metadata mutation is
committed. An owned target is checked before comparison, preserving cross-account
404 behavior. Basic malformed/negative requests remain 400; after matching guards,
validate the proposal against the current plan ceiling. A stale positive proposal
returns conflict before an out-of-plan error so operators refresh their review.

Preview JSON includes an expected object for valid proposals, and human output
prints the exact --expected-max-pending and --expected-plan-max-pending flags.
The existing capacity CLI accepts these together, validates their shape locally,
and sends the nested expectations with the proposed setting. --yes remains
required for apply. Neither flag means legacy apply; one flag, negative saved
expectation, or nonpositive plan expectation is rejected before authentication.
No automatic retry, fallback, new preview, replacement expectation or control is
performed after a conflict. Go/Node/Python request models expose the new object.

This is a comparison of current values, not a revision or incarnation fence.
Changes that return to identical values can pass. Scope ownership still comes
from the existing app/subscription route. The guard does not compare live usage,
pause, pacing, circuit state, or reserve capacity; those controls are preserved.
It does not make a lost response idempotently acknowledged: inspect status and
review again rather than treating a repeat conflict as success. Lowering still
leaves existing deliveries intact, and other gates/order/recovery semantics stay
unchanged. Plan updates may occur after the successful commit.

## Compatibility and validation

The existing PUT route, authorization, MFA, deadlines and response DTO remain.
No new migration, schema, quota or registry mapping is needed. Upgrade capacity
writers before sending expectations; older strict request decoders reject the
new field rather than safely supporting the guard. Only rely on server-side
guard support in upgraded writers. Plain reads/previews remain advisory.

Validate application/server and SDK builds, SDK generation and source parsing,
SQLC parity, SDK coverage and static lock/mutation ordering. Do not add or run
local tests under the user's constraint. Database concurrency remains for CI
qualification. Compliance registries remain unchanged.
