# ADR-20261104020000: MCP Task recovery after retry budget changes

## Status

Accepted; extends ADR-20261104001500 and the MCP starter parity decisions.

## Context

Lowering a worker's retry budget left already queued retries unclaimable but
nonterminal in Node, Go and Python. A resumed input or drained attempt could
also become unclaimable above the new budget. Go crash exhaustion produced
failed Task responses without the object error required by diagnostic clients.
Generated Go/Python stacks recommended a release adapter that requires Node.

## Decision

On each claim poll, settle queued retries whose attempt count has exhausted the
current budget, including delayed retries. Clear their retry and lease state.
Preserve live leases. A queued input or drain resumption continues its existing
attempt regardless of the current budget, without increasing its count. Further
failures remain subject to the new retry budget. Count these resumptions in
compatibility metrics as well. Policies must remain consistent across workers and
observers sharing a namespace.

Go failed Task views supply a generic object error when crash recovery has no
stored handler error, matching Node and Python. Retain any existing error object.

The automated native Task release adapter remains Node-only. Validate the worker
project's Node release entrypoint before control-plane requests or journal
creation. Preserve read-only journal status and recovery inspection. Generated
Go/Python instructions describe manual migrations, compatible replacements,
observer retargeting and graceful retirement instead of recommending this gate.

## Consequences

Retry policy changes no longer leave permanently queued work consuming admission
and scaling capacity. A task that started under a higher budget may finish or
resume that attempt, but cannot start another one above the new budget. No schema
migration is needed. PostgreSQL regressions cover delayed retries, live attempts,
resumptions, metrics and crash exhaustion. CLI regressions cover Node support and
early Go/Python rejection without remote writes or release state creation. Native
KVM and real OAuth qualification remain separate acceptance gates.
