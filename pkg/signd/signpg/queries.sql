-- signd's queries (ADR-20261109170000 §3).

-- name: BuildClaim :one
-- The deployment a signature request names, its app's slug and the image
-- preparation that claims its build. No row: no live deployment, or no claim.
SELECT d.status, d.sidecars, a.slug, p.node_name, p.phase
FROM deployments d
JOIN apps a ON a.id = d.app_id
JOIN deployment_image_preparations p ON p.deployment_id = d.id
WHERE d.id = sqlc.arg(deployment_id)::uuid AND d.deleted_at IS NULL;

-- name: InsertArtifactSignature :execrows
INSERT INTO artifact_signatures (storage_key, digest, bytes, deployment_id, node_name, signature_format)
VALUES (sqlc.arg(storage_key), sqlc.arg(digest), sqlc.arg(bytes), sqlc.arg(deployment_id)::uuid, sqlc.arg(node_name), sqlc.arg(signature_format))
ON CONFLICT (storage_key) DO NOTHING;

-- name: ArtifactSignatureByKey :one
SELECT storage_key, digest, bytes, deployment_id::text AS deployment_id, node_name, signature_format, signed_at
FROM artifact_signatures
WHERE storage_key = sqlc.arg(storage_key);

-- name: StoredLayers :many
-- Every signed layer a deployment that still exists can boot: its main layer
-- (legacy rows without rootfs_key keep layers/<id>.ext4) and its sidecars'.
-- The re-sign pass walks them (ADR-20261109170000 §5 step 3).
SELECT d.id::text AS deployment_id,
       (CASE WHEN d.rootfs_key <> '' THEN d.rootfs_key
             ELSE 'layers/' || d.id::text || '.ext4' END)::text AS storage_key
FROM deployments d
WHERE d.deleted_at IS NULL AND (d.rootfs_key <> '' OR COALESCE(d.rootfs_path, '') <> '')
UNION ALL
SELECT l.deployment_id::text AS deployment_id, l.storage_key
FROM deployment_sidecar_layers l
JOIN deployments d ON d.id = l.deployment_id
WHERE d.deleted_at IS NULL
ORDER BY 1, 2;
