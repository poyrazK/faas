# Architecture Decision Records

ADR-001 through ADR-010 are **accepted and locked for v1**; they live inline in
[`../faas_implementation_spec.md`](../faas_implementation_spec.md) §3, not as
separate files here. This directory holds ADRs made *after* the spec.

Any deviation from the spec requires a new ADR here first (spec §3, CLAUDE.md).

## Format

```
# ADR-NNN · <title>

- **Status:** proposed | accepted | superseded by ADR-MMM
- **Date:** YYYY-MM-DD
- **Decision:** <what we're doing>
- **Why:** <the forcing reason>
- **Consequences:** <what this makes true, including new surfaces/milestones>
- **Rejected alternatives:** <options considered and why not>
```

## Log

| ADR | Title | Status | Source |
|---|---|---|---|
| 001–010 | Locked v1 decisions | accepted | spec §3 |
| 011 | Thin dashboard at launch (was gap G3) | accepted | UX spec §11 — landed before M7.5 code |
| 012 | `githubd` / GitHub App for push-to-deploy | accepted | UX spec §11 — landed before M7.5 code |
| 013 | M1 gRPC codegen: generated protobuf (v1.0) | accepted | M1 plan |
| 014 | M1 wire shape: caller resolves `(app)` | accepted | M1 plan |
| 015 | M1 unix-socket auth (mode 0660 group `faas`) | accepted | M1 plan |
| 016 | M1 `Stats()` shape + `vmmd_*` metric names | accepted | M1 plan |
| 017 | Hand-written `pkg/state/pgstore.go` (M5 sqlc exception) | accepted | M5.1 review |
| 018 | schedd gRPC surface + ReportActivity ownership | accepted | M5 plan |
| 019 | Jailer `--exec-file` invocation + jail resource ownership | accepted | M0 metal run |
| 020 | `pkg/secretbox` host age keypair for sealed customer secrets | accepted | M7 — landed before M8 |
| 021 | Account export + staged deletion (G6 GDPR self-service) | accepted | M8 G6 — landed 2026-07-21 |
| 022 | Post-restore resume hook over AF_VSOCK (V6 ship-blocker) | accepted | M8 PR-A |
| 023 | IPv6 tenant egress policy (`ip6 daddr`, allow-and-restrict) | accepted | M8 |
| 024 | CertMagic cut-over + test closure (gatewayd TLS) | accepted | M8 |
| 025 | Decoupled control plane and compute nodes | proposed | M8 |
| 026 | schedd consumes `NotifyAccountDeletionPending` and evicts live instances | accepted | M8 — landed 2026-07-21 |
| 027 | Stripe push observability taxonomy (11-label + duration histogram) | accepted | M7 hardening |
| 031 | Per-app egress IP allowlist (`cidr[]` on `apps`, post-deny accept) | accepted | M8 tier-2 |
| 032 | MVP auth: harden /login against #165 + real sign-in methods | accepted | issue #165 / PR #1+#2 |
| 033 | Per-app egress IP allowlist — IPv6 mirror (trigger swap + renderer partition) | accepted | M8 tier-2 |
| 034 | IPv6 lateral-movement: 6to4 + Teredo deny (v6 denylist gap from ADR-023) | accepted | M8 tier-2 PR-A |
| 035 | Auth audit log surface (IAM-4: `auth.login`, `key.created`, `account.plan_changed`, …) | accepted | M8 IAM-4 / PR #217 |
| 036 | Per-instance metrics: {app,node} cardinality rollups (issue #170 / PR-A + G10) | accepted | issue #170 |
| 037 | Reactive scale-up trigger (per-app RPS / CPU targets → proactive admit up to max_concurrency) | accepted | issue #169 / #172, M7 follow-up |
| 038 | Build attestation: provenance row + (Phase 3) cosign sign/verify for ext4 layers | accepted | issue #197 B3.x, Tier 3 sprint |
| 040 | OCI layer symlink policy: store `Linkname` verbatim, clamp ancestors on traversal | accepted | fixes imaged crash-loop / cd-digitalocean |
| 041 | Migration slot reservation convention (gate carve-out for cross-PR slot collisions) | accepted | follow-up to #335 / #369 / #352 deadlock |
| 042 | Per-app request metrics + `cold_wake`→`cold_boot` rename; `route` label dropped (ADR-036 precedent) | accepted | issue #273 / #273 |
| 043 | App logs producer stream (Move 4): per-instance ring + schedd fan-out + vmmd Logs RPC | accepted | issue #254, Move 4, M7 observability |
| 044 | Per-plan CPU fairness at the cgroup level (3-level hierarchy + per-plan `cpu.weight` / `cpu.max` + `FaasCpuStarvation` alert) | accepted | issue #301 |
| 045 | Mutable app env via `POST /v1/apps/{id}/env` (replaces immutable `--env`; envelope-sealed, re-encrypted on `RotateKey`) | accepted | Move 2 |
| 046 | Per-instance egress metering (telemetry seam for future egress-billing PR) | accepted | issue #<TBD> (egress billing seam; ADR-039 precedent) |
| 050 | Repo decomposition: `projects` object + multi-workload auto-provision | proposed | `docs/repo_decomposition_implementation.md` |
<<<<<<< HEAD
| 051 | Characterization boot: observed workload classification + in-guest port normalization | accepted | ADR-050 Phase 4 |
| 052 | Adding a function runtime: 7-layer additive procedure | accepted | Tier 1 PR 1+2 worked example |
| 053 | Deploy-time overrides for OCI image deploys (entrypoint/cmd/env/port/healthcheck) | accepted | issue #460 (PR A ships contract; PR B imaged layer injection; PR C port plumbing) |
| 059 | Customer-configurable scaling policy (4-PR: persistence + inflight signal + engine cooldown + worker carve-out) | proposed | issue #462 / PR #493 / #501 / #507 / #512 |
| 060 | Per-app GB-h floor for `min_instances > 0` (meterd synthetic rows + UUID v5 lineage) | proposed | issue #515 (follow-up to #462) |

ADR-011 and ADR-012 are required by the UX spec (§11) before git-deploy work
begins at M7.5; both landed on 2026-07-17 alongside the M7.5 PR open.
