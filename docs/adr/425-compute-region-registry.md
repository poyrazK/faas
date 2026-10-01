# ADR-425: Durable compute region registry and admission diagnostics

Date: 2026-10-01

Status: Implemented as an internal capability; native fleet qualification pending.

## Context

ADR-424 makes an app's compute region immutable and fences execution, migration,
and recovery to matching nodes. Its discovery endpoint infers regions from
active node labels. A whole-region outage therefore removes the region from
discovery while its apps remain pinned there. Operators also lack a durable
way to pause new app admission independently of node lifecycle.

## Decision

Persist `compute_regions` with a canonical immutable `id`, a human-readable
`display_name`, `accepting_new_apps`, and creation/update timestamps. The
migration backfills canonical labels from every registered compute node,
including inactive nodes, and every app, including tombstones. Empty and
noncanonical legacy node labels remain outside the catalog.

New node registration does not create or change registry policy. Operators
must declare a new region explicitly. There is no region deletion API.
PostgreSQL rejects identity changes and deletion of a region referenced by an
app or node. No new region quota or reserved capacity is introduced.

`GET /v1/regions` lists the durable catalog in identifier order, including
regions with no active nodes. It preserves `active_nodes` and adds:

- `display_name` and `accepting_new_apps`: durable operator policy.
- `registered_nodes`: every node carrying that region, including inactive nodes.
- `eligible_nodes`: nodes passing the chooser's static lifecycle, active flag,
  RAM ceiling, and vCPU budget checks. This does not promise request headroom.
- `availability`: `available` when at least one node is eligible; otherwise
  `unavailable`.
- `healthy_nodes`: eligible nodes with a heartbeat within the existing
  `state.DefaultHeartbeatStaleness` window and not in the future.
- `observed_health`: `healthy` when all eligible nodes have fresh heartbeats,
  `degraded` when only some do, `unknown` when eligible nodes have never reported
  a heartbeat, and `unavailable` otherwise.

Availability follows the existing node lifecycle authority. Heartbeat health is
an observation, so it can temporarily differ while the existing reconciler
converges. Discovery does not add a second health controller or a capacity
reservation. It exposes no node identifiers, addresses, or certificates.

`PUT /v1/regions/{id}` declares complete policy. It requires the existing
allowlisted operator-session mutation chain: admin scope, MFA, fresh step-up,
same-origin protection, and idempotency. Both `display_name` and an explicit
`accepting_new_apps` boolean are required. The display name is bounded to 128
Unicode characters and rejects surrounding whitespace and control characters.
Policy changes emit `compute_region.policy_updated` audit events.

`accepting_new_apps = false` blocks new region-bound app INSERTs, including new
previews. Existing apps retain their choice and may claim an owner, admit new
instances, recover, migrate within their region, redeploy, or restore. Pausing
does not drain nodes or evict instances. Unconstrained apps retain legacy fleet
placement, including newly created apps; this policy governs explicit region
selection. The existing `regional_compute_enabled`
gate still controls explicit customer selection; persisted placement constraints
remain enforced when that global gate is off.

App INSERTs take a shared lock on the registry row before checking policy.
Policy updates take the conflicting row lock. If admission commits first, the
app exists before the pause; if the pause commits first, admission fails. The
MemStore serializes the same operations under its mutex. Direct SQL and every
app creation helper remain subject to the PostgreSQL trigger.

## Errors and clients

Regional problems include a structured `compute_region` field and a detail
naming that region:

| Code | HTTP status | Meaning |
|---|---|---|
| `compute_region_unknown` | 422 | No catalog entry exists. |
| `compute_region_admission_paused` | 503 | Operator policy blocks new apps. |
| `compute_region_unavailable` | 503 | No statically eligible node is available in the selected region. |
| `compute_region_capacity_exhausted` | 503 | Regional nodes exist, but request admission has no fitting capacity. |

Resource checks, recovery-slot ceilings, restore pressure, and owner authorization
remain authoritative. A refusal never grants cross-region overflow. The gRPC
adapter preserves the region and stable code through ErrorInfo metadata; clients
recover the correct HTTP status. SDK error wrappers retain regional context.

`gregale regions [list]` now prints policy, availability, eligible node count,
and observed health. Operators use the authenticated `gregalectl` surface:

```sh
gregalectl regions list
gregalectl regions set --region eu-fsn --display-name Falkenstein \
  --accepting-new-apps=true
gregalectl regions set --region eu-fsn --display-name Falkenstein \
  --accepting-new-apps=false
```

Both set commands declare the complete policy. Omission of the boolean fails
locally before authentication or a remote mutation. JSON mode retains the full
response for automation.

## Validation and rollout

Apply the registry migration before deploying the API and scheduler changes.
Backfill preserves existing regional apps and their node labels. Declare regions
for future hosts explicitly. Keep regional compute internal until the native
acceptance gates pass.

Portable tests cover catalog persistence, policy updates, admission versus
existing app execution, unavailable discovery, heartbeat observations, operator
authorization and input validation, CLI policy serialization, placement refusal
codes, and gRPC metadata. Shared MemStore/PgStore conformance checks policy and
existing-app admission. PostgreSQL tests exercise the shipped backfill statement,
direct SQL guards, and both lock orders for admission versus pause.

`TestNativeComputeRegionAcceptance` is a guarded metal test for a dedicated
three-worker native Linux x86_64 KVM fleet: two workers in the chosen region and
one in another region. It provisions its own app through apid and exercises cold
HTTP wakes with snapshots invalidated while admission is paused, selected-region
saturation despite remote capacity, same-region ownership recovery after stopping
the owner vmmd, and discovery/refusal during a whole-region outage. It checks the
persisted app owner and every instance against the selected region after each
stage. Cleanup restarts stopped vmmd services, restores node ceilings and policy,
and deletes the test app.

The acceptance runner requires `FAAS_REGION_ACCEPTANCE_CONFIRM=native-x86`, a
`DATABASE_URL` for the dedicated fleet, `FAAS_REGION_API_URL`, a customer
`FAAS_REGION_API_TOKEN`, and a cold-bootable HTTP `FAAS_REGION_IMAGE` whose `/`
returns 200. Supply node names in `FAAS_REGION_NODE_A`, `FAAS_REGION_NODE_PEER`,
and `FAAS_REGION_NODE_B`, with SSH targets in corresponding `FAAS_REGION_SSH_*`
variables. The chosen region must contain exactly A and PEER. Each designated
worker must have KVM and an active vmmd/schedd pair. The API gate must be enabled,
the customer must have app/deploy permission and spare app quota, and the fleet
must be dedicated to this fault drill.

Run `RUN_REGEX='^TestNativeComputeRegionAcceptance$' make test-metal` on the
native acceptance runner and run `make leakcheck` on each affected host after
cleanup. A skipped or macOS run does not qualify this capability. Native
qualification remains pending.

The down migration refuses while any region has paused admission, preventing
rollback from silently reopening that policy. Resume admission deliberately
before removing the registry; the immutable placement protection in ADR-424
remains in place.

## Next milestone

ADR-426 carries region intent through project scan, plan identity tokens,
apply/reconcile, and environment cloning. Plans expose immutable-region
conflicts before mutation. ADR-427 extends placement to standalone managed Jobs.
Regional control-plane independence, geographic ingress, data residency,
and a global app identity with multiple regional deployments remain later work.
