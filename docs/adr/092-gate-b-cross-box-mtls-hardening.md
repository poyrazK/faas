# ADR-092 · Gate-B cross-box mTLS hardening

- **Status:** accepted v1.0 (2026-08-10)
- **Date:** 2026-08-10 (proposed + accepted)
- **Decision:** Split the FaaS daemon fleet across a control-plane host and one or more compute hosts, and harden the cross-box mTLS posture so the production cutover from a single-box loopback deployment (`127.0.0.1` on fsn-1) is operator-reproducible and CI-pinned. M9 runs a peer schedd beside vmmd on every compute host while retaining the control-plane schedd. No new cryptography is introduced; Gate-B is the **operational scaffolding** on top of ADR-052 (chain + SAN + EKU + PeerCN), ADR-056 (`pkg/wire.PGNodeVerifier`), and ADR-070 (gatewayd-public / gatewayd-internal split).
- **Why:** ADR-025 §Tier 2 has retired all five Tier 1 pre-requisites plus #250. ADR-025 lines 39-43 list the remaining cutover gates; this ADR closes the first. Without the split the fleet's `fsn-1` + `fsn-2` boxes both run the full daemon set, which (a) lets a single host compromise expose every surface (apid, vmmd, builderd, imaged, meterd, gatewayd-public, gatewayd-internal, githubd), (b) wastes control-plane memory on boxes that don't need to run vmmd/builderd/imaged, and (c) prevents the operator from running the multi-host cutover at all because the per-daemon role is not enforced anywhere. The role gate + per-box ansible split + per-box PKI subset close the operational surface; verifier wiring is a follow-up slice (see §Future work).
- **Consequences:**
  - **Role gate (PR-1):** every daemon refuses to start under the wrong observed role. The PR-1 `pkg/role` package + per-daemon `[faas].role` field + `role.Require(daemon, observed, allow...)` enforce the per-box assignment listed below. Single-box dev (`make bootstrap` against `127.0.0.1`) defaults to `RoleSingleBox` and every daemon is allowed to start; back-compat is preserved.
  - **Ansible split (PR-2):** the legacy single-host `deploy/ansible/inventory` (single-file `[box] 127.0.0.1`) is replaced by `deploy/ansible/inventory/hosts.ini` carrying `[control_plane]`, `[compute_nodes]`, and the legacy `[box]` group with `[box:children]` aggregating both. New `host_vars/faas-fsn-{1,2}.yml` pin `faas_box_role` and `faas_node_name`. New `deploy/ansible/bootstrap.yml` has three plays (one per host group, each `serial: 1` + `max_fail_percentage: 0`). New `deploy/ansible/roles/compute_only_service/` owns imaged + its tripwires (sign.key, sign-pub.pem, host.age, ReadWritePaths). New `deploy/ansible/roles/githubd_service/` is minimal (unit file + example TOML only, no secret asserts). New Makefile targets `bootstrap-control-plane` and `bootstrap-compute`; legacy `bootstrap` calls `bootstrap.yml --limit box -e faas_box_role=single-box`.
  - **Per-box PKI subset (PR-3):** new `pkg/pki.RolesForBox(role role.Role) []Role` filters the canonical `pkg/pki.Roles()` (unchanged) by per-daemon directory ownership. New `gregalectl pki {init,rotate,status} --box-role=...` flag threads through. Operator runs `gregalectl pki init --box-role=control-plane` on fsn-1 and `gregalectl pki init --box-role=compute-only` on fsn-2; each box gets only the leaves it owns (no double-issuance, no over-issuance). Empty / unset preserves the pre-Gate-B full-set posture. Invalid values fail-closed (empty slice, "0 leaves written" report).
  - **Doc defect fixes (PR-4):** `docs/runbooks/multi-host-rollout.md` mTLS-PKI paths at lines 36, 149, 153, 158-160, 416, 418, 419, 452 are corrected from `/etc/faas/secrets/` → `/etc/faas/tls/`. Line 22's `> [!NOTE]` retractable warning is flipped to `> [!CAUTION]` per the existing acceptance footer at lines 600-601. Line 103's `/etc/faas/secrets/storage-box/` is **not** a defect (storage-box credentials are non-mTLS).
  - **CI pin (PR-4):** `cmd/e2e/mtls_e2e_test.go::TestMTLSE2E_RoleGateRefusesWrongBox` exercises the four per-daemon role gates — apid, schedd, vmmd, builderd — at the unit-test layer (no live dial required). Each gate is pinned for both the bad-role refusal and the good-role acceptance, plus the single-box back-compat path.

## Per-box daemon assignments

| Daemon            | Allowed roles                            | Lives on |
|-------------------|------------------------------------------|----------|
| apid              | single-box, control-plane                | fsn-1    |
| schedd            | single-box, control-plane, compute-only | fsn-1 + every compute node |
| gatewayd-public   | single-box, control-plane                | fsn-1    |
| githubd           | single-box, control-plane                | fsn-1    |
| meterd            | single-box, control-plane                | fsn-1    |
| vmmd              | single-box, compute-only                 | fsn-2    |
| gatewayd-internal | single-box, compute-only                 | fsn-2    |
| builderd          | single-box, compute-only                 | fsn-2    |
| imaged            | single-box, compute-only                 | fsn-2    |

`imaged` is on `fsn-2` because it calls `MountParentExt4ReadOnly` on vmmd (ADR-053 slice-3) — the parent ext4 mount must be on the same host as the vmmd. `githubd` + `meterd` are on `fsn-1` because they both talk to control-plane daemons (apid/builderd for githubd, schedd for meterd).

## Per-box PKI subset

The filter is Directory-based: each per-daemon subdirectory under `/etc/faas/tls/` is either wholly owned by one box role or wholly absent on the other. Client leaves for outbound dials live in the **dialer's** home directory (e.g. `meterd/{schedd-client,egress-client}` on fsn-1 because meterd is the dialer; `gatewayd/{schedd-client,vmmd-client}` on fsn-2 because gatewayd-internal is the dialer).

| Box role         | Directories it owns                                                                             |
|------------------|-------------------------------------------------------------------------------------------------|
| `control-plane`  | `schedd`, `apid`, `meterd`, `githubd`, `gatewayd-public`                                        |
| `compute-only`   | `schedd`, `vmmd`, `builderd`, `imaged`, `gatewayd`, `egress`, `gatewayd-internal-public`       |
| `single-box` / `""` | All directories (the legacy pre-Gate-B posture; equivalent to `pkg/pki.Roles()`)               |
| anything else    | Empty slice (fail-closed; operator sees "0 leaves written" rather than a silent full-fleet issuance) |

The two per-box sets are disjoint except for the explicitly shared `schedd`
directory, which is present on both roles now that M9 runs a node-local
schedd on every compute host. The invariant is pinned by
`pkg/pki/pki_test.go::TestRolesForBoxIsSubsetOfRoles`: every per-role set is a
subset of `Roles()`, both per-box sets are non-empty, `RoleSingleBox` returns
`Roles()` verbatim, and no directory other than `schedd` overlaps.

## Reused primitives (no change)

- **ADR-052** — chain + RFC 6125 SAN + EKU enforcement in a single handshake pass; handler-layer `pkg/wire.PeerCN(ctx)` defense-in-depth. Multi-box entry points: `wire.LoadServerTLSConfigWithPrefixAndVerifier` / `wire.LoadClientTLSConfigWithPrefixAndVerifier`.
- **ADR-056** — `pkg/wire.PGNodeVerifier` handshake hook; snapshot-backed `loader-failure-keeps-last-known-good`. Single-box dev keeps `AllowAllNodeVerifier`; multi-box schedd + vmmd wire `PGNodeVerifier`.
- **ADR-070** — gatewayd-public / gatewayd-internal split; `gatewayd-public` is the only public listener on a node (TLS-only edge). `gatewayd-internal` listens on a unix socket on the node and is reached only by `gatewayd-public`.

Gate-B is the deployment-shape layer on top of these three. No new mTLS primitives are introduced.

## Per-PR surface (reviewable in ~10 min each)

### PR-1 — `pkg/role` + per-daemon refusal gate (merged)

- `pkg/role/role.go` — `type Role string`, constants `RoleSingleBox` / `RoleControlPlane` / `RoleComputeOnly`; `Parse`, `FromConfig` (env wins; empty parses to `RoleSingleBox`), `Require(daemon, observed, allow...)` returning a typed error naming the daemon + observed + allowed.
- `pkg/role/role_test.go` — table-driven cover of every allow × role cell; env-vs-TOML precedence; parse error.
- Per-daemon config struct extended with `Role string` field and `[faas].role` TOML field. Each daemon's `run(ctx, log)` calls `role.Require` immediately after `LoadConfig`. Refusal is logged at ERROR with the daemon's name + observed + allowed, and the daemon exits non-zero.
- `cmd/imaged/main.go` — extract `cmd/imaged/config.go` (the only daemon without one today) following the schedd/vmmd shape.

### PR-2 — Ansible split + host_vars + bootstrap playbook (merged)

- `deploy/ansible/inventory/hosts.ini` — three groups `[control_plane]`, `[compute_nodes]`, `[box]` with `[box:children]` aggregating both.
- `deploy/ansible/host_vars/faas-fsn-{1,2}.yml` — `faas_box_role` + `faas_node_name`.
- `deploy/ansible/bootstrap.yml` — three plays, each `serial: 1` + `max_fail_percentage: 0`, dispatching by host group.
- `deploy/ansible/roles/control_plane_service/tasks/main.yml` — surgical split: imaged user / example-TOML / unit / runtime-dirs / sign-key tripwires removed; `host.age`, `archive-creds.json`, `trusted-publishers/`, `tls/ca/` kept (cross-cutting).
- `deploy/ansible/roles/compute_only_service/tasks/main.yml` — assert `faas_box_role in [compute-only, single-box]`; imaged user/unit/example-TOML; `/srv/fc/snap` + `/srv/fc/sigs` + `/var/lib/faas/build-drive`; sign.key (0440), sign-pub.pem (0444), imaged ReadWritePaths, imaged PKI leaves.
- `deploy/ansible/roles/githubd_service/tasks/main.yml` — minimal like `gatewayd_public_service`; unit + example TOML only, no secret asserts.
- `Makefile` — `bootstrap-control-plane` and `bootstrap-compute` targets; legacy `bootstrap` calls `bootstrap.yml --limit box -e faas_box_role=single-box`.
- `cmd/deployctl/main.go::defaultTargets` — extended with the two new ansible role directories and their `computeOnlySkips()` / `githubdOnlySkips()` helpers.
- `.github/workflows/ci.yml` — second `ansible-playbook --syntax-check bootstrap.yml` invocation alongside the existing `site.yml` syntax-check.

### PR-3 — `gregalectl pki init --box-role` + PGNodeVerifier extension (merged; verifier wiring deferred)

- `pkg/pki.RolesForBox(role role.Role) []Role` — per-box subset filter; canonical `pkg/pki.Roles()` unchanged.
- `pkg/pki/pki_test.go::TestRolesForBoxIsSubsetOfRoles` — four invariants (subset, non-empty, single-box back-compat, and only the M9 `schedd` overlap).
- `cmd/gregale/commands_pki.go` — `--box-role` flag threaded through `ensureAllLeaves`, `ensureAllLeavesFiltered`, `reportLeafStatusAll`, `cmdPKIInit`, `cmdPKIRotate`, `cmdPKIStatus`. Empty / unset preserves the full-set posture. Invalid values fail-closed.
- **PGNodeVerifier wiring on the remaining five daemons was scoped to a follow-up slice.** On closer review, **none of apid / gatewayd-internal / meterd / githubd / builderd have a cross-box mTLS dial in v1** — apid dials githubd (fsn-1 → fsn-1), gatewayd-internal talks to schedd via `pg_notify` only, meterd dials schedd + egress (fsn-1 → fsn-1), githubd serves on unix socket, builderd dials vmmd (fsn-2 → fsn-2). The stdlib chain + SAN + EKU path is sufficient for same-box dials (every leaf on a box is issued by the same CA). `PGNodeVerifier` is load-bearing for cross-box hops; those land later as new dial relationships are added. See §Future work.

### PR-4 — Runbook + ADR + CI pin + doc defect fixes (this PR)

- `docs/adr/092-gate-b-cross-box-mtls-hardening.md` — this file.
- `cmd/e2e/mtls_e2e_test.go::TestMTLSE2E_RoleGateRefusesWrongBox` — four-cell table: apid, schedd, vmmd, builderd. Bad role rejected; good role accepted; single-box back-compat preserved.
- `docs/runbooks/multi-host-rollout.md` — mTLS-PKI path doc defects fixed at lines 36, 149, 153, 158-160, 416, 418, 419, 452 (changed `/etc/faas/secrets/` → `/etc/faas/tls/`); retractable warning at line 22 flipped from `> [!NOTE]` to `> [!CAUTION]`. Storage-box credentials at line 103 are **not** mTLS PKI material and stay under `/etc/faas/secrets/`.
- Runbook acceptance footer at lines 600-601 documents Gate-B acceptance criteria.

## Future work

- **PGNodeVerifier wiring on cross-box mTLS dials.** The first cross-box mTLS dial lands in a future slice (Tier A10 PR-D standby write-redirect already exposes this gap). When the first cross-box hop arrives, wire `PGNodeVerifier` on the dialer + receiver's server-side `LoadServerTLSConfigWithPrefixAndVerifier` + client-side `LoadClientTLSConfigWithPrefixAndVerifier` paths, mirroring `cmd/vmmd/main.go:826-844`.
- **`builderd` ansible role.** `builderd` has no unit file in tree today and is only deployed via `make metal-lima` / `bootstrap.sh` (RETIRED 2026-08-15 by issue #911 / PR-1; v2 path is `make bootstrap` + `gregalectl manifest {validate,render}` + `gregalectl release install`). A future slice adds `deploy/ansible/roles/builderd_service/` parallel to `vmmd_service`.
- **Live-cutover runbook.** The current runbook `docs/runbooks/multi-host-rollout.md` §Pre-flight is the Gate-B operator-facing reference. Now `docs/runbooks/manifest-renderer-cutover.md` (PR-7) is the canonical cutover reference — the fsn-1 / fsn-2 in-place migration sequence (which boxes to drain first, the `faas-fsn-2` capacity-report pre-warm, the first cross-box handshake sanity check).
- **Multi-host HA (ADR-083 Tier A8 active-passive).** Already shipped as `pkg/gateway/leader/leader.go`. Gate-B is the cutover step; the N+1 active-passive topology is a follow-up.

## Rejected alternatives

- **Force-merge the four PRs as a single review.** Rejected — each PR is independently reviewable in ~10 min, and the per-PR CI pin surface is meaningful (PR-3 catches verifier wiring drift; PR-4 catches doc defects). The CLAUDE.md "Small PRs" rule applies.
- **Shared `service_base` role with include_role dispatch.** Rejected — surgical split keeps each role's tripwires co-located with the daemon they protect; the user-confirmed approach at planning time (AskUserQuestion, 2026-08-10). Each tripwire is documented with a "this runs on every box that runs X daemon" comment so the intent is durable.
- **Schema migration for `compute_nodes.role`.** Rejected — option B: derive from the daemon's runtime config + host_vars. No new migration slot needed; the role is set at deploy time, not at runtime, and no in-flight app rows are affected.
- **Deprecate `RoleSingleBox`.** Rejected — single-box dev (Lima + the existing `make bootstrap` against `127.0.0.1`) is the default-local path and must keep working. `RoleSingleBox` is the back-compat posture; every daemon's allow-list includes it.
- **Replace `pkg/pki.Roles()`.** Rejected — keep `Roles()` as the canonical "every leaf a fleet could need" set. `RolesForBox` is a filter on top, not a replacement. The single-box back-compat path keeps issuing everything.
- **Extend `pkg/role` to enforce scheduling policy.** Rejected — `pkg/role` is a deployment-shape gate only. Scheduling policy is `pkg/sched` territory.
