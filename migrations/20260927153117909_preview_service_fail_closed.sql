-- +goose Up
-- Revoke the compatibility backfill. Customers who need production calls
-- from previews can explicitly re-enable allow_marked through the policy API.
update github_deploy_policies
   set preview_service_policy = 'deny', updated_at = now()
 where preview_service_policy = 'allow_marked';

-- +goose Down
-- Intentionally no-op: restoring an implicit production-service grant would
-- silently weaken preview isolation after a rollback.
