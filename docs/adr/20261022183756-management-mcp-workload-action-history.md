# ADR-20261022183756: Management MCP workload rollout and service abort history

## Status

Accepted — 2026-10-22. Extends ADR-20261022161757.

## Context

Service/worker rollouts and service aborts already retain guarded, expiring plans,
native attempt markers and replay receipts. The first action history profile
omits them, so an agent cannot discover their original recovery handles after a
lost response. Service reverse handoff accepts intent before native readiness,
routing acknowledgements and draining complete. Worker generation activation
does not prove queue processing or delivery completion.

## Decision

Extend `gregale_list_actions` and `gregale_get_action` to `workload_rollout` and
`service_abort`, without adding tools or changing native mutation paths. Include
the reviewed service/worker mode. A retained rollout may reference the exact
retained deployment; an abort pins its exact candidate and predecessor. Allowlist
accepted deployment IDs, reverse-handoff request IDs, and positive native handoff
generations when supplied. Deployment IDs identify workload generations; do not
invent a numeric worker generation or confuse deployment revision with handoff
generation. Exclude reasons, source/configuration, rollout policies, gateway
lists, native error prose, tokens and replay keys.

Keep `not_attempted`, `outcome_unknown` and `accepted` distinct. Validate receipt
identity, scope, artifact attribution and exact recovery pairs before projecting
acceptance. An absent or mismatched receipt stays unknown after an attempt.
Accepted history never establishes native completion, traffic health, worker
residency convergence or queue acknowledgement. Supply an explicit
`gregale_get_rollout` invocation with the exact known deployment and app/environment
selectors. An uncertain rollout without a returned deployment ID has no invented
observation target; an abort can inspect its reviewed candidate even when the
response was lost. Reads perform no automatic observation or recovery writes.

Recover through the original guarded apply tool and same plan while unexpired,
with current authorization. Expired history remains inspectable without
recreating replay authority. Current immutable account/app/environment and grant
checks remain in force. Writes-disabled connections retain read access.

Persist new action kinds in an additive version-1 `workload-actions/<grant>`
namespace. N-1 validates its known kinds in `actions/<grant>` and must not receive
unfamiliar kinds there. Commit both histories and replay payloads in one encrypted,
fsynced StateStore snapshot. Current OAuth revocation cleans both namespaces.
Downgraded bridges ignore companion history; their subsequent actions appear
after upgrade only while original replay plans remain available. Stale companion
snapshots are observations, never evidence that no later attempt occurred.

Keep the existing combined 256-record bound, 24-hour retention after original
plan expiry, page bounds, and active-plan priority. On upgrade, merge independently
bounded N-1/companion snapshots and prune to the shared bound before serving.
Validate format, namespace, identities and receipt fields before reconciliation;
reject corrupt or future snapshots. No database migration or native API change.

## Consequences

Agents can recover image/source rollouts and accepted reverse-handoff intent
without replacing the original idempotency key. Catalog remains 51 read/planning
tools and 60 with writes enabled. Environment promotion/rollback and route-budget
history remain outside this profile. Native/provider qualification is separate.

Tests cover both MCP protocols, image/source service/worker receipts, redaction,
encrypted restart, lost responses, expiry, named consent changes and identity
replacement, OAuth refresh/grant isolation/revocation, malformed receipts and
snapshots, N-1 reconciliation and combined capacity. Pinned real Codex acceptance
inspects the same read-only worker plan across restart/refresh without model
requests or native deployment writes.
