# dbbackupd_service

Installs dbbackupd (ADR-1027 durability), the archive keeper of native
managed databases on a database node. vmmd relays each database VM's
`archive_command`/`restore_command` and base-backup streams to its unix
socket after stamping the database id; dbbackupd stores them on the node's
NVMe and ships them to an S3-compatible bucket. Run it through
`deploy/ansible/database_node.yml` (`make bootstrap-database-nodes`).

When `dbbackupd_enabled: true`, the `database_vmmd` role also renders
`archive_socket` into vmmd.toml, which turns WAL archiving on in every
database VM booted afterwards. Point-in-time restore is offered by the
backend only once its settings carry `max_restore_window_seconds`.

| Variable | Meaning |
| --- | --- |
| `dbbackupd_store_dir` | local archive (default `/srv/db/archive`) |
| `dbbackupd_min_retention`, `dbbackupd_prune_interval` | every archive restores this far back (default 24h; a database's restore window keeps more), pruned this often |
| `dbbackupd_s3_bucket`, `dbbackupd_s3_prefix` | off-node copy; empty bucket keeps it on the node only |
| `dbbackupd_s3_endpoint`, `dbbackupd_s3_region`, `dbbackupd_s3_path_style` | S3-compatible endpoint |
| `dbbackupd_s3_access_key_id`, `dbbackupd_s3_secret_access_key` | prefix-scoped credentials (Vault) |
| `dbbackupd_offsite_s3_*`, `dbbackupd_offsite_upload_interval` | off-site mirror: a second bucket, at another provider, with its own credentials; needs the bucket above; empty bucket turns it off |

Files written on the host:

- `/etc/faas/dbbackupd.toml`: configuration.
- `/etc/faas/dbbackupd/s3-{access-key-id,secret-access-key}` and, with a
  mirror, `offsite-s3-{access-key-id,secret-access-key}`: owner-only
  files of `faas-dbbackupd`; the daemon refuses group- or world-readable
  secrets.

Deleting a database deletes its archive (local and bucket) before vmmd
deletes its volume.

Operations: `/healthz`, `/readyz` and `/metrics` on `dbbackupd_ops_addr`:
port 9110 on the overlay address when the manifest renders
`database_node_metrics_host` (control-plane Prometheus job `dbbackupd`),
loopback otherwise.
`dbarchive_pending_oldest_seconds` is the off-node recovery point: how old
the oldest archive file still only on this node is.
`dbarchive_offsite_pending_oldest_seconds` is the mirror's: how old the
oldest file, deletion, hold or handoff not yet applied off-site is.
