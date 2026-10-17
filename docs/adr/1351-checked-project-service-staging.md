# ADR-1351: Temporary service readiness for checked project graphs

## Status

Accepted; native lifecycle acceptance remains a release gate.

## Context

Checked activation and rollback require ready replicas of the exact selected
service deployments. Target-pinned binding probes deliberately never wake
services. Traffic-weight service allocation can otherwise park a zero-traffic
candidate before it can be qualified. Dark promotion and GitOps qualification
already exist, but neither is a temporary readiness lease for an operator's
checked non-GitOps graph manifest.

## Decision

Add `stage`, `stage-status` and `cancel-stage` under project environment
`release-sets`, backed by an account/project/environment-scoped durable lease.
The stage request carries a complete prepared forward or rollback manifest and
an explicit 1–1800 second TTL. Every current workload, exact selected deployment,
service edge, enforced policy revision, expected current graph or initial
100% fallback must still match. Selected deployments must already be live,
materialized and non-GitOps; staging does not promote pending deployments.
Mirror recipients, active service rollouts and deactivation manifests are rejected. Historical
rollback membership and retention are rechecked. Artifact preflight precedes
intent publication. No binding evidence or activation authority is granted.

One unexpired uncancelled stage is allowed per project environment. An identical
manifest reuses its stage identity and original expiry, even with a different
requested TTL. Another manifest conflicts until cancellation or expiry.
APID alone writes this customer intent. Schedd reads the lease through the
existing service recovery loop (accelerated by deployment notifications) and increases the exact deployment target to
its configured desired count, with a minimum of one. Serving traffic-weight
allocations are preserved. Existing concurrency, residency, billing, placement,
retry leases and startup gates apply; staging grants no rollout concurrency.
Current graph and weighted-route replacements take priority over temporary
candidate allocations. Lack of spare capacity blocks staging rather than reducing
healthy serving capacity. Before each subsequent admission the controller rechecks allocations.
A boot already in progress may finish after cancellation or expiry; ordinary
reconciliation removes surplus afterwards.

Non-GitOps active graph members retain their exact desired service allocation after the
stage becomes invalid on activation or expires. This closes the handoff from
staging to durable graph routing. Cancellation never tears down an active graph
member or a traffic-bearing allocation. Function and worker members are checked
as graph inputs but receive no residency lease.

Status is an observation: waiting, ready, blocked (manifest drift, capacity or
startup/dependency failure), expired or cancelled, with per-service ready/desired
counts. It conveys no binding proof. Operators still run target-pinned `verify`
and explicit `activate` or `rollback`, which independently enforce fresh policy,
evidence, membership, artifacts and readiness at commit. Existing sticky routing
retention and weighted routes are unchanged by staging.

## Consequences

Candidate services can be qualified without publishing public traffic or
manually manipulating replica state. Staging capacity is billable and consumes
ordinary concurrency; plans at capacity can legitimately block it. GitOps holds
and checked GitOps recovery remain separate. Queue evidence still proves
consumer readiness rather than delivery or processing.

Because this changes scheduler lifecycle allocation, deployment requires the
repository's `test-metal` and `leakcheck` gates on dedicated native x86_64 Linux
KVM hosts. Control-plane and fake-vmmd tests alone are insufficient acceptance.
