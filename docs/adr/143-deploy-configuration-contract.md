# ADR-143 · Deploy configuration contract: one unit source, a declared environment, and convergence that is verified

- **Status:** accepted
- **Date:** 2026-09-05
- **Scope:** `deploy/ansible/`, `pkg/daemonunitspec`, `cmd/deployctl`, CI gates
- **Supersedes:** the "Phase 2 tombstone deletion" note in ADR-110 (issue #911); the operator-copied `*.toml.example` narrative in `deploy/ansible/README.md`
- **Related:** ADR-047 (streaming operator gate), ADR-078 (unit generator), ADR-091 D21 (geo edge rules), ADR-110 (split-box manifest), spec §11

## Context

Three production incidents in the first week of September 2026 had one root cause: **a daemon read a `FAAS_*` variable that no deploy path set, and nothing could notice.**

| Incident | Variable | Effect | How long it survived |
|---|---|---|---|
| PR #1286 | `FAAS_FUNCTION_RUNNER_*` on imaged | every function deploy on the platform failed at first wake | six weeks |
| PR #1287 | `faas_apid_app_errors_listen` (apid) | `request_telemetry` and `app_errors` dropped 100% of their data, WARN-only | since the split-box cut-over |
| this ADR | `FAAS_GATEWAY_STREAMING`, `FAAS_JOBS_DISPATCH`, `FAAS_GEOIP_DB_PATH` | streaming responses off fleet-wide on a paid plan; job runs pending forever; customer geo rules silently no-ops | since each feature shipped |

The audit that found the third row (`comm -23` of every `"FAAS_*"` literal in daemon code against every `FAAS_*` any deploy path mentions) also found the structural reasons the class recurs:

1. **Four sources of truth for systemd units.** `deploy/systemd/`, the ansible role `files/` trees, the retired `deploy/controlplane/systemd/` tombstone, and the Go renderer in `pkg/daemonunitspec`. `deployctl generate` only covered three trees; the vmmd, gatewayd-internal, gatewayd-public and builderd roles carried hand-edited copies that had drifted (ordering on masked units, a missing `LoadCredential=`, a missing explicit env).
2. **Config changes never reached the daemon.** The control-plane role had one restart handler (meterd). apid and schedd unit or drop-in changes, including the very drop-in that fixes PR #1287, installed a file and stopped. imaged, builderd and githubd roles had no handlers at all. Drop-in changes did not even `daemon-reload`.
3. **Nothing verified convergence.** CI ran `--syntax-check` only; `scale_check.yml` carried real `assert`s for months and was never executed. The end-of-bootstrap role checked service masks, never a readiness probe. No ansible-lint.
4. **Spec §11 host hardening was unimplemented.** No sshd posture, fail2ban, unattended security updates, or auditd anywhere in the tree.

## Decision

### D1 — `pkg/daemonunitspec` is the only source for production units

`cmd/deployctl generate` emits every service role's `files/faas-<daemon>.service` (eight roles) plus the legacy `deploy/systemd/` tree; `make generate-check` gates all of them. The `deploy/controlplane/` tombstone is deleted (ADR-110 Phase 2). The registry gains `Entry.Role` (`control-plane` | `compute-only`); the manifest renderer, `deploy/ansible/vars/daemons.yml` and the per-role generator targets all derive the partition from it instead of copying it.

Reconciliations made while unifying: gatewayd-internal no longer orders on the masked `faas-apid.service` / `faas-schedd.service` and delivers the session key via `LoadCredential=` (security review A4); builderd drops the masked apid ordering; vmmd and builderd gain the explicit env the Go spec already declared.

### D2 — every environment variable is declared, with a delivery contract

`pkg/daemonunitspec/envcontract.go` lists every `FAAS_*` a daemon (or a deploy script) reads with an `EnvSource`:

`unit` · `dropin` · `envfile` · `secrets-env` · `runtime-config` · `default` · `script` · `internal` · `client` · `dev-only` · `guest`

`envcontract_test.go` enforces it in both directions and generates `docs/ops/env-contract.md`:

- every literal read in daemon code must be declared (forward tripwire);
- every entry that promises delivery must be delivered by that path — `unit` must appear in a rendered unit, `dropin`/`envfile` in the ansible tree or the manifest renderer, `secrets-env` must name its file and the owner's unit must load a secrets `EnvironmentFile`, `runtime-config` must be mapped in `cmd/apid/runtime_config.go`;
- every `FAAS_*` the deploy tree sets must be read by something (dead-config tripwire);
- no stale entries.

**Rule:** a feature that is meant to be on in production may not be `default` with an off default. The three silently-off gates are now declared: streaming via `streaming_enabled = true` in `gatewayd-internal.toml` (the code default stays off per ADR-047; production config turns it on), jobs dispatch as an explicit `FAAS_JOBS_DISPATCH=0` in the schedd unit with the reason (the vmmd job RPC has not shipped), and the GeoIP database staged by a new `geoip` role at the code-default path with a pinned monthly release and two checksums.

### D3 — every config change reaches the running daemon, safely

Every service role has handlers: a `daemon-reload` listener first, then `systemctl try-restart` per daemon. `try-restart` only restarts an *active* unit, so the documented contract that roles ship units but never enable or start them is preserved: the first bootstrap is a no-op and an unconfigured daemon is never started into a crash loop. Every unit, drop-in, TOML and env-file task now notifies.

### D4 — convergence is verified, not assumed

- `deploy/ansible/vars/daemons.yml` is the shared topology for `role_convergence` and the new `fleet_verify` role; `TestDaemonsYAML_LockstepWithRegistry` pins it to the registry (units per role and readiness probes).
- `fleet_verify` runs at the end of both bootstrap plays in lenient mode (skips units nobody enabled yet) and strictly through `verify.yml` / `make verify-fleet`: every required unit enabled, active, answering its unix-socket or TCP probe, then `gregalectl doctor`.
- CI now runs `scale_check.yml` for real against the rendered example manifest, `ansible-lint` at the `production` profile (`deploy/ansible/.ansible-lint`; three cosmetic rules skipped with rationale), and syntax-checks `verify.yml`.

### D5 — spec §11 host posture ships as a role

`host_hardening` installs an sshd drop-in (password auth off only after proving the connecting user has an `authorized_keys`, validated with `sshd -t` before reload, `Port=` never touched), fail2ban's sshd jail, unattended security-pocket upgrades with auto-reboot off (a reboot destroys every tmpfs jail), a small auditd rule set over `/etc/faas`, units, nftables and the VMM binaries, and the kernel information-leak sysctls not owned by the grub role. Each sub-feature has its own switch; all default on.

## Consequences

- Adding an env var to a daemon without declaring it fails `make test`. Declaring it `unit`/`dropin`/`envfile` without wiring it fails the same test. Setting something in ansible nobody reads fails it too.
- Adding a daemon means adding it to the registry with a `Role`; the renderer, the topology file test, the per-role generator targets and `daemons.json` follow.
- Operators regain three features that were paid for or documented: streaming, geo rules, and honest job-run failures instead of silent pending rows. Jobs dispatch stays off until Mega-1.5 ships the vmmd RPC; the unit says so.
- `ansible-lint` adds one pinned pip dependency to CI. The `name[casing]`, `var-naming[no-role-prefix]` and `yaml[line-length]` rules are skipped deliberately; everything else is a merge blocker.
- Out of scope, tracked separately: the control-plane bootstrap still builds the guest kernel from source when no signed release kernel is staged (compute nodes require the signed one); the operator-copied `*.toml.example` path on the control plane remains until the control-plane join adopts the manifest renderer the compute join already uses.
