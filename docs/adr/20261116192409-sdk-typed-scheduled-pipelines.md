# ADR: Typed scheduled pipelines over native workflow admission

Status: Accepted (SDK preview)

## Context

Typed pipelines already compose Jobs, approvals and child workflows and start
from business events. Native workflow schedules already own default-deployment
calendar evaluation, overlap/catch-up policy, durable occurrence history and
controlled replay (ADRs 487, 653 and 654). SDK callers otherwise construct fixed
input envelopes and coordinate schedule/run provenance manually.

## Decision

Add matching Node/Python typed schedule definitions and owner-authorized clients.
Definitions validate a generated pipeline input and bind scope, owner, contract
and calendar into a portable identity. Emit the existing native deployment trigger
with that input; introduce no local timer, scheduler, native endpoint or quota.
Add `schedule` to the existing pipeline envelope trigger discriminator, preserving
manual and event callers. Only default-scope, account-owned schedules are covered.

Every management operation reauthorizes its application principal and verifies
native account/app identity. Before calendar/history/replay operations, verify
the pinned immutable deployment and its exact workflow trigger/input, including
owner and contract. Live preview/replay additionally require the matching current
schedule; historical reads and attachment remain possible after rollout. Read bounded history, filter unrelated workflow/tenant rows,
and discover selected replay IDs only within a bounded scan. Native replay remains
the atomic admission authority and rechecks deployment/definition/quota/overlap.

Attach only through a verified occurrence's original or replay run ID, then
verify its scope, input, deployment and nominal time. Returned handles reauthorize
future reads and cancellation. Keep Python metadata timestamps as strings and
compare nanosecond instants without lossy datetime conversion. Pruned runs do
not invalidate retained occurrence inspection.

Use one total deadline and no hidden transport retries. Deadline/cancellation
before dispatch prevents replay. After possible dispatch, report an unknown
outcome with the exact selection; inspect retained history to reconcile. Do not
retry automatically. Native occurrence identity prevents duplicate replay
admission; application effects remain at least once.

## Consequences and evidence

Applications include the generated trigger in a normal reviewed deployment,
upgrade handlers for schedule envelopes, and pin a client to its resulting
deployment ID. Preview uses the deployed native calendar and is advisory.
Input remains fixed; changing daily work requires an application coordinator.
Tenant-configurable schedules and named environments remain outside this slice.

Shared Node/Python fixtures cover DST gap/fold/catch-up responses, identity,
precision, authorization, duplicate requests, unknown replay, limits and
cancellation. Package consumers verify typed inputs/results. The CSV example
qualifies one scheduled reviewed import using PostgreSQL and mixed SDKs, with
native scheduling/control-plane/realtime protocol fixtures and browser approval.
No deployed scheduler or VM lifecycle acceptance is claimed.
