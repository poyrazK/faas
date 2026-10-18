# ADR-1176: Guarded subscription capacity preview

Status: Accepted
Date: 2026-10-13
Related: ADR-1174, ADR-1175

## Decision

Add the read-only CLI command subscription-capacity-preview APP SUB --max-pending N.
Resolve the supplied app to a stable nonzero UUID, then read the existing combined
subscription delivery-health endpoint. Never send capacity, pause/resume, replay
or recovery requests, allocate UUIDs, or write a plan/report file. Use a positive
total read deadline (default existing five-second control timeout), signal
cancellation and a bounded max-age (existing watch age defaults and bounds).

Expose plan_max_pending_deliveries in the existing pressure snapshot before
clamping to the configured consumer ceiling. An effective lower ceiling cannot
establish the allowance for a proposed increase or zero-reset. Capture the plan
allowance with the other pressure fields in the same existing read transaction or
MemStore mutex. No new endpoint, schema, quota or registry entry is needed.

Before projecting, require a complete recognized pressure snapshot matching the
resolved app and supplied subscription, a positive plan ceiling, nonnegative saved
configuration and three correctly ordered consumer/app/account scopes. Validate
positive effective limits, nonnegative nested live counts, at-limit flags, the
configured/plan effective-ceiling relationship and current first blocker. Require
nonzero, non-future observation time within max-age. Missing fields from older
servers, stale evidence or malformed/inconsistent replies produce an error rather
than inferred readiness. Advice/waiting-history fields do not drive projections.

Validate the proposed integer against zero through the observed plan ceiling.
Zero projects the plan default; a positive value projects itself. Out-of-plan
proposals yield valid=false, a validation error, and no projection. Valid proposals
show the effective consumer ceiling, available consumer slots when below the
ceiling, and how many current consumer deliveries must drain to fit one additional
ordinary delivery. At exactly the ceiling, one delivery must drain; lowering never
implies eviction. Current pending plus dispatching trusted slots remain the usage
basis, including replay descendants and excluding untagged legacy work.

Recalculate the first capacity blocker in consumer/app/account order with the
proposed consumer ceiling. List every currently full shared app/account ceiling
separately, including those obscured by a consumer blocker. Raising a subscription
limit cannot remove shared limits. capacity_only_ready reflects only this count
projection; other gates and duplicate/cancellation/keep-latest exceptions remain
outside the preview, as do write permission/MFA admission and future state changes.

Output human or JSON reports with read_only, advisory, valid, requested value,
validated current pressure and optional projection. Exit 0 for a valid projected
proposal even if draining/shared pressure would block new admission; exit 1 for
invalid proposals or unavailable evidence, and 130 for interruption during reads.
No value is applied. Use the existing subscription-capacity command separately
with --yes; it still revalidates current plan limits and server admission remains
authoritative. No capacity reservation, immediate-delivery or ordering guarantee.

Add the plan allowance to Go/Node/Python pressure models. Older clients can keep
using existing diagnostics; this preview requires a server exposing the new field.
Validate builds, SDK generation/parsing, SDK coverage and static checks without
adding or running local tests. All compliance registries remain unchanged.
