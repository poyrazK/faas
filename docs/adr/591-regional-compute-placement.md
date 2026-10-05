# ADR-591: Regional compute placement

Date: 2026-10-01

Status: Implemented as an internal capability; native fleet qualification pending.

Discovery and admission policy are extended by [ADR-592](592-compute-region-registry.md).

## Context

Gregale already stores optional region and zone labels on compute nodes. The
placement chooser uses `PreferredRegion` as a soft latency hint, snapshots fan
out within a region, and private network peering checks region compatibility.
These mechanisms do not preserve a customer's choice of compute region.
Ownership claims, overflow, recovery, and live migration can select any eligible
node in the shared fleet. Fleet manifests also cannot supply locality to vmmd
self-registration.

The first milestone provides an explicit compute location for a standalone app.
It prepares the placement contract needed for later regional control planes
without changing the existing single-owner application model.

## Decision

Add immutable `apps.compute_region`, with an empty default for existing apps.
`POST /v1/apps` and the org-scoped equivalent accept `compute_region`; app reads
return it. Empty values retain unconstrained fleet placement. Nonempty values
must be canonical identifiers of at most 63 lowercase ASCII letters, digits,
or hyphens, without a leading or trailing hyphen.

`GET /v1/regions` lists canonical region labels on active registered nodes,
sorted by identifier, with active node counts. This is a view of the current
fleet, not a durable region registry or a capacity reservation. Creating a
region-bound app requires an active registered node in the selected region.
If that region later becomes unavailable, the choice remains persisted.

New region-bound app creation requires `regional_compute_enabled = true` in
apid's TOML configuration. It defaults to false. Turning it off stops new
explicit region choices; scheduling and recovery continue enforcing choices
already stored. Region discovery remains available for operator diagnostics.
The product capability stays internal until native acceptance is complete.

The scheduler derives `RequiredRegion` from the persisted app, before capacity,
owner affinity, preferred-region latency, or snapshot locality scoring. It
applies to ownership claims, wakes, deployment priming and smoke instances,
warm pool placement, and scaling. Pressure transfers and overflow peers are
filtered too. Region exhaustion returns the existing capacity refusal; no path
may spill into another region. Live migration checks the destination before
pausing the source. PostgreSQL rejects mismatched ownership and instance writes,
and the in-memory store preserves the same contract. Recovery's conditional
transfer query rechecks region compatibility.

Region is node identity. A node carrying a region referenced by any region-bound
app cannot be relabeled, including an idle peer selected by an in-flight
migration. App tombstones also retain the protection throughout the restore
window. Owner and instance writes lock the destination node so admission
serializes with relabeling. Retire and replace nodes when changing location;
do not reuse an in-service identity for a different region. Zones remain
descriptive metadata in this milestone.

`gregale regions [list]` exposes discovery. `gregale deploy --region eu-fsn`
sets the region when creating a standalone app, including source-ref deploys.
Redeploying with a different region fails before configuration changes; omitting
the flag preserves the choice. Existing unconstrained apps cannot be repinned.
Create a new app to choose a different region. ADR-593 extends project deploys
and their scan/dry-run/diff plans with locality. ADR-595 carries placement
through standalone diff and local simple-app plans. Workflow snippets accept
region selection only with a verified supporting deploy Action release (ADR-595).
Pull-request previews derived from a region-bound app inherit its region.

The dashboard's GitHub app-creation wizard offers the same optional choice when
the operator gate is enabled. It uses the registered region catalog and current
admission observations, then runs the shared API admission preflight and atomic
creation guard before binding the repository. Form errors, installation reloads,
and GitHub connection returns preserve the selected value, including a selection
that becomes unavailable. A failed or disabled catalog cannot silently change a
regional submission into automatic placement. App list and detail pages show
the persisted region even when new regional creation is disabled; legacy empty
values are labeled "Automatic placement". Region remains immutable.

Operators declare `region` and optional `zone` per fleet host. The renderer
writes them into that host's vmmd `[compute_node]` block, and self-registration
persists them. Omitting optional labels on a subsequent registration preserves
the existing node metadata.

## Boundaries

This is a compute placement guarantee within the existing shared control plane.
PostgreSQL, API admission, gateways, builders, artifact storage, managed data,
secrets, and observability retain their existing topology. It does not establish
data residency, regional control-plane independence, geographic ingress routing,
cross-region replication, automatic regional failover, or an active-active app.
ADR-594 extends compute placement to standalone managed Jobs.

Account quotas, billing identity, per-node admission ceilings, service recovery
capacity, snapshot compatibility, and cold-boot fallback remain authoritative.
Choosing a region grants no additional quota or reserved capacity. Snapshots
remain a cache; a cold boot must work when no compatible replica exists.

## Rollout and qualification

1. Apply the migration. Defaults leave existing apps unconstrained.
2. Deploy the region-aware state and scheduler code to every compute host before
   enabling app creation. Configure and verify host labels through vmmd
   self-registration. Upgrade apid and clients.
3. Qualify the fleet on native Linux x86_64 with KVM. Keep the operator gate off
   until the checks below pass.
4. Enable `regional_compute_enabled` in apid for the qualified fleet.

Example fleet configuration fragments:

```yaml
fleet:
  hosts:
    - name: worker-fsn.faas
      role: compute-only
      address: 10.42.0.2:50051
      region: eu-fsn
      zone: eu-fsn-a
daemons:
  apid:
    regional_compute_enabled: true
```

```sh
gregale regions
gregale deploy --name regional-api --image "$IMAGE_DIGEST" --region eu-fsn
```

Portable coverage exercises canonical identifiers, manifest/rendering and
self-registration, API gate and validation, CLI creation/redeploy behavior,
affinity and capacity refusal, pre-pause migration refusal, and shared MemStore
and PgStore ownership/instance/migration/recovery conformance. PostgreSQL tests
also exercise direct SQL bypass attempts and both admission/relabel lock orders.

Native acceptance must deploy across two labeled regions, cold-wake and restore
with missing snapshots, scale and refill warm pools, saturate the selected
region, drain an owner, and verify recovery and migration only use same-region
peers. Repeat with the selected region wholly unavailable and verify refusal
without remote execution. Inspect `apps.node_id` and every serving
`instances.node_id` against `compute_nodes.region`. Run the affected
`make test-metal` suite and `make leakcheck`; a macOS test run does not qualify
Firecracker behavior or WAN reachability. Native qualification is pending.

Rollback may disable new regional creation while retaining enforcement. Do not
run old schedulers against region-bound apps. The down migration refuses to
drop the constraint while any region-bound app remains, including a tombstone;
purging those apps requires a separate deliberate migration or cleanup.

## Follow-on milestones

ADR-592 adds the durable region registry, new-app admission policy, and
availability diagnostics. ADR-593, ADR-594, and ADR-595 regionalize project plans,
managed jobs, and standalone previews. Build independent regional control
planes and storage boundaries before promising regional fault isolation or
residency. A later multi-target app model can attach per-region deployments,
capacity and health to a global identity, followed by explicit ingress routing
and replicated data policies. Keep those contracts separate from this immutable
single-region compute choice.
