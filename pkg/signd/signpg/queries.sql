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
