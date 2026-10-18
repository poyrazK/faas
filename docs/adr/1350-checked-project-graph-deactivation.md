# ADR-1350: Checked project graph deactivation

## Status

Accepted.

## Context

Checked activation and historical rollback qualify exact project graphs, but
legacy graph deactivation remains blocked under binding enforcement. Removing
the graph exposes weighted fallback routes, whose recipients and dependency
edges must be checked before the switch.

## Decision

Add `release-sets prepare-deactivation`, reuse `release-sets verify`, and add
`release-sets deactivate`. Preparation observes a current active graph and
requires exactly one eligible materialized live 100% fallback per workload.
Missing routes and traffic splits are rejected. The manifest freezes current
routing, enforced policy revisions and exact fallback service dependencies.
`deactivate: true` is an explicit action discriminator; activation and rollback
reject it. `ttl_seconds: 0` means retirement preserves the source graph's existing
TTL rather than requesting new deployment retention for traffic-bearing fallbacks.

Deactivation checks all current policies, evidence, artifacts and service replica
readiness using existing graph qualification and both catalog fences. The store
locks the project, current graph, workloads and fallback deployments and compares
all recipients to actual 100% weighted routing. GitOps-managed workloads remain
closed. PostgreSQL grants retirement of the exact current graph, without granting
insertion of a replacement. Commit checks require the complete receipt and exact
fallback graph, unchanged weighted routing and unexpired evidence. A forward
graph grant must produce its checked replacement and cannot remove a graph alone.

The transaction retires the graph, preserves retained member access and records
an immutable `deactivation` receipt on the retired release: receipt UUID, release
UUID, completion time and every fallback app/deployment pair. Inventory exposes
this record after retirement. Returned memory-store receipts are detached copies.
The mutation uses existing API idempotency middleware; stale unkeyed retries fail.

## Consequences

Operators can return to qualified weighted routing without disabling policy.
Preparation and qualification do not switch routing, reserve capacity or wake
service targets. Clients carrying the retired graph ID keep its retention window.
ADR-1351 adds temporary readiness leases for checked non-GitOps service graphs.
Traffic split qualification and checked GitOps recovery require separate workflows. Queue coverage remains consumer readiness,
not proof of delivery or successful application processing.
