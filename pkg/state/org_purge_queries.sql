-- ADR-061 amendment 1: release the slugs of shared organizations deleted
-- before requested_before, at most row_limit per call. The row stays.

-- name: PurgeDeletedOrgSlugs :many
WITH picked AS (
    SELECT id, slug
      FROM orgs
     WHERE deleted_pending
       AND NOT personal_org
       AND coalesce(deletion_requested_at, updated_at) < sqlc.arg(requested_before)::timestamptz
       AND slug NOT LIKE 'purged-%'
     ORDER BY coalesce(deletion_requested_at, updated_at)
     LIMIT sqlc.arg(row_limit)::int
       FOR UPDATE SKIP LOCKED)
UPDATE orgs o
   SET slug = 'purged-' || substr(replace(o.id::text, '-', ''), 1, 24), updated_at = now()
  FROM picked p
 WHERE o.id = p.id
RETURNING o.id::text AS id, p.slug::text AS old_slug;

-- name: RevokeOpenOrgInvitations :exec
UPDATE org_invitations
   SET revoked_at = now()
 WHERE org_id = ANY(sqlc.arg(org_ids)::uuid[])
   AND consumed_at IS NULL
   AND revoked_at IS NULL;
