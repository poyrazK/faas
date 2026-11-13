# FaasNodeCPUSaturated

Warning, family `cgroup_resources` (ADR-20261109172421 §6). A compute node's
tenants have used over 90 % of the tenant slice's CPU quota for 15 minutes
while tenant tasks waited for a CPU over 20 % of the time.

Memory is the only capacity limit: schedd never refuses a wake for CPU. A
node with too little CPU therefore shows up as slower requests and this alert,
not as `503 capacity_unavailable`.

## Where the numbers come from

- **Quota:** `faas-tenant.slice`'s `CPUQuota`. On a compute node `node_join.yml`
  sets it to (host threads − `faas_tenant_cpu_reserve_threads`) × 100 % in
  `faas-tenant.slice.d/90-host-capacity.conf`. The reserve defaults to 2
  threads and keeps gatewayd, vmmd and schedd responsive.
- **Metrics:** `faas-cgroup-metrics` (role `cgroup_metrics`) writes them every
  30 s:
  - `faas_cgroup_cpu_usage_seconds_total{cgroup="faas-tenant.slice"}`;
  - `faas_cgroup_cpu_quota_cores{cgroup="faas-tenant.slice"}`, absent while
    the slice has no quota;
  - `faas_cgroup_pressure_stall_seconds_total{cgroup="faas-tenant.slice",resource="cpu",kind="some"}`.

## Check

1. How full the slice is, and how long tenant tasks wait:

   ```promql
   rate(faas_cgroup_cpu_usage_seconds_total{cgroup="faas-tenant.slice"}[10m])
     / faas_cgroup_cpu_quota_cores{cgroup="faas-tenant.slice"}
   rate(faas_cgroup_pressure_stall_seconds_total{cgroup="faas-tenant.slice",resource="cpu",kind="some"}[10m])
   ```

2. Which apps use the CPU. The node's measured instance total is
   `schedd_node_cpu_measured_millicores{node}`, and its distribution per
   instance `schedd_instance_cpu_measured_millicores`.
3. Whether it is a burst or a trend. Cron waves at :00 and :30 are short; a
   whole afternoon at the quota is a capacity problem.
4. Whether the platform still responds: gateway latency
   (`gateway_wake_latency_seconds`) and vmmd/schedd CPU in their own slices.
   The reserve should keep these flat while tenants are saturated.

## Act

- **Add capacity.** A new compute node spreads new wakes: placement prefers the
  node with the least measured CPU pressure (ADR 20261030100950).
- **Move hot apps.** Park a CPU-heavy app's instances so they wake elsewhere,
  or ask its owner to cap it (`cpu_millicores` 250 or 500).
- **Do not lower the platform reserve** to give tenants more CPU. It protects
  the request path for every tenant on the node.

## Related

- [FaasCgroupResources](FaasCgroupResources.md): the other per-slice alerts.
