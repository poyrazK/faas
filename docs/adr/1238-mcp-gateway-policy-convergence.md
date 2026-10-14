# ADR-1238: MCP gateway policy convergence before promotion

## Status

Accepted.

## Context

An atomic stored-policy fingerprint condition prevents promotion against changed
intent. Gateways may still have stale edge-rule caches. An app-owned wildcard
rule can affect another app, so the existing app-scoped revision is insufficient.

## Decision

Reuse durable edge-rule replay watermarks and fresh serving-gateway observations.
Add optional `edge_rules_scope=fleet` to runtime policy status; all other component
scopes and legacy requests remain unchanged. This is deliberately conservative:
unrelated fleet writes can delay an MCP release. No rule bodies or other account
identities are returned by the status API.

An access audit waits at most ten seconds, bounded by its caller's deadline, for
a positive fleet revision with nonzero serving gateways, all applied, and no
pending or stale observations. It then reads account rules again and requires
the original fingerprint. Missing/unsupported status or absent revision blocks
promotion as unknown; changed policy fails. Shared audit wiring applies this to
deploy, task-stack promotion, and recovery, before their traffic PATCH.

## Consequences

No extra gateway reporting pipeline or database migration is required. Retention
keeps the newest edge-rule ledger entry so pruning cannot erase the fleet cursor. Control
planes must support the new scope before using the upgraded audit. An empty
ledger is unverified, including guest-only installations without a recorded rule
mutation. Operators need real gateway replay evidence; the gate cannot manufacture
it from stored configuration.

Replay is cache invalidation/observation evidence, not token enforcement, JWKS
availability, or runtime attestation. Membership and freshness can change after
the observation. The existing atomic stored-policy condition remains necessary
and does not make gateway observations transactional with traffic promotion.
