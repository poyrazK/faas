# ADR-738: Production lifecycle review history

Status: Accepted

## Context

Production traffic writes retain the lifecycle decision from ADR-735, but operators
cannot inspect it through the API. A rejected attempt usually rolls back its
transaction, losing its review. Capture and release-graph state can change after a
successful rollout, so current state cannot reconstruct the original binding.

## Decision

Expose `GET /v1/apps/{slug}/route-lifecycle/history` and
`gregale routes lifecycle history APP [--limit N] [--before ID] [--json]`.
Use the existing approval-read authorization, application ownership checks and
MFA requirements. Return newest review IDs first, with an app-owned cursor,
default page size 10 and maximum 20. Cursor IDs are canonical positive int64
strings; unknown or cross-application cursors return not found.

Successful production traffic increases retain their decision and sanitized
binding evidence in the same transaction as the traffic write. Application-level
lifecycle rejections first roll back all business writes, then save their denied
review in a separate transaction. Blocked canary reviews are saved in the existing
transaction that commits only check requests and review evidence. Recovery records
are marked explicitly. Successful evaluations whose traffic transaction later
rolls back, and direct SQL writes rejected by the database guard, are not retained.
Canceled requests, deleted resources or unavailable storage can prevent saving a
blocked review; this never permits the denied traffic write.

Capture hashes, source graph IDs and original approval graph IDs are historical.
Approval summaries expose baseline/candidate capture pins and configuration hashes,
whether the review used the receipt, and current expiry/invalidation status with a
reason. Current receipt status does not constitute a fresh rollout authorization:
all revision, capture, account and routing checks still run at the traffic boundary.
Up to 20 related approvals, 64 capture bindings and 64 graph IDs per approval are
returned; omissions set `truncated`. Full successor mappings remain in the existing
approval receipt endpoint. No workload configuration, sealed credentials, routing
rule bodies, contract payloads or arbitrary stored successor snapshots are exposed.

Earlier records retain their original decisions and indicate that binding evidence
was not recorded. Do not backfill them from today's captures or graph. App/deployment
deletion retains the existing cascading history deletion policy.

## Consequences

Operators can distinguish a committed production write from a blocked review,
inspect recovery, and explain expired or invalidated approvals. Memory and PostgreSQL
stores expose the same history contract. Pagination is stable as newer reviews
arrive. Receipt statuses are evaluated at read time, while recorded graph/capture
metadata remains unchanged. An index supports app-scoped review ID pagination.
