# ADR-016 · M1 `Stats()` shape + `vmmd_*` Prometheus naming

- **Status:** accepted
- **Date:** 2026-07-16
- **Decision:** vmmd's `Stats()` returns a `StatsResponse` proto with `live_count`, `leased_count`, `total_resident_bytes` (Int64Value so unset != 0), and an `instances[]` array carrying `{instance, lease_uid, host_ip, state_in_bytes}` per live instance. Two Prometheus metrics ship with vmmd as the platform's first `vmmd_*` series: `vmmd_ops_total{op,code}` and `vmmd_op_duration_seconds{op}` (a histogram).
- **Why:** Spec §4.4 line 138 names `Stats()` with no payload definition — we have to commit to one before the proto is generated. §12 names dashboard rows that no daemon emits yet (`cold-boot fallback rate`, `resident_ram_pct_of_target`, etc.). vmmd is the natural emitter for the `op_total` and `op_duration_seconds` series because vmmd is the only place a `cold-boot fallback` actually happens (every wake goes through `Manager.bringUp`, `pkg/fcvm/manager.go:159`). Locking the names now means later milestones quote them by reference. The histogram is the building block for the dashboard's p50/p95 latency row once gatewayd (M4) feeds request latency too.
- **Consequences:**
  - The proto file defines `StatsResponse`, `InstanceStats`, `WakeMethod`, and the request/response messages for the five RPCs.
  - `pkg/vmmdgrpc/stats.go` reads `/sys/fs/cgroup/faas-tenant.slice/vm-*.scope/memory.current` per live instance. On non-Linux it skips with an empty `instances[]`. Reuses `pkg/fcvm/leakcheck/leakcheck.listTenantScopes` (M0).
  - `pkg/wire/metrics.go` defines two helpers wrapping `prometheus.NewCounterVec` and `prometheus.NewHistogramVec`. They register against an injected `prometheus.Registerer` so unit tests don't pollute the global registry; `cmd/vmmd/main.go` wires the real registry and exposes `/metrics` on a private port (separate from the unix socket).
  - Per spec §Conventions line 88 ("every quota/limit lives in this one table"), the constants used by handlers (e.g. timeout defaults, port numbers) come from `pkg/api/limits.go`. The two new ones (`VMMDMetricsPort = 9104`, `VMMDOpTimeoutSeconds = 30`) join that table.
  - **Read-side cost:** every `Stats()` call globs `/sys/fs/cgroup/faas-tenant.slice/vm-*.scope/*/memory.current` and reads 1 file per instance. At v1.0 max concurrency (20 Scale × 5 Pro ≈ 65 instances), the cost is <1 ms total — well under the §4.3 admission tick. If it ever becomes hot we cache.
- **Rejected alternatives:**
  - **Expose the entire `Allocator` map via `Stats()`.** Leaks the lease UID set, which is also useful to a hostile caller probing the uid pool — preferred to ship only the actively-used UIDs and the high water mark.
  - **`Stats()` returns JSON through a side channel.** Spec says it's a gRPC method; making it a separate HTTP path is unjustified surface area.
  - **Wait for M7 meterd to define `vmmd_*` names.** That pulls the M1 implementation into M7 territory and orphans the M3 latency gate's per-op timings. Better to fix the names now.

## Wire shape (final)

```proto
message StatsResponse {
  int32  live_count          = 1;
  int32  leased_count        = 2;
  google.protobuf.Int64Value total_resident_bytes = 3;
  repeated InstanceStats instances = 4;
}

message InstanceStats {
  string instance                 = 1;
  int32  lease_uid                = 2;
  string host_ip                  = 3; // netip.Addr.String()
  google.protobuf.Int64Value resident_bytes = 4;
}

enum WakeMethod {
  WAKE_COLD_BOOT = 0;
  WAKE_RESTORE   = 1;
}
```

## Metric names (final, v1.0, frozen)

| Name | Type | Labels |
|---|---|---|
| `vmmd_ops_total` | counter | `op` (`create_from_snapshot`/`create_cold_boot`/`pause_and_snapshot`/`destroy`), `code` (`ok`/`unavailable`/`invalid_arg`/`internal`) |
| `vmmd_op_duration_seconds` | histogram | `op` (same) |

Both exposed on `:9104/metrics` (default), overridable via `vmmd.toml`. The `/metrics` endpoint itself is out-of-scope for this PRD (M5 OWASP) — ADR supersedes once HTTP auth is on the table.
