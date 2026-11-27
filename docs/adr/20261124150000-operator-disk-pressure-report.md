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

  The grade compares the unrounded ratios, so it matches the alerts even
  where the rounded `free_percent` crosses a threshold. A mount whose free
  space was not read is `unknown`, with no free figures, never graded
  from a missing value. tmpfs mounts are dropped except the jail mount,
  which is marked `jail_tmpfs` with state `ram`: its use is RAM, the
  alerts exclude tmpfs, so it is shown but not graded and not part of its
  node's state.
- **Nodes:** each host's worst state and mount, jail tmpfs in use, journal
  size, and guests whose writable disk is near full or full. A host seen
  only through vmmd (no filesystem read) is `unknown`, not healthy. The
  control plane's static node-exporter target scrapes loopback, so its
  host is named `control-plane`; vmmd rows take their role from their job
  (`vmmd` or `vmmd-database`).
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

## Amendment 1 (2026-11-26): checked against production

The report's queries were run against the OVH fleet's Prometheus (bhs-1).
Labels matched (`node`/`node_id` on compute targets, `exported_instance` on
vmmd's guest series, the control plane's loopback target with no `node`),
and every query answered in under 0.3 s. Two differences changed the report:

- **The jail was invisible.** `/srv/fc/jail` is a root-owned tmpfs with mode
  1700, so the unprivileged node_exporter reports `device_error="permission
  denied"` and no size. vmmd, which is root, now exports
  `vmmd_jail_tmpfs_size_bytes` and `vmmd_jail_tmpfs_avail_bytes` (a statfs at
  scrape time; no sample when the read fails, never zero). The report reads
  them where node_exporter has no jail row. Until vmmd with these gauges is
  rolled out the jail shows no row and its use counts as zero.
- **Bind mounts were repeated.** `faas-fc-crypt` is mounted at `/srv/fc` and
  `/var/lib/faas` on every compute node, and also at the Postgres directories
  on the control plane. One block device on a node is now one filesystem,
  named by its shortest mountpoint, so its state and the counts are not
  repeated.
