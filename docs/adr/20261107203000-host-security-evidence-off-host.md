# ADR-20261107203000: Host security evidence leaves the host as it is written

- **Status:** accepted
- **Date:** 2026-11-07
- **Amends:** ADR-20261023185953 (Alloy replaces Promtail): Alloy also ships
  a second, security stream. ADR-143 (host hardening): auditd's log is
  readable by the `adm` group.
- **Related:** ADR-20261103163954 (app hosts point straight at the nodes),
  ADR-20261104221012 (edge security round 7), ADR 20261024014500
  (honeytokens)

## Context

With ADR-20261103163954, compute nodes take app traffic from the internet
with nothing in front, so a compute node is now the first host an attacker
reaches. host_hardening makes root hard to get: key-only SSH through OS
Login, no root login, fail2ban, security updates, sysctls and auditd. It
does little once someone has root:

- **The evidence stays on the host.** Alloy ships an allowlist of platform
  units to Loki on the control plane. sshd, sudo, account changes, kernel
  messages and the auditd log never leave the host. auditd already watches
  `/etc/faas/secrets`, systemd units, sudoers, nftables and every root
  command a login session runs, but whoever has root can delete those
  records along with the journal.
- **Nothing alerts.** No rule reads those logs, and Loki has no ruler.

## Decision

### 1. A security stream, shipped as it is written

Alloy reads three more sources, all labelled `job="faas-security"` with the
host's `host` and `environment` labels and a `source` label:

- **Journal** (`source="journal"`): entries whose `SYSLOG_IDENTIFIER` is
  sshd or sshd-session, sudo, su, or one of the shadow tools that change
  users, groups or passwords (`alloy_security_identifiers`). The `ident`
  label carries the identifier.
- **Kernel** (`source="kernel"`): kernel messages at notice or more severe.
  Lockdown refusals are notices. Bridge and tap port changes are info and
  stay local.
- **auditd** (`source="auditd"`): `/var/log/audit/audit.log`, read from its
  end on Alloy's first start so old history does not page. `audit_type` and
  `audit_key` (the rule key, a closed set from host_hardening) become
  labels.

A journal line is the whole journal entry as JSON, so the pid, uid,
executable and command line travel with it. An audit line is the raw
record.

Loki on the control plane already keeps 14 days. A host's compromise does
not reach the copy it has already sent. A control-plane compromise does, and
is out of scope here: Loki's S3 backend with object lock is the follow-up.

### 2. What stays on the host

The audit record types that only add detail to an event already shipped
(`PATH`, `CWD`, `PROCTITLE`, `SOCKADDR`), and those platform daemons emit all
day (`NETFILTER_CFG` from vmmd's nftables changes, systemd's `BPF`,
`SERVICE_START` and `SERVICE_STOP`), stay in the local log
(`alloy_security_audit_drop_types`). The `SYSCALL` and `EXECVE` records of
the same event do ship, so the program, the login user and the command line
are off the host.

### 3. Alloy counts the events; Prometheus alerts

Loki has no ruler, and adding one would put a second alert path beside
Prometheus. Alloy instead labels a line with `event` when it matches one of
a fixed set of selectors and counts it in `faas_security_events_total{event}`
on the host's own metrics endpoint, which Prometheus already scrapes. The
counter's series never idle out; the alerts also fire on a series that did
not exist 15 minutes before, because a counter's first value is invisible to
`increase()`.

| Event | Selector | Alert |
|---|---|---|
| `ssh_login` | sshd `Accepted publickey` | none (deploys log in) |
| `ssh_password_login` | sshd `Accepted password` or keyboard-interactive | page |
| `ssh_root_login` | sshd `Accepted … for root` | page |
| `audit_disabled` | `CONFIG_CHANGE` with `audit_enabled=0` | page |
| `sudo_denied` | sudo refusals | warn |
| `account_change` | any shadow-tool line | warn |
| `kernel_lockdown` | kernel `Lockdown:` | warn |
| `kernel_taint` | kernel `taints kernel` / `tainting kernel` | warn |
| `audit_lost` | kernel audit backlog or rate limit | warn |
| `auditd_stopped` | `DAEMON_END`, `DAEMON_ABORT` | warn |
| `secret_read_interactive` | `faas_secrets` `SYSCALL` in a login session (`auid` set) by a program other than python3 (Ansible) | warn |
| `host_config_write_unattended` | successful `sudo`, `faas_units` or `faas_nft` `SYSCALL` outside any login session (`auid` unset) by a program other than systemd, package tools, nft or `/opt/faas` | warn |

The page events cannot happen under host_hardening's settings. The warn
events have legitimate causes (an operator, a deploy, an upgrade) and the
runbook (`docs/runbooks/FaasHostSecurityEvent.md`) says how to tell.

The two auditd selectors read `auid`, the login uid the kernel keeps
across sudo and setuid: a daemon or timer has none, an SSH session (an
operator's or Ansible's) has one. They cannot tell a deploy from an intruder
who logged in with a stolen key and runs python3; the stream still records
what that session did.

### 4. auditd's log is readable by `adm`

host_hardening sets `log_group = adm` in auditd.conf and keeps
`/var/log/audit` at `root:adm 0750`. Alloy is already in `adm` for the
journal. Ubuntu 24.04 ships the same values; the role makes them explicit.

## Consequences

- A host's logins, privilege use and audit trail survive its compromise.
- Shipping volume grows with root commands run by login sessions
  (`root_exec`), mostly during deploys. Each command is two records
  (`SYSCALL`, `EXECVE`).
- An intruder with root can still stop Alloy: from then on nothing ships.
  `FaasLokiPromtailDown` fires for the host after 10 minutes, and the
  runbook reads it as part of a security event.
- Rule changes in host_hardening that add audit keys need a matching
  selector here, or they ship without alerting.

## Not decided here

- Kernel lockdown and disabling module loading after boot (the next step).
- Runtime detection (Falco or Tetragon) and an out-of-band check of the
  platform binaries.
- Loki on S3 with object lock, so a control-plane compromise cannot erase
  the copies either.
