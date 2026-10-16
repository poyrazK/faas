# ADR-1362: MCP consumer discovery and retirement

Status: Accepted

## Context

Named activity consumers persist progress but cannot be discovered or removed.
The 100-consumer connection limit and shared journal budget require deliberate
capacity reclamation. Deleting a checkpoint alone reopens revision zero, allowing
a delayed initial acknowledgment to recreate retired progress (the ABA problem).

## Decision

Add a scoped read-only list tool and an explicit retirement tool. List one
immutable app/project/environment in the current account, endpoint and connection
namespace. Sort names lexically and return name/revision/update metadata only;
do not expose cursors or claim current readiness without exact consumer reads.
Default to 20 entries, maximum 50. Continuations are bounded strict JSON encoded
as base64url, bound to the target, namespace and connection generation. They are
pagination metadata, not grants. Live pages are not a frozen snapshot; retirements
invalidate continuations and require restarting the list.

Retirement checks the exact revision and generation, removes that consumer's
checkpoint, and rotates a connection-wide random generation in one encrypted
fsync/rename commit. Consumer acknowledgments must carry the generation from a
recent get/list result; compare it again under the journal mutex at commit. Legacy
journals have generation empty until the first retirement, so existing consumers
remain readable without a migration. After any retirement all consumers in that
connection require a refreshed generation, even for unrelated targets. This
bounded coordination cost prevents stale create/update requests without keeping
one unbounded tombstone per retired name. No automatic recreation or head reset
is performed; a new initial acknowledgment explicitly starts a new incarnation.

Keep only the current generation and one last-retirement receipt per namespace.
An exact retry of that retirement can return the original generation while the
consumer remains absent. Never delete a recreated consumer for an old retry.
Later retirements replace the receipt; older retries conflict and require fresh
inspection. Retiring expired progress is supported by verifying current feed
access at a fresh checkpoint rather than trying to read removed history.

List, get, acknowledge and retire revalidate current read consent, native target
identity and feed access. Journal operations also recheck durable OAuth grant
state under the store mutex. Revocation removes both consumer and generation
namespaces. OAuth cleanup does not remove local profile namespaces, which it
does not own. Local and remote generation fences survive restart using the same
encrypted state file/key. Capacity projection precedes any map or filesystem
mutation; uncertain filesystem failures disable further access until recovery.

Retirement is a destructive, idempotent bridge metadata mutation. Advertise
readOnlyHint=false, destructiveHint=true and idempotentHint=true, track it for
mutation drain grace, and require read consent rather than native write consent.
It sends no native mutation and does not undo downstream work or schedules.

## Consequences

No native endpoint, SQL, migration or background job is added. The tool catalog
remains static and discovery makes no native requests. New bounds live in
api/limits.go. Exact processing remains an application responsibility; retiring
a consumer loses that saved progress. Generations are coordination fences, never
authorization tokens or retention leases. First retirement atomically upgrades
the journal's authenticated-encryption associated-data format to v2. New bridges
read v1 and v2 and preserve v2 on later writes; older generation-unaware bridges
cannot authenticate/open v2 and fail closed. The OAuth snapshot's internal format
stays unchanged. Keep an upgraded bridge for journals where retirement was used.

Real Codex acceptance exercises consumer discovery, pagination, acknowledgment,
retirement/retry, bridge drain/reopen, client reconnect, stale initial requests,
recreation, activity replay and expiry retirement, plus native access denial.
The fixture permits bridge metadata mutations and rejects native writes. CI
retains sanitized counts/outcomes; no model turn or live account is used.
