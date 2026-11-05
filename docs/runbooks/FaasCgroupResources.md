# FaasCgroupResources

Covers `FaasCgroupMemoryNearLimit`, `FaasCgroupOOMKill`,
`FaasPostgresCPUStalled` and `FaasCgroupMetricsStale` (family
`cgroup_resources`, ADR-20261023124955). All are warnings for the daily review.

`faas-cgroup-metrics` (role `cgroup_metrics`) writes the cgroup v2 numbers to
the node_exporter textfile collector every 30 s:

- **Which cgroups:**
  - every top-level slice (`system.slice`, `faas-cp.slice`, `faas-obs.slice`,
    `faas-tenant.slice`, `faas-cp-build.slice`, `user.slice`);
  - the services inside `faas-cp.slice` and `faas-obs.slice`;
  - PostgreSQL's `system.slice/system-postgresql.slice`, the GitHub runners
    and any other `faas-*` service in `system.slice`.
- **What is recorded:** CPU, memory, IO and pressure-stall time.
- **Not covered:** tenant VM scopes.

The "Control plane resources" Grafana dashboard plots all of it.

## Symptom

- **FaasCgroupMemoryNearLimit.** A cgroup's anonymous (non-reclaimable)
  memory has been above 85 % of its `MemoryMax` for 15 minutes. Page cache is
  excluded because the kernel reclaims it before it OOM-kills anything.
- **FaasCgroupOOMKill.** The kernel OOM-killed a process in the cgroup in the
  last 10 minutes. A slice counts its children's kills too.
- **FaasPostgresCPUStalled.** At least one PostgreSQL process was runnable but
  waiting for a CPU more than half of the time for 15 minutes. Every
  control-plane daemon waits behind the database while this lasts.
- **FaasCgroupMetricsStale.** The collector on that host has not written for
  more than 5 minutes, so the alerts above are blind there.

## Check

1. Open the "Control plane resources" dashboard and select the host from the
   `{{instance}}` legend entries.
2. **Memory:** compare the cgroup's anonymous memory with its limit, and find
   its largest member:

   ```promql
   topk(5, faas_cgroup_memory_anon_bytes{instance="<host>"})
   ```

   On the control plane, PostgreSQL's 2 GB `shared_buffers` shows as file
   memory (shared memory), not anon.
3. **OOM kill:** find the process and the service that restarted:

   ```bash
   journalctl -k --since -30min | grep -i -E 'oom|killed process'
   systemctl list-units --failed
   ```

4. **PostgreSQL CPU stall:** see who holds the CPU:

   ```promql
   topk(6, rate(faas_cgroup_cpu_usage_seconds_total{instance="<host>"}[5m]))
   ```

   The usual competitors on the 2-vCPU control plane:
   - the self-hosted GitHub runners (`system.slice/*github-runner*`), at about
     90 % of a core per running job;
   - `faas-obs.slice`, during Prometheus compaction or a long dashboard query;
   - a busy control-plane daemon in `faas-cp.slice`.
5. **Stale collector:**

   ```bash
   systemctl status faas-cgroup-metrics.timer faas-cgroup-metrics.service
   journalctl -u faas-cgroup-metrics -n 50 --no-pager
   ls -l /var/lib/node_exporter/textfile_collector/cgroups.prom
   ```

## Recover

- **Memory near limit, or OOM kills in `faas-obs.slice`:**
  - Prometheus grows with head series: check
    `prometheus_tsdb_head_series` against the cardinality work in
    ADR-1236 / PR #4675.
  - Loki grows with ingest: check the Loki pipeline dashboard.
  - Raise `faas_obs_slice_memory_max` (or a unit's own limit such as
    `prom_memory_high` or `loki_memory_max`) in the operator variables only
    after confirming the host has the headroom. Every byte comes out of what
    PostgreSQL and the page cache can use.
- **Memory near limit in `faas-cp.slice`:** a control-plane daemon is
  leaking or holding a large batch. Before restarting it, capture where the
  memory is (ADR-20261023200002):

  ```bash
  sudo gregalectl debug pprof --daemon <daemon> --profile heap --gc
  sudo gregalectl debug pprof --daemon <daemon> --profile goroutine --debug 2
  ```

  Open the profile with `go tool pprof`. Then restart only that service. Raise
  `faas_cp_slice_memory_max` only when the spec §13 budget allows it.
- **PostgreSQL CPU stall:**
  - If a platform daemon is the competitor, record what it is doing first:
    `sudo gregalectl debug pprof --daemon <daemon> --profile cpu --seconds 30`.
  - Move or pause GitHub runner jobs if they are the competitor.
  - Otherwise look for the heavy statement in `pg_stat_statements` and the
    daemon issuing it.
  - Do not lower PostgreSQL's CPU weight.
- **Stale collector:** restart the timer
  (`systemctl restart faas-cgroup-metrics.timer`). If the service fails, its
  journal names the unreadable file. The cgroup v2 files it reads are
  world-readable, so a failure usually means the textfile directory's
  permissions changed: the node_exporter role owns it as
  `node_exporter:node_exporter 0775`.
