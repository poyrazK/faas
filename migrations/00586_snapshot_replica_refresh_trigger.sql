-- +goose Up
-- +goose StatementBegin
-- Workstream B / ADR-137 follow-up (post-review).
--
-- Migration 00579's `DROP COLUMN active CASCADE` removed the
-- snapshot_replica_refresh_after_compute_node trigger (defined in
-- 00480) because the trigger was bound to `UPDATE OF active, region`.
-- The function body was preserved; only the trigger was dropped. With
-- `active` now STORED GENERATED from `lifecycle`, the trigger must
-- fire on `UPDATE OF lifecycle, region` (Postgres does not fire
-- triggers for changes to generated columns — they're derived from
-- their source columns, and the source column's change is what
-- matters).
--
-- The function body still reads `NEW.active`, which is fine: PG
-- populates generated columns on the NEW row before trigger
-- evaluation, so `NEW.active = (NEW.lifecycle IN ('active',
-- 'recovering'))` is correct at trigger time.
--
-- The same effect could be achieved by changing the trigger column
-- list to `UPDATE OF lifecycle, region`. The function reference stays.
DROP TRIGGER IF EXISTS snapshot_replica_refresh_after_compute_node
    ON compute_nodes;
CREATE TRIGGER snapshot_replica_refresh_after_compute_node
    AFTER INSERT OR UPDATE OF lifecycle, region ON compute_nodes
    FOR EACH ROW
    EXECUTE FUNCTION snapshot_replica_refresh_after_compute_node();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS snapshot_replica_refresh_after_compute_node
    ON compute_nodes;
CREATE TRIGGER snapshot_replica_refresh_after_compute_node
    AFTER INSERT OR UPDATE OF active, region ON compute_nodes
    FOR EACH ROW
    EXECUTE FUNCTION snapshot_replica_refresh_after_compute_node();
-- +goose StatementEnd