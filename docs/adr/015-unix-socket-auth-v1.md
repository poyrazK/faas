# ADR-015 · M1 unix-socket auth (v1.0 = mode 0660 group `faas`)

- **Status:** accepted
- **Date:** 2026-07-16
- **Decision:** vmmd binds `/run/faas/vmmd.sock` with mode `0660` and group `faas`. schedd, gatewayd, builderd (and any future control-plane daemon) join the `faas` group so the kernel grants write access via standard unix DAC. No mTLS, no SO_PEERCRED check on the wire, no socket firewall.
- **Why:** (a) Every daemon on the box already runs as a known user (apid, schedd, vmmd…). If a user cannot join the `faas` group, they cannot dial — same threat model as PG's `peer` auth (`doc/faas_implementation_spec.md:398`). (b) The only explicitly-listed daemon-to-daemon auth reference in the repo is spec line 398 about PG; spec §4.4 doesn't mention auth; CLAUDE.md's "Components talk via Postgres rows + pg_notify, or gRPC on unix sockets" is the whole architecture. Picking a layered-DAC approach that doesn't need a transport-level auth side-channel keeps the daemon code narrow. (c) Spec §16 (open questions) defers "Regional expansion = FSN + HEL pair at Gate A?" — when that lands, the per-host unix-socket model stops being meaningful and mTLS has to happen at the wire. We commit to that decision now.
- **Consequences:**
  - vmmd's listener calls `os.Chown(sockPath, uidFaas, gidFaas)` then `os.Chmod(sockPath, 0660)`. The ansible role installs the `faas` group and adds every vmmd-calling daemon user.
  - `pkg/wire/unixsock.ListenOrRecreate` (M1) wraps the chown/chmod in one helper so schedd's future RPC client to vmmd uses the same socket discipline.
  - **No** defense against an attacker who already has `faas` group membership (e.g. through some other vulnerability). The group is trusted; the model is "compromise-of-`faas`-group = compromise of all control-plane daemons" — same as a member of `wheel` on a typical Linux host. Treat `faas` group membership with the same care as `faas` user (which is currently rootless for everything except vmmd).
  - For Gate-A multi-host (spec §16), every `/run/faas/*.sock` becomes a TCP listener behind mTLS; this ADR is replaced with the multi-host variant.
- **Rejected alternatives:**
  - **mTLS for v1.0.** Spec is silent; nothing about the platform calls for it; the per-host unix socket is the simpler correct answer. Adopt only when multi-host forces a wire-level transport.
  - **`SO_PEERCRED` whitelist.** Same threat surface as group membership (a process that compromised a faas-group daemon can ALSO get the right creds). Adds complexity without adding protection.
  - **Allow any-uid vmmd in dev.** Reads nicely on a laptop but means a future operator script may have write access to the socket without any audit. Strict mode is cheap (one group); default to strict.

## Re-evaluation triggers

- **Gate-A multi-host**: replace group-only auth with mTLS (or unix-socket-on-tcp + mTLS depending on whether we keep the per-host daemon socket). Either is fine; the line is "no mTLS in v1.0."
