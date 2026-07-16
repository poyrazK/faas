# CLAUDE.md — agent guide for the one-box FaaS platform

Scale-to-zero FaaS on Firecracker microVMs, one Hetzner EX44, solo-operated.
Customer apps park as snapshots on disk and wake on request in <350 ms.

## Source of truth (in order)

1. `docs/faas_implementation_spec.md` — THE spec. Section refs below (§) point here.
2. `docs/adr/` — decisions made after the spec. ADR-001..010 are inlined in spec §3.
3. `ex44_faas_financial_model.xlsx` — business numbers. Never contradict it in code or docs.

Any deviation from the spec requires a new ADR in `docs/adr/` (format: §3). Do not
silently "improve" the architecture — several designs below look simplifiable and are not.

## Commands

```
make bootstrap      # idempotent host setup (ansible) — only on a dev EX44
make test           # unit tests, must pass on any machine, no KVM needed
make test-metal     # integration tests tagged //go:build metal — needs KVM + root
make leakcheck      # asserts zero leaked netns/TAPs/jail uids/cgroups after tests
make lint           # golangci-lint + custom checks (see Conventions)
```

Go ≥ 1.23. One binary per `cmd/` dir. If a change touches VM lifecycle, run
`test-metal` and `leakcheck` before calling it done.

## Repo map

```
cmd/{apid,gatewayd,schedd,vmmd,builderd,imaged,meterd,faas}   daemons + CLI (Go)
pkg/{api,state,fcvm,netns,oci,rootfs,meter,stripex,wire}      shared libs
pkg/api/limits.go     EVERY plan quota/limit lives in this one table — never inline a limit
guest/init            static Go PID1 inside every microVM
guest/runners/{node22,python312}                              function runner shims
images/               Dockerfiles for base/runner/builder images
migrations/           goose, numbered, append-only (never edit a merged migration)
deploy/{ansible,systemd,nftables}
docs/adr/
```

## Component ownership (do not blur these)

- `schedd` is the ONLY writer to `instances` and owner of the state machine (§6).
- `apid` is the ONLY writer to customer-intent tables (apps, deployments, domains).
- `vmmd` is the ONLY component that touches firecracker/jailer, and the only root one.
- `gatewayd` is the ONLY public listener on the box.
- Components talk via Postgres rows + `pg_notify`, or gRPC on unix sockets in /run/faas/.
  Never add a direct call that bypasses an owner (e.g. apid must not call vmmd).

## Invariants — enforce with property-based tests, never delete (§6.2)

1. ≤ `max_concurrency(plan)` instances of an app in {WAKING, COLD_BOOTING, RUNNING}.
2. Σ(ram_mb + 8) over live instances ≤ 47,600 MB (85% of 56 GB tenant budget).
3. An app always has a live snapshot OR a cold-bootable rootfs — never neither.
4. A parked app consumes zero resident RAM (its cgroup must be gone).
5. Two instances restored from one snapshot never share IP, netns, jail uid, or RNG stream.

## Things that look wrong but are load-bearing — DO NOT "fix"

- **Two-drive rootfs** (§4.6): drive0 = shared read-only base, drive1 = per-app layer,
  overlayfs in guest-init. Flattening to one rootfs per app duplicates ~150 MB of base
  per app and breaks the 130 MB/sandbox disk economics. Never flatten.
- **Identical inner network world** (ADR-009): every guest is 10.0.0.2/30 behind tap0
  inside its own netns. This is what lets one snapshot restore as N instances. Per-VM
  guest IPs would break snapshot reuse.
- **Builds run inside ephemeral builder microVMs** (ADR-003), never in host containers,
  never with host docker/buildkit. The VM boundary IS the resource cap and the sandbox.
- **Cold boot must always work** (ADR-005): snapshots are cache, not truth. Snapshots are
  pinned to the Firecracker version that made them (`snapshots.fc_version`); on FC
  upgrade they go stale and apps lazily re-snapshot via cold boot. Never make wake
  depend on a snapshot existing.
- **Billing uses plan RAM + 8 MB per running second** (§4.7), not sampled RSS. Sampled
  RSS is telemetry only. Customers get predictable bills; the model's math depends on it.
- **Builder slots**: 1 guaranteed (inside the 6 GB control-plane slice), 2nd opportunistic
  only when tenant residency < 60%. Builds never outrank tenant wakes.

## Hard limits (source: financial model §1; encoded in pkg/api/limits.go)

Plans (deployed/concurrent/RAM MB/GB-h): Free 1/1/128/5 · Hobby 5/2/256/50 ·
Pro 25/5/512/250 · Scale 100/20/1024/1500. Overage €0.01/GB-h. Source tarball ≤100 MB
(≤250 MB Pro+). App layer ≤256 MB/512 MB/1 GB/2 GB by plan. Build VM: 2 vCPU, 2048 MB,
10 min. Idle timeouts: 30/60/300/600 s. RAM admission ceiling: 47,600 MB.
Fleet snapshot average target: 130 MB — `snapshot_fleet_avg_mb` alerts at 160.

## Conventions

- Errors: wrap with `%w` + operation context. API errors: RFC 7807, stable `code`,
  limit errors include limit + observed value + docs URL.
- Money: integer cents/millicents. Floats near money fail review.
- SQL via sqlc only; no string-built queries. Every state column has a CHECK.
- Handlers ≤ 50 lines — extract. Table-driven tests. No global state except wiring.
- Logs: `slog` JSON. Metrics: Prometheus, names as specced in §12 — dashboards depend
  on exact names (`gateway_wake_latency_seconds`, `snapshot_fleet_avg_mb`, …).
- Never log secret values; env secrets are sealed at rest (gap G2 lean, §17).

## Security rules (ship-blocking, §11)

- cgroups v2 only; host `unprivileged_userns_clone=0`; nothing runs as root except vmmd.
- Every VM via jailer: unique uid/gid (20000–29999), chroot, seccomp, cgroup scope
  `memory.max = plan + 8 MB`.
- Tenant egress: deny 25/465/587, deny RFC1918 + link-local + metadata ranges.
- No shared host directories with guests — block devices only. virtio-rng always attached.
- Post-restore resume hook must re-seed entropy + step clock before readiness (test V6).

## Workflow

- Work milestones **in order** (M0→M8, §14). A milestone is done when its acceptance
  tests pass — they are listed in §14 and are executable, not aspirational.
- Small PRs (reviewable in ~10 min). PR description names the milestone; architecture
  changes name an ADR. New quota/limit → add to `pkg/api/limits.go` + docs, never inline.
- Open gaps live in §17 (G1–G7) with decided leans and deadlines; validation experiments
  in Appendix D (V1–V10). Check both before designing something "new" — it may already
  have a decided lean.

## Gotchas

- Firecracker snapshot restore is slow on cgroups v1 hosts — we require v2; don't waste
  time debugging restore latency on a v1 dev box.
- `test-metal` needs /dev/kvm and root for netns/jailer; standard CI runners: check KVM
  availability before assuming.
- Restored guests wake with stale clocks and duplicated RNG state — the resume hook
  (guest-init) handles it; if wake tests flake on TLS/UUID collisions, that hook broke.
- Jailer chroots live in tmpfs (`/srv/fc/jail`) — anything written there by mistake
  disappears on reboot and eats RAM until then.
- The gateway holds requests during wake (queue cap 512/30 s). Load tests that fire
  before readiness are testing the queue, not the app.
