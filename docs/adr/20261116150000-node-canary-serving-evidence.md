# ADR-20261116150000: The node canary accepts an instance the node started since it activated

- **Status:** proposed
- **Date:** 2026-11-16
- **Related:** the per-node canary in `deploy/scripts/production-release-acceptance.sh`
  (rc.301, rc.309, rc.345 notes there), cd-compute's "Prove the activated node
  serves and builds through production ingress"

## Context

After each compute node activates, cd-compute's canary deploys fresh
acceptance apps until `gregalectl release-acceptance verify-node` sees the node
both **serve** one (a canary app's instance running or parked there) and
**build** one (a canary build's provenance names its builder). Twenty minutes
or six deployments without both fails the rollout.

The build half was made reliable on 2026-11-15: canary rounds submit two
fresh-source builds at once, so the activated node's builderd claims one.

The serve half depends on placement, which sends a new app to the node with the
most admission headroom. Rolling the first node drains its apps onto the
second, so when the second node's canary runs it is usually the fuller one:

| Release | Node | Canary result |
|---|---|---|
| rc.345 | fsn-3 (second) | served, never built (fixed by fresh sources) |
| rc.347 | fsn-3 (second) | built, served none of six: fsn-3 held 119 apps and 1.9 GB headroom, fsn-2 was empty with 6.0 GB |

Both nodes were healthy and serving customers. Because a finished activation
cannot be re-run, each failure left cd-platform red, skipped the fleet
acceptance and post-deployment monitoring, and could only be cleared by a
fresh rollout that drains both nodes again.

## Decision

`verify-node` takes `--since` (RFC 3339). When no canary app's instance is on
the node, an instance on that node created at or after `--since` and now
running or parked also proves it serves. The canary passes the time it starts,
which is after the node activated, so such an instance was booted or restored
by the release under test. The report names its evidence
(`instance_evidence`: `acceptance` or `node`).

The build half is unchanged: only a canary build counts.

## Consequences

- The second node no longer fails its canary because placement preferred the
  emptier node, as long as it starts any instance during the canary window.
  A node that starts nothing in 20 minutes, and gets no canary app either,
  still fails.
- Node evidence proves vmmd, restore and schedd on the node with the new
  release; it does not prove that a fresh deployment's first boot lands there.
  The fleet acceptance after the last node still requires placement coverage
  of every active node.
- `--since` is optional, so a release's script and its own `gregalectl` stay
  consistent; older scripts keep the stricter check.
