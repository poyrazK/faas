# ADR-126 — public bind defaults in multi-host posture

## Status
Accepted, 2026-08-22. Part of the multi-host safety cluster
(PR-8 / PR-9, audit F8).

## Context

`gatewayd-public` is the TLS-only public listener introduced by
the Tier A7 split (ADR-070). It serves every customer request
that arrives at a Gregale node. Today, when no
`FAAS_PUBLIC_LISTEN_ADDR` env var is set, the daemon binds
`127.0.0.1:8080` (HTTP) — the single-box default. On a
multi-host box the loopback bind is unreachable from the
external LB; traffic disappears and operators see a silent
failure mode.

The same trap applies to `FAAS_PUBLIC_CONTROL_ADDR` (default
`127.0.0.1:9092`) which serves `/healthz`, `/readyz`,
`/metrics` — kubelets and prometheus can't reach the loopback
on a multi-host node either.

The single-box posture is signalled by `FAAS_NODE_NAME=""`.
Operators who set `FAAS_NODE_NAME=<x>` have opted into the
multi-box fleet and MUST also set
`FAAS_PUBLIC_LISTEN_ADDR` / `FAAS_PUBLIC_CONTROL_ADDR` to
reachable values.

## Decision

Add a boot-time check at `cmd/gatewayd-public/main.go`
(`requirePublicBindInMultiHost`). When `FAAS_NODE_NAME != ""`
AND the relevant env var is unset (per `os.LookupEnv`), the
daemon refuses to start with a loud error naming the env var
that must be set.

The escape hatch — explicit loopback override — uses
`os.LookupEnv` to distinguish "unset → would default to
loopback" from "explicitly set to loopback". The latter is
permitted (a node behind an external LB may legitimately want
loopback bind; they just have to opt in).

Companion PR-9 (`cmd/gregalectl/commands_manifest_ansible.go`)
makes the manifest renderer emit `faas_public_listen_addr` for
compute roles so a correctly bootstrapped fleet never reaches
the boot error.

## Consequences

- New `requirePublicBindInMultiHost` function in
  `cmd/gatewayd-public/main.go`.
- New tests:
  - `TestRequirePublicBindInMultiHost_AcceptsLoopbackDefaultInSingleBox`
  - `TestRequirePublicBindInMultiHost_FatalsOnLoopbackDefault`
  - `TestRequirePublicBindInMultiHost_FatalsOnLoopbackControl`
  - `TestRequirePublicBindInMultiHost_AcceptsExplicitOverrideInMultiHost`
- Migration: none.
- Quota / limit change: none.
- Wire shape: unchanged.

## Open follow-ups (deliberately deferred)

- None. The check is the backstop; PR-9 makes the manifest
  emission the primary mechanism so the check is rarely fired
  in a correctly bootstrapped fleet.

## Rejected alternatives

- **Default to `0.0.0.0`** when `FAAS_NODE_NAME` is set.
  Rejected: the LB may not be on the same host; binding all
  interfaces without an explicit operator decision is a
  security regression.
- **Warn-only check** (log a warning, boot anyway). Rejected:
  the failure mode is "traffic disappears" which is silent —
  a warn log at boot won't be seen until the operator
  investigates an outage.
- **Per-host firewall / socket activation rules.** Rejected:
  adds a coord surface outside the daemon. Operators on
  different distros manage socket activation differently; the
  env-var check is the single source of truth.

## References

- ADR-070 (Tier A7 edge split, `gatewayd-public` /
  `gatewayd-internal`).
- ADR-119 (multi-box posture signal `FAAS_NODE_NAME`).
- Multi-host safety audit, finding F8.
- `cmd/gatewayd-public/main.go:625` (the check).