# `log_archive` ansible role

Operator wiring for the issue #562 log archive pipeline. Two
daemons share one credential envelope:

| daemon            | role                                  |
| ----------------- | ------------------------------------- |
| `apid`            | writes per-instance logs to the bucket (PR-A shipper) |
| `gatewayd-internal` | reads per-day archives back on `?archive=1` (PR-B handler) |

The Go-level wiring (config parsing, SigV4 sign, S3 client) lives
in `pkg/logarchive`. Both daemons read the same provider-neutral
S3 settings plus the same systemd-staged `archive-creds.json`
envelope. This role is the host-level glue: where the source
envelope lives, what perms it must have, and where the local
spool root sits.

## Where the bucket comes from

The manifest's `log_archive` section (see `deploy/manifest/README.md`).
`gregalectl manifest ansible` renders it into every host's inventory
`host_vars`, and this role writes it into `archive-creds.json`:

```yaml
log_archive:                 # keyless GCS: the host's attached service account
  bucket: gregale-prod-280686321025-log-archive
  endpoint: https://storage.googleapis.com
  auth: gcp_metadata
```

```yaml
log_archive:                 # static S3 keys (OVH, R2, MinIO)
  bucket: gregale-logs
  endpoint: https://s3.gra.io.cloud.ovh.net
  region: gra
  auth: static
  credentials_src: /operator/secrets/log-archive.json  # {"key_id": "…", "secret": "…"}
```

The key file stays on the controller; the role reads it with `no_log` and
merges the pair into the envelope. Without a `log_archive` section the role
leaves each host's envelope as the operator provisioned it, and
`faas_log_archive_required` (set for both node groups) fails a host that has
none. The repo carries no bucket name: the one it used to carry belonged to a
closed project.

## What this role does

1. Fail-closed asserts on
   `/etc/faas/secrets/storage-box/archive-creds.json`
   (mode `0400`, owner `root`, group `root` — spec §11). PID 1
   reads this source and systemd stages it for both daemons via
   `LoadCredential=` so the
   plaintext never appears on the host filesystem outside
   the unit's `$CREDENTIALS_DIRECTORY`.
2. Creates `/var/log/faas/archive` (`faas:faas`, mode `0750`).
   The apid shipper writes `.partial` files here on the way
   to the bucket; gatewayd-internal does NOT touch this dir
   (read-back is bucket-only, not spool).
3. Removes the legacy gatewayd-internal archive drop-in. The
   gatewayd-internal base unit owns the optional `LoadCredential=`
   declaration, so the feature is safe before the envelope exists.
4. Asserts the spool root mode is `≤ 0750` (spec §11).
5. Runs `systemctl daemon-reload` after removing the legacy drop-in.

## What this role does NOT do

- **No cron purge unit.** The apid shipper's `PurgeInterval`
  ticker (`pkg/logarchive/shipper.go:125`) already sweeps the
  local spool on the same cadence the spec calls for. Adding
  a second systemd timer would race the in-process purger on
  the same files; the apid shipper is the canonical owner.
  Bucket-side retention is enforced by the customer's plan
  (Hobby 7d / Pro 30d / Scale 90d — plan-gated at the
  gatewayd-internal handler) and by the S3 lifecycle policy
  the operator attaches at bucket-provision time (see the
  runbook).
- **No `gregalectl backup unseal-archive-creds` invocation.** That
  CLI ships in PR-A and the operator runs it once during
  `bootstrap.sh` step 11d (RETIRED 2026-08-15 by issue #911 / PR-1;
  v2 path is PR-X `gregalectl secrets init`) — mirrors the `unseal-rclone`
  flow at `deploy/ansible/roles/postgres/files/postgresql@.service.d/
  99-faas-off-host-backup.conf`. This role assumes the envelope
  is already on disk; the assert catches any future perms
  drift.
- **No daemon restart.** The role only removes the legacy drop-in
  and reloads systemd; operators choose when to restart a deployed
  daemon so the role never silently bounces the edge.
- **No S3 lifecycle policy attachment.** That's an operator
  action on the bucket itself; see `docs/runbooks/
  FaasLogArchiveShipperDegraded.md` "Bucket lifecycle
  policy" section for the recommended shape.

## Idempotency

`file` for the spool root and legacy drop-in removal are no-ops on a
converged box; the asserts are read-only. Re-running on a converged
host produces zero `changed`.

## Refs

- Issue #562 (acceptance matrix)
- `pkg/logarchive` (PR-A: shipper + config)
- `cmd/gatewayd-internal/app_logs_archive.go` (PR-B: read-back)
- `deploy/ansible/roles/control_plane_service/files/faas-apid.service.d/99-faas-log-archive.conf` (sibling drop-in)
- `docs/runbooks/FaasLogArchiveShipperDegraded.md` (operator runbook)
