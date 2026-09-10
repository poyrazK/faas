# ADR-070 — PR-B sidecar runtime shipped (issue #463 / ADR-069)

- **Status:** accepted
- **Date:** 2026-08-03
- **Issue:** #463
- **Depends on:** ADR-069 (design), PR-A (PR #531, API/storage contract)
- **Supersedes:** nothing
- **Superseded by:** ADR-071 (PR-C, billing + per-sidecar observability, planned)

## TL;DR

PR-B ships the **runtime** half of issue #463. PR-A closed the
6 acceptance criteria that didn't require runtime wiring (PR
#531, ADR-069). PR-B closes the remaining 2 plus the §"Downstream"
obligations:

- **AC #1** — `type: "init"` runs to completion before the main
  workload; non-zero exit fails the deploy with `failure_class:
  user_error`.
- **AC #2** — `type: "sidecar"` runs alongside the main workload
  inside the shared netns; the main workload can reach it on a
  customer-pinned port.
- **AC #4** — Sidecar OOMs do not kill the main workload (separate
  cgroup scopes under one instance scope).
- **ADR-069 §Decision 7** — Each sidecar is its own drive1 with no
  shared writable layer.
- **ADR-069 §"Downstream"** — `pkg/fcvm.ColdBootSpec.Workloads []`
  reshape, guest-init orchestrator boots init → main + sidecar in
  parallel under `Supervisor`, per-workload ext4 + cgroup + manifest.

PR-C (planned) owns the remaining items: `pkg/billing` consumption
of `BillableRAMMBWithSidecars`, the new
`schedd_sidecar_restart_total{app,sidecar}` Prometheus counter,
and per-sidecar gateway portnorm.

> **Note (PR-C 2026-08-04):** the counter is registered as
> `vmmd_sidecar_restart_total` (vmmd hosts the canonical
> `dispatchSidecarRestart` today); the `<daemon>_sidecar_restart_total`
> shape is preserved so a future schedd-side producer can
> host the same family under its own prefix. See ADR-072
> §"Decisions 2".

## Scope (in / out)

**In scope for PR-B:**

- `pkg/fcvm.ColdBootSpec.Workloads []WorkloadSpec` reshape (replacing
  the legacy single-`LayerKey` field).
- `pkg/fcvm.RestoreSpec.Workloads []WorkloadSpec` (snapshot path
  mirror).
- Per-workload cgroup scopes inside the guest, with one aggregate
  per-VM host fence.
- Per-workload ext4 manifest stamping at `/etc/faas/workload.json`
  on every drive (operator visibility + reverse-compat seam).
- Deployment-level roster at `/etc/faas/workloads.json` on drive1
  (the guest-init orchestrator's authoritative source).
- `guest/init` orchestrator (`runWorkloads`): init sidecars
  sequentially, then main + `type=="sidecar"` workloads in
  parallel under per-workload `Supervisor`s. Returns when every
  supervisor exits.
- `guest/init` mounts each N+1 sidecar drive read-only after pivot and
  chroots that workload into the artifact's `/upper` image tree.
- `guest/init` characterize probe filters AppPID() to main
  workload only.
- `pkg/events.WakeSidecarInitExit` + `WakeSidecarRestart` event
  types + `pkg/state.ListEventsBySidecar` read API.
- `pkg/fcvm/sidecar_metal_test.go` metal tests
  (TestMetalSidecarBoot, TestMetalSidecarPortReachable,
  TestMetalTwoSidecarsColdBoot).

**Out of scope for PR-B (deferred to PR-C):**

- `pkg/billing` and `cmd/meterd` consumption of
  `BillableRAMMBWithSidecars`. The helper lives in `pkg/api/limits.go`
  but PR-C is the first consumer.
- The `schedd_sidecar_restart_total{app,sidecar}` Prometheus
  counter. (Renamed to `vmmd_sidecar_restart_total` in PR-C
  §4 — see ADR-072 §"Decisions 2".)
- Per-sidecar gateway portnorm (today's gateway DNATs :8080 only).
- Guest-init emit-side wiring for `WakeSidecarInitExit` /
  `WakeSidecarRestart`. The event types + read API ship in PR-B;
  the actual `Emit` calls from the orchestrator land in PR-C
  (guest-init doesn't import `pkg/events` today; PR-C's broader
  wake-timeline observability rollout is the right surface).
- The full AC #4 OOM-isolation metal gate. The per-workload
  cgroup scopes ship and are pinned by the unit test
  (`pkg/fcvm/cgroup_test.go::TestWriteWorkloadCgroup` +
  `TestRemoveWorkloadCgroupsMirrorsWrite`); the metal-suite
  stress-loop proof needs the customer workload's malloc
  harness which lives in PR-C's scope. The current
  `TestMetalSidecarOOMIsolation` is a `t.Skip` stub.
- Customer-image `cmd` field on `WorkloadSpec`. PR-B's sidecar
  entrypoint is the canonical `/usr/local/bin/start.sh` per
  imaged's stamp during `buildSidecarLayer`. PR-C extends the
  manifest + per-workload exec path to read the customer cmd.
- Financial-model addendum (AC #7 precondition). Per the
  plan, the xlsx lives outside git and is uploaded via the
  operator-upload channel. PR-B doesn't touch the xlsx;
  PR-C consumes the new "Sidecar RAM" line via
  `BillableRAMMBWithSidecars`.

## Files changed

### Schema / migrations

- `migrations/00127_deployments_sidecar_layers.sql` — new table
  `deployment_sidecar_layers(deployment_id, sidecar_name,
  storage_key, bytes, content_digest)` with FK to
  `deployments(id)` ON DELETE CASCADE and unique
  `(deployment_id, sidecar_name)`. Originally filed at slot
  00119; renumbered to 00123 then 00124 then 00127
  post-rebase (main reserved 00119 as a fence for PR #470-FU-B
  and added real migrations at 00120/00121/00122/00123, then
  PR #547 added 00124/00125/00126).
- `migrations/00127_deployments_sidecar_layers_test.go` — cap
  pin + FK cascade pin.
- `migrations/00120_reserve_slot.sql` — fence `select 1;` only
  (the slot-fence dance; rebased-out before PR-C merges).
- `migrations/00128_events_sidecar_name_idx.sql` — partial
  expression index for `ListEventsBySidecar`'s jsonb filter
  (PR-B review finding #5). Originally filed at slot 00121;
  renumbered to 00124 then 00125 then 00128 to follow the
  sidecar layers migration in lockstep.

### State

- `pkg/state/types.go` — `DeploymentSidecarLayer` struct +
  `Store.SetDeploymentSidecarLayer` /
  `ListDeploymentSidecarLayers` methods.
- `pkg/state/pgstore.go` — implementations (hand-written INSERT
  pattern, mirrors `CreateDeployment`).
- `pkg/state/memstore.go` — mirror; tests in
  `pkg/state/memstore_sidecars_test.go` (PR-A) +
  `pkg/state/memstore_sidecar_test.go` (PR-B event read API).
- `pkg/state/store.go` — `ListEventsBySidecar` interface method.

### imaged

- `pkg/imaged/handler.go` — `buildImageLayer` extended to loop
  over `app.Sidecars`, calling `buildSidecarLayer` per entry.
- `pkg/imaged/handler.go::buildSidecarLayer` (new) — extracted
  helper, reuses `rootfs.Builder` and `rootfs.ApplyLayerGz`
  (ADR-040 verbatim Linkname + clamp policy).
- `pkg/imaged/handler.go::cleanupAppFiles` — extended to remove
  sidecar layer keys on app/teardown.
- `pkg/imaged/handler_image_build_test.go` — `TestImageBuild_SidecarCase`
  covers AC #6.

### Proto / gRPC

- `api/proto/onebox/faas/vmmd/v1/vmmd.proto` — `repeated AppSpec
  sidecars` (additive).
- `pkg/vmmdgrpc/proto.go` — `toWakeRequest` /
  `toColdBootRequest` recursively extract sidecars.

### fcvm

- `pkg/fcvm/config.go` — `Workloads []WorkloadSpec` field on
  `ColdBootSpec` and `RestoreSpec`. `DriveLayerMain`,
  `DriveSidecarPrefix` constants. `BuildColdBootConfig` emits
  one Drive per workload in stability order; main RW, sidecars
  RO. Legacy single-workload path preserved via
  `len(Workloads) == 0` branch.
- `pkg/fcvm/manager.go` — `WakeRequest`/`ColdBootRequest` carry
  `[]WorkloadSpec`. `bringUp` builds `ColdBootSpec.Workloads`
  from the request. The host applies one aggregate per-VM cgroup fence;
  guest-init creates the per-workload leaves. `StageWorkloadManifest` called per
  workload + `StageWorkloadRoster` called once on drive1.
- `pkg/fcvm/vmm.go::BootColdBoot` — resolves each workload's
  StorageBackend key through `materializeFromStorage` in turn.
- `pkg/fcvm/vmm.go::Restore` — same loop, stages sidecar drives
  read-only via `stageReadOnly`.
- `pkg/fcvm/vmm.go::StageSecretsEnv` / `StageAPIEnv` — generalized
  to per-workload drives; recipient is the workload name.
- `pkg/fcvm/vmm.go::StageWorkloadManifest` — new; loopback-mount
  per-workload drive, write `/etc/faas/workload.json` mode 0o400.
- `pkg/fcvm/vmm.go::StageWorkloadRoster` — new; same loop dance
  on drive1, writes the deployment-level roster
  `{Main, Sidecars[]}` at `/etc/faas/workloads.json`.
- `pkg/fcvm/cgroup.go::writeWorkloadCgroup` — new; writes
  `<parent>/<workload>/memory.max` raw bytes (NOT via
  `BillableRAMMB` — that would double-count the +8 MB overhead
  across siblings).
- `pkg/fcvm/cgroup.go::removeWorkloadCgroups` — new; Stat-first
  to distinguish removed vs no-op.

### guest-init

- `guest/init/main_linux.go::boot` — `discoverRoster(os.DirFS("/"))`
  routes through `runWorkloads` when `roster.Sidecars` is
  non-empty; legacy path otherwise.
- `guest/init/main_linux.go` — `discoverSidecarDevices` reads the
  roster after the main pivot, mounts each sidecar drive read-only below
  `/run/faas/sidecars/<name>`, and supplies isolated runtime mounts.
- `guest/init/workload_linux.go` (new) — `workloadSpec`,
  `workloadRoster`, `discoverRoster`, `runWorkloads`,
  `newSupervisorForMain`, `newSupervisorFor`, `runSidecar`.
- `guest/init/supervise.go` — `lastRunErr atomic.Pointer[error]`
  + `lastErr()` + `trackRunErr()` so the orchestrator can read
  the main workload's terminal state after `WaitGroup.Wait()`.

### Events

- `pkg/events/wake.go` — `WakeSidecarInitExit` + `WakeSidecarRestart`
  constants; `SidecarInitExit` + `SidecarRestart` struct types
  implementing `WakeEvent`.
- `pkg/state/pgstore.go::ListEventsBySidecar` — production reader
  with closed-kind filter (no non-sidecar row leaks).
- `pkg/state/memstore.go::ListEventsBySidecar` — in-memory twin.

### Tests

- `pkg/state/pgstore_sidecar_layers_test.go` — round-trip,
  cap-rejection, FK-cascade.
- `pkg/state/memstore_sidecars_test.go` — MemStore mirror (PR-A).
- `pkg/imaged/handler_image_build_test.go` — `TestImageBuild_SidecarCase`.
- `pkg/vmmdgrpc/proto_test.go` — empty-sidecar + 2-sidecar cases.
- `pkg/fcvm/cgroup_test.go` — `TestWriteWorkloadCgroup` +
  `TestRemoveWorkloadCgroupsMirrorsWrite` + 4 sibling tests.
- `pkg/fcvm/workload_stage_test.go` (new) — 5 tests for
  `StageWorkloadManifest` integration with `Wake`.
- `guest/init/workload_linux_test.go` (new) — `discoverRoster`
  absent/valid/malformed; `newSupervisorFor` policies;
  `Supervisor.lastErr` plumbing.
- `pkg/events/wake_test.go` — `SidecarInitExit_Shape` +
  `SidecarRestart_Shape`.
- `pkg/state/memstore_sidecar_test.go` (new) — kind-filter,
  name-filter, limit cap, empty result.
- `pkg/fcvm/sidecar_metal_test.go` (new) — `TestMetalSidecarBoot`,
  `TestMetalSidecarPortReachable`,
  `TestMetalTwoSidecarsColdBoot`,
  `TestMetalSidecarOOMIsolation` (skipped, see Scope above).

## Acceptance criteria → commit map

| AC  | Commit(s)                                                                                                                  |
| --- | --------------------------------------------------------------------------------------------------------------------------- |
| #1  | `feat(issue-463): PR-B step 7 — guest-init workload orchestrator + StageWorkloadRoster` (workload_linux.go `runWorkloads`)   |
| #2  | `feat(issue-463): PR-B step 9 — sidecar metal tests` (`TestMetalSidecarPortReachable`)                                       |
| #4  | `feat(issue-463): PR-B step 6 — fcvm runtime` (`writeWorkloadCgroup` + per-instance scope); metal full OOM gate in PR-C     |
| #6  | `feat(issue-463): PR-B step 4 — imaged` (`TestImageBuild_SidecarCase`)                                                      |
| #7  | xlsx addendum is uploaded via the operator channel; PR-B doesn't touch the xlsx                                             |

| ADR §                                    | Commit(s)                                                                 |
| ---------------------------------------- | ------------------------------------------------------------------------- |
| Decision 7 (no shared writable layer)    | step 6 (`BuildColdBootConfig` RO sidecars); step 7 (isolated sidecar roots) |
| §"Downstream" (ColdBootSpec.Workloads)   | step 6                                                                     |
| §"Downstream" (guest-init orchestrator)  | step 7                                                                     |
| §"Downstream" (imaged buildSidecarLayer) | step 4                                                                     |

## Verification

- `make test` — green across `pkg/`, `cmd/`, `guest/`.
- `make test-metal` (reference control-plane node only) — gates TestMetalSidecarBoot +
  TestMetalSidecarPortReachable + TestMetalTwoSidecarsColdBoot.
- `make leakcheck` — the aggregate per-instance host scope is clean after
  Destroy; guest-init owns per-workload leaf cleanup inside the VM.
- `make lint` — golangci-lint v2.4.0 + custom checks (per CLAUDE.md).

Specific AC coverage (the verification matrix from the plan):

| AC | Test that proves it                                       | Status         |
| -- | --------------------------------------------------------- | -------------- |
| #1 | guest-init/main_linux_test.go + TestMetalSidecarBoot       | PR-B shipped   |
| #1 | TestSidecarInitFailure_EmitsFailureClassUserError          | PR-C (emit)    |
| #2 | TestMetalSidecarPortReachable                              | PR-B shipped   |
| #4 | TestMetalSidecarOOMIsolation                               | PR-C (skipped) |
| #6 | TestImageBuild_SidecarCase                                 | PR-B shipped   |
| #7 | xlsx addendum — manual operator upload                     | pre-PR-B       |

PR-C readiness check before merge: `grep -r Sidecar pkg/billing/
cmd/meterd/` should still be empty (PR-C's job); the helper
`pkg/api/limits.go::BillableRAMMBWithSidecars` is left unused
for that PR to wire.

## Risks & follow-ups

1. **PR-C billing path.** `BillableRAMMBWithSidecars` is in
   `pkg/api/limits.go` but no consumer yet. The unit-test
   surface (the financial-model xlsx isn't in git) prevents
   accidental billing-side coupling — the constant is exported
   for PR-C to find, not to be silently consumed by
   `pkg/fcvm`.
2. **Migration slot renumbering.** PR-A landed with slot 95
   (later renumbered to 116 → 117 → 118). PR-B's fence at
   slot 120 absorbs one collision; a follow-up PR that wants
   121 should renumber per the slot-fence discipline in
   `migrations/README.md`.
3. **Characterize probe regression.** A sidecar's TCP listener
   must not be classified as the main workload's class. The
   filter `runWorkloads` wiring (`go runCharacterizationForSup(mainSup, ...)`)
   is the load-bearing one-liner; a missed sidecar bind could
   regress boot-class inference. The probe's `AppPID()` /
   `WaitForExit` / `RingBufferTail` callbacks all read from
   `mainSup`, never from sidecar supervisors.
4. **OOM isolation correctness.** The host cgroup contains the Firecracker
   process and enforces the aggregate VM limit. Per-workload leaves live in
   the guest cgroup hierarchy because cgroup v2 cannot delegate controller
   children beneath a host scope that already contains Firecracker. PR-C's
   metal-suite stress-loop test gates the customer-visible isolation property.

## References

- Issue #463: "SIDECARS: single init container + single metrics sidecar".
- ADR-069: sidecar containers init + metrics (hard cap 2).
- PR-A: PR #531 (API/storage contract).
- Spec §4.6 (two-drive rootfs, preserved).
- Spec §4.7 (plan RAM + 8 MB billing, extended by PR-C).
- Spec §6.2 (invariants: per-app concurrency, RAM ceiling, parked
  = zero RAM, distinct instance state).
- ADR-009 (identical inner network world, drives the per-app
  sidecar reachability).
- ADR-022 (vsock resume hook + entropy re-key, untouched by PR-B
  but consumed by the sidecar boot path through the same
  `waitReady` machinery).
- ADR-040 (rootfs symlink verbatim + clamp policy, applied per
  sidecar ext4).
- ADR-044 (per-plan cgroup hierarchy, nested under PR-B's
  per-workload scopes).
- ADR-064 / ADR-068 (wake-timeline vocabulary + closed event
  kinds; PR-B extends with `wake.sidecar_*`).
