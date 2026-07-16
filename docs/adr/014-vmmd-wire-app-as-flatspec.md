# ADR-014 · M1 wire shape — caller resolves `(app)`

- **Status:** accepted
- **Date:** 2026-07-16
- **Decision:** vmmd's `CreateFromSnapshot` and `CreateColdBoot` carry an **`AppSpec`** proto on the wire containing `base_path`, `layer_path`, `vcpu_count`, `mem_size_mib`. vmmd looks up **nothing** about the app — the caller (schedd, for v1.0) translates its Postgres ledger into the flat proto before dialing.
- **Why:** (a) The spec §4.4 line 138 parenthetical `(app, instance)` is satisfied by either caller-resolved fields or vmmd-resolved fields; picking caller-resolved keeps vmmd in the "fire someone told me to fire" role that matches CLAUDE.md Component ownership (vmmd is "the ONLY component that touches firecracker/jailer, and the only root one" — adding a second writer to apid's `apps` table inside vmmd would expand the "only root" privilege). (b) Schedd already owns the `instances` table per CLAUDE.md: "schedd is the ONLY writer to `instances` and owner of the state machine" — the lookup naturally belongs there. (c) The wire is one small message per app — no caching, no pg_notify, no async invalidation.
- **Consequences:**
  - The proto file adds `message AppSpec { ... }`. No `pg_notify` subscriber inside vmmd.
  - schedd grows a `pkg/sched/grpcclient` (M4) that wraps a vmmd connection. M1 tests don't exercise that — it lands in M4.
  - A deploy that updates `image_digest` does NOT need a vmmd-side refresh; schedd reads the digest on the next wake and passes the latest image paths on the wire. vmmd's view is single-request.
  - Future vmmd-side feature "warm a VM for an app that's about to wake" (would need a vmmd-resident cache) is denied in v1.0; if it lands later, this ADR is superseded with a new one.
- **Rejected alternatives:**
  - **vmmd subscribes to apid's `pg_notify` and caches the app→image map.** Simpler on the caller surface. Rejected because it adds a Postgres-credentialed consumer to the only-root daemon and was neither spec-named nor spec-encouraged.
  - **`Fetch(app_id) -> *AppSpec` RPC of vmmd pulling from apid.** Directs an inter-daemon path through apid that spec Component ownership bars (apid must not be the target of vmmd calls).
  - **Pass a deep-link app object on the wire.** Vague. The flat fields are what schedd needs to wake; nothing else is.

## Re-evaluation triggers

- **Spec §v1.1 introduces shared-image warm pools** (one parent snapshot serving many instances). Then vmmd needs to know which `app` asked for it on the same network; promote to a `GetAppSpec(app_id)` gRPC call against an owner that does have the schema (apid).

## Wire shape (final)

```proto
message AppSpec {
  string base_path   = 1; // drive0 shared ro base rootfs
  string layer_path  = 2; // drive1 per-app layer (spec §4.6 two-drive scheme)
  int32  vcpu_count  = 3; // 2, or 4 for Scale
  int32  mem_size_mib = 4; // plan RAM; the slice fences at +8 MiB (pkg/api/limits.go)
}
```

`base_path` and `layer_path` point at the **current** images as schedd sees them; a stale schedd view at most causes a cold-boot rollback (ADR-005), never a leak.
