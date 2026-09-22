# ADR-207 · Bounded builder cache affinity

- **Status:** accepted
- **Date:** 2026-09-22
- **Decision:** For a short grace period, route an app's next source build to
  the compute node that completed its latest successful build; after the
  grace expires, let any healthy builder claim it.
- **Why:** ADR-190 made production artifact and BuildKit dependency caches
  reusable, but they remain node-local. On a two-node fleet, both builderd
  daemons receive the queue notification and previously raced without cache
  locality, so an unchanged or incremental rebuild could land on the cold
  node and pay the full build cost.
- **Consequences:** Existing build provenance becomes a placement hint for
  both LISTEN/NOTIFY and durable polling claims. The default preference is
  five seconds, which covers notification and polling jitter but bounds the
  penalty when the preferred node is busy or unavailable. Apps without prior
  provenance remain immediately claimable fleet-wide. Account fairness still
  ranks all rows eligible on a node.
- **Rejected alternatives:** A strict node target would make cache locality an
  availability dependency; sharing BuildKit caches through GCS would add
  transfer and lifecycle complexity to every build; larger builder VMs would
  increase the fixed beta fleet cost without addressing cache placement.

## Mechanics

`build_provenance.builder_node_id` already records the builder that completed
each successful build. Claim queries resolve the newest successful build for
the queued build's app and admit the matching node immediately. The queued
timestamp opens the claim to all nodes after `cache_affinity_grace`, so no new
durable target or cleanup state is needed.

The direct notification claim and the polling claim share the same rule. This
is load-bearing: applying affinity only to polling would leave the low-latency
notification race free to place the build on either node.
