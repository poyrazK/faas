# FaasHostSecurityEvent / FaasHostSecurityCritical

A host logged a security event that Alloy counted in
`faas_security_events_total{event}`
([ADR-20261107203000](../adr/20261107203000-host-security-evidence-off-host.md)).
Alloy ships the host's logins, privilege use, account changes, kernel
notices and audit log to Loki as they are written, under
`{job="faas-security"}`. Whatever an intruder deletes on the host afterwards
is still in Loki.

`FaasHostSecurityCritical` (page) fires on events host_hardening's settings
forbid. `FaasHostSecurityEvent` (warn) fires on events that can be
legitimate but must be explained.

## Events

| Event | Alert | Meaning |
|---|---|---|
| `ssh_root_login` | page | An SSH login as root. `PermitRootLogin no` should refuse it. |
| `ssh_password_login` | page | An SSH login with a password. Password authentication is off. |
| `audit_disabled` | page | The kernel audit was switched off (`auditctl -e 0`). |
| `sudo_denied` | warn | sudo refused someone: not in sudoers, wrong password, or a command not allowed. |
| `account_change` | warn | A local user or group was added, changed or removed, or a password changed. OS Login users are not local, so deploys do not do this. |
| `kernel_lockdown` | warn | The kernel refused something under lockdown, such as loading an unsigned module or reading kernel memory. |
| `kernel_taint` | warn | A module tainted the kernel: out-of-tree, unsigned or forced. |
| `audit_lost` | warn | The kernel dropped audit records (backlog or rate limit). Events in that window are missing. |
| `auditd_stopped` | warn | auditd stopped or aborted. Expected during a reboot or an auditd package upgrade. |
| `secret_read_interactive` | warn | A program in a login session read `/etc/faas/secrets`, and it was not Ansible (python3). A person, or a tool run by one, read a secret by hand. |
| `tetragon_jailed_setuid_root` | page | A process running as a Firecracker jailer uid (20000-29999) asked for root (`setuid`, `setreuid` or `setresuid` to 0). The jailer never does: treat it as a guest escape. Tetragon (ADR-20261110183000). |
| `tetragon_jailed_secret_open` | page | A process running as a jailer uid opened a file under `/etc/faas/secrets`. Treat it as a guest escape. |
| `tetragon_secret_open` | warn | A program not in `alloy_security_tetragon_secret_reader_exes` opened a file under `/etc/faas/secrets`, in or outside a login session. |
| `tetragon_module_load` | warn | A kernel module was loaded after boot (`init_module` or `finit_module`). |
| `tetragon_ptrace` | warn | A process attached to another (`PTRACE_ATTACH`, `PTRACE_SEIZE`) or wrote its memory (`process_vm_writev`). |
| `host_config_write_unattended` | warn | A program outside any login session changed sudoers, systemd units or `/etc/nftables.conf`, and it was not systemd, a package upgrade, nft or a platform binary under `/opt/faas`. |

`ssh_login` (a key login) is counted but never alerts: every deploy logs in.

## Look

All lines from the host around the event:

```bash
gregalectl obs logs --query '{job="faas-security", host="<host>"}' --since 1h
```

Only the event's lines:

```logql
{job="faas-security", host="<host>", event="<event>"}
```

Each journal line is the full journal entry (`_PID`, `_UID`, `_EXE`,
`_CMDLINE`, `MESSAGE`). Each audit line is the raw record. In a `SYSCALL`
record:

- `auid` is the login user's uid. `4294967295` means no login session: a
  service, a timer or a daemon.
- `exe` is the program, and `key` is the audit rule that matched.
- The matching `EXECVE` record has the same `msg=audit(<time>:<serial>)` and
  gives the command line. `PATH` and `CWD` records stay on the host in
  `/var/log/audit/audit.log` (ADR-20261107203000 §2).

Every root command a login session runs is in the audit stream with
`key="root_exec"`:

```logql
{job="faas-security", host="<host>", audit_key="root_exec"}
```

## Decide

**Explained.** It matches a change someone made on purpose: an operator
session, a deploy, or a package upgrade (`auditd_stopped`,
`host_config_write_unattended` by an upgrade's helper). Note who and why in
the alert thread and close it. If a legitimate program keeps raising
`host_config_write_unattended`, add it to
`alloy_security_unattended_writer_exes` instead of ignoring the alert.

**Not explained**, or any page event:

1. Treat the host as compromised. Follow
   [incident response](incident-response.md) and declare an incident. For a
   compute node, work through
   [compute-node-compromise.md](compute-node-compromise.md) instead of the
   steps below: it cuts the node's database access before draining it, which
   matters because a compute node can rewrite its own drain flag.
2. Take the host out of app traffic: drain it, so edgednsd withdraws its
   address records.
3. Do not log in to look around first. Read Loki: it holds what the host
   sent before anyone could tamper with it. A login on a compromised host
   also exposes your session.
4. Keep the host for forensics; do not reboot it unless it is attacking
   others. Snapshot its disks if the provider allows.
5. Rotate what the host held: its fleet certificate, the fleet and
   per-domain seal identities staged on it (see
   [FaasSealDomain](FaasSealDomain.md)) and, for a compute node, the
   apps-domain certificate key (see [FaasEdgeCertificate](FaasEdgeCertificate.md)).
6. Rebuild the host from the image; never return it to service as it is.

## When the stream stops

An intruder with root can stop Alloy. `FaasLokiPromtailDown` then fires for
that host after 10 minutes. On a host that also shows a security event, read
that alert as part of this one.

## Tetragon events (compute nodes)

The `tetragon_*` events come from Tetragon's kernel probes on compute nodes
where `faas_tetragon_enabled` is set
([ADR-20261110183000](../adr/20261110183000-tetragon-runtime-detection.md)).
The Loki line is Tetragon's full JSON event: the process (binary, arguments,
uid, pid, `exec_id`), its parent, the function and its arguments (the file
path for `faas-secret-open`). Look under
`{job="faas-security", source="tetragon", tetragon_policy="<policy>"}`.
On the host, `/var/log/tetragon/tetragon.log` holds the same events until it
rotates.

The event's uid is one VM's jailer uid. Its process and parent show which VM:
the jailer starts Firecracker with `--id <instance>`, and on the host
`ps -eo uid,pid,args | awk '$1 == <uid>'` lists that VM's processes. A `tetragon_jailed_*`
page is a guest escape until shown otherwise: drain the node and follow
[compute-node-compromise](compute-node-compromise.md).

A `tetragon_secret_open` by a program that does belong (a new platform tool,
say) is noise: add its path to `alloy_security_tetragon_secret_reader_exes`.

`FaasTetragonDown` fires when `tetragon.service` is installed but not active
for 10 minutes: nothing reports these events on that host until it runs
again.
