# ADR-20261124150000: Operator disk pressure report

Status: Accepted

Extends ADR-20261115175927 (operator console routes) and
ADR-20261124093000 (operator gateway traffic report, whose Prometheus read
this follows).

## Context

Disk is the one node resource the operations console does not show. A
full snapshot or rootfs disk breaks wakes and builds without an obvious
cause. The jailer chroots live in a tmpfs (`/srv/fc/jail`), so files
leaked there hold RAM until reboot. Today the only signal is the
`FaasHostDisk*` alerts: free space under 15% (warn) or 5% (page),
under 25% and predicted full within 4 h (page), and inodes under 10%
(warn). Those alerts exclude tmpfs.

Snapshot sizes, the fleet average against the 130 MB target and the
largest snapshots are already on the Snapshot health page
(ADR-20261122135018). This report covers the hosts.

The data is already in Prometheus:

- node-exporter's `node_filesystem_{size,avail}_bytes` and
  `node_filesystem_files{,_free}` for every host (jobs `node`,
  `node-compute`, `node-database`). Compute targets carry the discovery
  `node` label.
- vmmd's `vmmd_guest_disk_{used,capacity}_bytes` and
  `vmmd_guest_disk_pressure` per live instance, and
  `vmmd_journal_disk_usage_bytes` per compute host.

## Decision

### One operator session read, Prometheus only

| Route | Returns |
| --- | --- |
| `GET /v1/admin/obs/disk-pressure` | `ObsDiskPressureReport` |

The route takes no parameters. It runs nine instant queries concurrently,
all at the same instant, under `ObsDiskPrometheusBudget` (3 s). Growth is
`deriv()` of free space over `ObsDiskGrowthWindow` (6 h), the window the
filling alert's `predict_linear` uses. No store query, no migration. apid
looks up the app of each listed guest (at most 25) by instance ID.

`pkg/apid/diskpressure` (ADR-982) reports:

- **Filesystems:** each host mount's size, free space, free inodes and
  hours to full while free space is falling (shown up to 7 days out).
  Each mount is graded with the alert thresholds, worst first:
  - `critical`: under 5% free;
  - `filling`: under 25% free and full within 4 h;
  - `low`: under 15% free;
  - `inodes_low`: under 10% of inodes free;
  - `ok`.

  tmpfs mounts are dropped except the jail mount, which is marked
  `jail_tmpfs` because its use is RAM.
- **Nodes:** each host's worst state and mount, jail tmpfs in use, journal
  size, and guests whose writable disk is near full or full.
- **Guests:** the 25 fullest guest writable disks with their app and
  account. vmmd's `instance` label arrives as `exported_instance`, because
  the scrape target has its own `instance`; only instance UUIDs are used.

`source` is `observed`, `partial` (the failed queries are named and their
fields are empty), `unavailable` or `not_configured`.

The route is operator-only (`requireOperatorSessionRead`), sends
`Cache-Control: no-store`, and is excluded from spec compliance. Thresholds
and limits are in `pkg/api/limits.go` (`ObsDisk*`). They copy the alert
rules' numbers, so a change to one must change the other.

## Consequences

- **Console:** gets a Disk pressure page under On call.
- **Alerts unchanged:** the page explains a `FaasHostDisk*` page and shows
  what is close to one. It adds no alert. The jail tmpfs is shown but
  still not alerted on.
- **Not covered:** the database nodes' Postgres volumes beyond their
  filesystem (managed Postgres has its own pages), and object storage.
