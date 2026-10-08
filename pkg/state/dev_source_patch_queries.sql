-- ADR-740 developer live source patches.

-- name: UpsertDevSourceManifest :exec
INSERT INTO dev_source_manifests (deployment_id, app_id, source_root, manifest)
VALUES (sqlc.arg(deployment_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(source_root)::text, sqlc.arg(manifest)::jsonb)
ON CONFLICT (deployment_id) DO UPDATE
SET source_root = EXCLUDED.source_root, manifest = EXCLUDED.manifest;

-- name: PruneDevSourceManifests :execrows
-- Keeps the newest keep_count manifests of the app plus the live
-- deployment's, which is the base every patch is computed against.
DELETE FROM dev_source_manifests m
USING deployments d
WHERE m.app_id = sqlc.arg(app_id)::uuid
  AND d.id = m.deployment_id
  AND d.status <> 'live'
  AND m.deployment_id NOT IN (
    SELECT k.deployment_id FROM dev_source_manifests k
    WHERE k.app_id = sqlc.arg(app_id)::uuid
    ORDER BY k.created_at DESC
    LIMIT sqlc.arg(keep_count)::int
  );

-- name: GetDevSourceManifest :one
SELECT deployment_id, app_id, source_root, manifest, created_at
FROM dev_source_manifests
WHERE deployment_id = sqlc.arg(deployment_id)::uuid;

-- name: InsertDevSourcePatch :one
-- Generations are dense per base deployment. A concurrent insert for the same
-- base collides on the unique key and the caller retries.
INSERT INTO dev_source_patches (app_id, base_deployment_id, generation, image_dir, archive, deleted, digest, expires_at)
SELECT sqlc.arg(app_id)::uuid, sqlc.arg(base_deployment_id)::uuid,
       COALESCE(MAX(p.generation), 0) + 1,
       sqlc.arg(image_dir)::text, sqlc.arg(archive)::bytea, sqlc.arg(deleted)::jsonb,
       sqlc.arg(digest)::text, sqlc.arg(expires_at)::timestamptz
FROM dev_source_patches p
WHERE p.base_deployment_id = sqlc.arg(base_deployment_id)::uuid
RETURNING id, generation, created_at;

-- name: LatestDevSourcePatch :one
SELECT id, app_id, base_deployment_id, generation, image_dir, archive, deleted, digest, created_at, expires_at
FROM dev_source_patches
WHERE app_id = sqlc.arg(app_id)::uuid
  AND base_deployment_id = sqlc.arg(base_deployment_id)::uuid
  AND generation > sqlc.arg(after_generation)::bigint
  AND expires_at > now()
ORDER BY generation DESC
LIMIT 1;

-- name: PruneDevSourcePatches :execrows
-- Patches for any other base deployment are obsolete once a newer build is
-- live; expired patches are never served.
DELETE FROM dev_source_patches
WHERE app_id = sqlc.arg(app_id)::uuid
  AND (expires_at <= now() OR base_deployment_id <> sqlc.arg(base_deployment_id)::uuid);
