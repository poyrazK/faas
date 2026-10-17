# ADR-1349: Checked rollback to retained project release graphs

## Status

Accepted.

## Context

ADR-1348 provides checked forward graph activation. Recovering a historical
project graph needs an explicit source identity, current-policy qualification,
and an atomic recovery record; deployment retention alone must not authorize
recovery of expired graph history.

## Decision

Add `release-sets prepare-rollback` and `release-sets rollback` API/CLI workflows.
Preparation selects an account/project/environment-scoped, inactive, unexpired
historical release and derives its exact complete member graph. It captures a
different current active release and the current enforced policies. The existing
`release-sets verify` command qualifies the resulting manifest, including exact
historical service recipients. History is never proof of current binding health.

Rollback reuses checked graph activation, including artifact preflight, complete
membership, GitOps restrictions, policy and managed PostgreSQL fences, fresh
binding evidence, and full service replica readiness. The store compares the
historical members and checks historical retention under the graph locks.
PostgreSQL rechecks source expiry and exact historical membership at commit.
Every failure leaves the serving graph intact.

Recovery publishes a new immutable release rather than reactivating or mutating
the historical source. Its `rollback_source_release_id` and
`rollback_previous_release_id` fields record the source graph and the graph it
replaced in the same transaction. These fields appear in release inventory and
cannot be changed. The rollback route uses the existing idempotency middleware;
callers can use the same Idempotency-Key to retrieve a completed mutation, while
an unkeyed stale manifest fails its current-release compare-and-swap.

## Consequences

Operators can recover retained project graphs without disabling binding policy.
They must reprepare when policy, membership or routing changes, and reverify when
evidence expires. Rollback does not wake or stage historical deployments and
requires a current active graph; it cannot restore expired history or perform
schema/data rollback. Generic deactivation remains blocked under enforcement;
ADR-1350 adds checked fallback deactivation. GitOps candidate staging remains a follow-up.
