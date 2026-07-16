# ADR-018 · schedd gRPC surface + ReportActivity ownership (M5)

- **Status:** accepted
- **Date:** 2026-07-16
- **Decision:** schedd exposes a gRPC service `onebox.faas.schedd.v1.Schedd` on
  the unix socket `/run/faas/schedd.sock` with two RPCs — `Wake(app_id)` and
  `ReportActivity([]Touch)`. The `.proto` lives at
  `api/proto/onebox/faas/schedd/v1/schedd.proto`; generated stubs are checked in
  next to it exactly like vmmd (ADR-013). gatewayd is the only caller for v1.0.
  Errors travel as `google.rpc.Status` carrying the RFC 7807 envelope via
  `pkg/grpcerr` (same as vmmd). Auth is the unix-socket DAC model — mode `0660`,
  group `faas` (ADR-015) — established with `wire.ListenOrRecreateByName`.
  schedd in turn dials vmmd's socket through the typed wrapper
  `pkg/sched.VMMClient` (ADR-014 named this "a `pkg/sched/grpcclient` that wraps
  a vmmd connection").
- **Why:**
  - (a) The gateway needs a wake path across a process boundary and CLAUDE.md's
    architecture is "gRPC on unix sockets in /run/faas/". vmmd already proved
    the whole discipline (ADR-013/014/015/016); a second control-plane service
    reuses the same Makefile `proto` target, the same `grpcerr` envelope, and
    the same socket helper — no new machinery.
  - (b) **ReportActivity ownership.** Spec §4.1 says the gateway "records
    `last_request_at[instance]` (in-memory, flushed to PG every 15 s)". Taken
    literally that makes the gateway a second writer to `instances`, which
    directly violates CLAUDE.md's load-bearing invariant: *"schedd is the ONLY
    writer to `instances`."* We keep the invariant and honour the spec's intent
    by having the gateway keep the 15 s in-memory batch and flush it **to
    schedd** over this RPC; schedd does the single `UPDATE`. The reaper already
    reads `last_request_at` to decide idle parking (spec §4.3), so co-locating
    the write there keeps read and write on one owner.
  - (c) Keeping `Wake` on schedd (not letting the gateway call vmmd directly)
    preserves the admission ledger as the single choke point for the §6.2-1/2
    invariants — the gateway must never boot a VM that skips admission.
- **Consequences:**
  - New generated package `api/proto/onebox/faas/schedd/v1` (`scheddpb`). The
    Makefile `PROTOS` glob already discovers it; `proto-check` guards drift.
  - `cmd/schedd` grows a gRPC listener on `/run/faas/schedd.sock` (mirrors
    `cmd/vmmd/main.go`) and dials `/run/faas/vmmd.sock`. The ansible `faas`
    group already includes the schedd user (ADR-015).
  - The gateway's existing `gateway.Scheduler` interface
    (`pkg/gateway/scheduler.go`) is the client seam; its production impl is a
    thin adapter over `scheddpb.ScheddClient`. `FakeScheduler` stays for tests.
  - `Wake` is idempotent: a second call for an app with a running instance
    returns that instance's address without a new boot. This is what lets the
    gateway's single-flight WakeGate coalesce 50 concurrent cold requests into
    one wake and still hand every waiter an address.
  - `ReportActivity` returns `applied` (count of touches that matched a known
    instance) so a mass mismatch is observable in tests/metrics; unknown
    instance ids are dropped silently (an instance may have been reaped between
    the gateway's last request and the flush).
  - `WakeResponse.problem` (a `google.protobuf.Struct`) mirrors vmmd's response
    and is reserved for forward compatibility; the live error path is the gRPC
    status, not this field.
- **Rejected alternatives:**
  - **Gateway writes `last_request_at` to Postgres directly.** The literal spec
    reading. Rejected: it makes the gateway a second writer to `instances`,
    breaking the single-writer invariant the whole state machine relies on.
  - **Gateway calls vmmd directly for wakes.** Skips admission control — the
    ledger (47,600 MB ceiling, plan concurrency, vCPU slots) would no longer be
    a single choke point. Rejected.
  - **A pg_notify-only wake trigger (no gRPC).** The gateway must *block the
    request* until an address exists and get that address back synchronously;
    fire-and-forget notify can't return the address on the request's own
    goroutine within the wake budget. Rejected for the request path (notify
    stays the mechanism for async state fan-out like `instance_changed`).
  - **Fold schedd's RPC into vmmd's proto package.** Conflates two ownership
    domains (vmmd = firecracker; schedd = the ledger + state machine) into one
    wire module. Kept separate; a `buf` workspace unifying them is a Gate-A
    concern (ADR-013 re-evaluation trigger).

## Re-evaluation triggers

- **Gate-A multi-host (spec §16):** the per-host unix socket becomes a TCP
  listener behind mTLS (same trigger as ADR-013/015); `Wake` may need to route
  to the schedd that owns the app's shard.
- **Cron firing (M7):** schedd fires synthetic requests *through* gatewayd
  (spec §4.3), which is the reverse direction — that stays HTTP to the edge, not
  a new schedd RPC.

## Wire shape (final)

```proto
service Schedd {
  rpc Wake(WakeRequest) returns (WakeResponse);
  rpc ReportActivity(ReportActivityRequest) returns (ReportActivityResponse);
}

message WakeRequest  { string app_id = 1; }
message WakeResponse {
  string instance_id = 1;
  string addr        = 2;              // host_ip:8080 (spec §7)
  WakeMethod method  = 3;              // what actually happened (ADR-005)
  google.protobuf.Struct problem = 4;  // reserved; live errors travel via status
}

message Touch { string instance_id = 1; int64 unix_ms = 2; }
message ReportActivityRequest  { repeated Touch touches = 1; }
message ReportActivityResponse { int32 applied = 1; }
```
