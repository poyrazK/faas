-- +goose Up
-- +goose StatementBegin

-- Issue #96 / ADR-025 axis 2 (final slice, PR #116): the canonical
-- StorageBackend key for each deployment's drive1 layer ext4.
-- Mirrors migrations/00022_snapshots_storage_key.sql's shape — adds
-- the durable column that lets schedd's wake path carry a key on
-- the wire instead of a host path. Closes #96.
--
-- storage_key (snapshots, 00022) is the older sibling: it carries
-- the mem blob's storage identity. rootfs_key (this migration) is
-- the layer ext4's storage identity. The mem blob already flows
-- through StorageBackend end-to-end (vmmd Put on save, Storage.Get
-- on restore); the layer ext4's host path was the last asymmetry
-- blocking multi-node OCI cold-boot (issue #98 depends on this).
--
-- Backfill strategy: for the default apps root
-- (/var/lib/faas/apps/<slug>/<deployment_id>.ext4), translate to the
-- canonical key (apps/<slug>/<deployment_id>.ext4). Non-default
-- apps roots stay empty — imaged re-stamps them on the next build
-- via the new SetDeploymentRootfs signature.
--
-- Idempotent (IF NOT EXISTS) so re-runs during local development
-- don't require manual cleanup.

alter table deployments
  add column if not exists rootfs_key text not null default '';

-- Backfill from the legacy apps-rooted path. The substring surgery
-- is bounded by the apps root default; if a deployment was stamped
-- with a non-default apps root (rare; old test fixtures), the WHERE
-- skips it and imaged fills it in on the next build.
update deployments
   set rootfs_key = regexp_replace(rootfs_path, '^/var/lib/faas/apps/', 'apps/')
 where rootfs_key = ''
   and rootfs_path like '/var/lib/faas/apps/%'
   and rootfs_path <> '';

-- No new index: deployments are read by id (PK) or by app_id (already
-- indexed). rootfs_key is only ever read together with the deployment
-- row, so the PK index is sufficient.

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
alter table deployments drop column if exists rootfs_key;
-- +goose StatementEnd
