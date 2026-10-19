-- name: HostingReleaseSetProvenance :execrows
UPDATE hosting_releases SET provenance=sqlc.arg(provenance)::jsonb WHERE id=sqlc.arg(id)::uuid AND provenance IS NULL AND state='pending';

-- name: HostingPromotionRecord :exec
INSERT INTO hosting_promotion_events(site_id,revision,previous_revision,release_id,previous_release_id,actor_account_id,actor_key_id,provenance)
VALUES(sqlc.arg(site_id)::uuid,sqlc.arg(revision)::bigint,sqlc.arg(previous_revision)::bigint,sqlc.arg(release_id)::uuid,
 sqlc.narg(previous_release_id)::uuid,sqlc.arg(actor_account_id)::uuid,sqlc.narg(actor_key_id)::uuid,sqlc.narg(provenance)::jsonb);

-- name: HostingPromotionPrune :exec
DELETE FROM hosting_promotion_events WHERE site_id=sqlc.arg(site_id)::uuid AND revision IN
 (SELECT revision FROM hosting_promotion_events WHERE site_id=sqlc.arg(site_id)::uuid ORDER BY revision DESC OFFSET sqlc.arg(retained)::integer);

-- name: HostingPromotionList :many
SELECT * FROM hosting_promotion_events WHERE site_id=sqlc.arg(site_id)::uuid
AND (sqlc.arg(before_revision)::bigint=0 OR revision<sqlc.arg(before_revision)::bigint)
ORDER BY revision DESC LIMIT sqlc.arg(page_size)::integer;
