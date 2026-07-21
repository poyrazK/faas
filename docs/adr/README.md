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

ADR-011 and ADR-012 are required by the UX spec (§11) before git-deploy work
begins at M7.5; both landed on 2026-07-17 alongside the M7.5 PR open.
