# ADR-20261110183000: Compute nodes report suspicious kernel activity with Tetragon

- Status: Accepted
- Date: 2026-11-10
- Related: ADR-20261107203000 (host security evidence off host),
  ADR-20261107220000 (kernel lockdown), ADR-20261022111812 (seal domains),
  `docs/runbooks/compute-node-compromise.md`

## Context

Compute nodes run untrusted guest code. A guest that escapes Firecracker
lands as the VM's jailer uid (20000-29999) inside a chroot. ADR-20261107203000
ships logins, sudo, account changes, kernel notices and the auditd record off
the host, but none of those see what an escaped process does next: ask for
root, open the node's keys, load a module or attach to another process.

## Decision

1. **Tetragon on compute nodes, observe only.** The `tetragon` role installs a
   pinned release (`tetragon_version`, checked against the release's
   sha256) into its own directory and runs it as a systemd unit with
   `CPUWeight=20` and a memory cap. `faas_tetragon_enabled` turns it on per
   environment; it is off by default. No policy blocks or kills: enforcement
   comes after production data shows the false-positive rate.
2. **Four policies, filtered in the kernel:**
   - `faas-setuid-root`: `setuid`, `setreuid`, `setresuid` asking for uid or
     euid 0;
   - `faas-secret-open`: any open under `/etc/faas/secrets/`;
   - `faas-module-load`: `init_module`, `finit_module`;
   - `faas-ptrace`: `PTRACE_ATTACH`, `PTRACE_SEIZE`, `process_vm_writev`.
3. **Only those events leave Tetragon.** The export allowlist keeps kprobe
   events only (no exec or exit stream), the export is rate-limited and
   rotated, and its file is readable by `adm`.
4. **The existing pipeline carries them.** Alloy tails the export into the
   `job="faas-security"` stream (`source="tetragon"`, label
   `tetragon_policy`) and counts in `faas_security_events_total{event}`:

   | event | alert | when |
   |---|---|---|
   | `tetragon_jailed_setuid_root` | page | a process with a jailer uid asks for root |
   | `tetragon_jailed_secret_open` | page | a process with a jailer uid opens a node secret |
   | `tetragon_secret_open` | warn | a node secret opened by a program outside `alloy_security_tetragon_secret_reader_exes` |
   | `tetragon_module_load` | warn | a kernel module load |
   | `tetragon_ptrace` | warn | a ptrace attach or cross-process memory write |

   `FaasTetragonDown` warns when the unit is installed but not active.

## Consequences

- An escape that asks for root, reads the node's keys, loads a module or
  attaches to a daemon is in Loki and alerts within the alert interval, even
  if the attacker later wipes the node.
- `security_file_open` runs on every file open; the prefix match is done in
  BPF, so the cost is one string compare per open.
- Tetragon itself runs as root with BPF; it is another root daemon to keep
  patched. Kernel lockdown (integrity) still allows it.
- The control plane does not run it yet.

## Amendment (2026-11-10): release integrity

Every control-plane and compute host runs `gregalectl release verify` from an
hourly timer (`release_integrity` role). It verifies the current release
against `release-manifest.json`, the manifest embedded in and signed with the
release tarball (`releaseinstall.Verify`), and checks every process whose
executable is under `/opt/faas`: it must run from an installed release that
verifies, from a file not replaced since it started. The node join pre-stage,
the compute edge's pinned cloudflared and CI canaries are skipped; an audit
hotfix is reported.

It writes `faas_release_integrity_ok`, a check timestamp and
`faas_release_manifest_info{release,digest}`, the sha256 of the signed
manifest bytes, and logs failures as `faas-release-integrity`, which Alloy
ships off the host. Alerts: `FaasReleaseIntegrityFailed` (page),
`FaasReleaseIntegrityStale` (warn, no check for 3 hours) and
`FaasReleaseManifestDiverged` (page, hosts on one release report different
digests: a manifest edited to cover a swapped binary).

The check runs on the host it checks, so root there can stop or fake it. The
evidence it leaves off the host (stale alert, Loki lines, the other hosts'
digests) is what it adds; remote attestation (TPM, IMA) is not covered.
Runbook: [FaasReleaseIntegrity](../runbooks/FaasReleaseIntegrity.md).
