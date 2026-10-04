-- ADR-532: retained queue retirement changes the reviewed binding inventory.
-- +goose Up
-- +goose StatementBegin
DROP TRIGGER IF EXISTS binding_promotion_revision ON queue_bindings;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON queue_bindings
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,account_id,app_id,name,queue_name,mode,enabled,retired_at');
-- +goose StatementEnd

-- +goose Down
-- Keep the conservative promotion fence when older binaries are restored.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
