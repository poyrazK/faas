-- +goose Up
-- +goose StatementBegin
-- Repository workload names now participate in production service resolution
-- and access policies. Prior evidence must not survive the new routing rule.
CREATE OR REPLACE TRIGGER service_binding_revision AFTER INSERT OR UPDATE OR DELETE ON apps
 FOR EACH ROW EXECUTE FUNCTION capture_service_binding_revision('id,account_id,slug,workload_name,status,manifest,project_id,preview_of_slug,preview_pr_number,preview_pr_state,preview_expires_at,app_protocol,websocket_enabled');
UPDATE app_binding_promotion_revisions SET epoch=gen_random_uuid(),revision=revision+1,service_revision=service_revision+1
 WHERE app_id IN (SELECT id FROM apps WHERE status<>'deleted' AND COALESCE(manifest->'service_bindings','[]'::jsonb)<>'[]'::jsonb);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE TRIGGER service_binding_revision AFTER INSERT OR UPDATE OR DELETE ON apps
 FOR EACH ROW EXECUTE FUNCTION capture_service_binding_revision('id,account_id,slug,status,manifest,project_id,preview_of_slug,preview_pr_number,preview_pr_state,preview_expires_at,app_protocol,websocket_enabled');
UPDATE app_binding_promotion_revisions SET epoch=gen_random_uuid(),revision=revision+1,service_revision=service_revision+1
 WHERE app_id IN (SELECT id FROM apps WHERE status<>'deleted' AND COALESCE(manifest->'service_bindings','[]'::jsonb)<>'[]'::jsonb);
-- +goose StatementEnd
