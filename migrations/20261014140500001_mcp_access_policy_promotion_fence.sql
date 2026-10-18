-- filename: 20261014140500001_mcp_access_policy_promotion_fence.sql
-- Serialize every account's edge-rule commits with guarded MCP promotions.
-- Deferred firing covers API, manifest, rollback and direct transactional writes.
-- +goose Up
-- The final schema object marks this transactional Up as applied. A ledger
-- replay must preserve definitions widened by later migrations and must not
-- re-run grant backfills or replace saved rollback constraint definitions.
-- +goose StatementBegin
DO $mcp_replay_guard$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='edge_rules'::regclass AND tgname='edge_rules_mcp_access_commit_fence_trg') THEN RETURN; END IF;
CREATE FUNCTION edge_rules_mcp_access_commit_fence() RETURNS trigger AS $$
DECLARE
    old_account uuid;
    new_account uuid;
    target uuid;
BEGIN
    IF TG_OP <> 'INSERT' THEN old_account := OLD.account_id; END IF;
    IF TG_OP <> 'DELETE' THEN new_account := NEW.account_id; END IF;
    FOR target IN
        SELECT DISTINCT account_id FROM unnest(ARRAY[old_account, new_account]) AS account_id
        WHERE account_id IS NOT NULL ORDER BY account_id
    LOOP
        PERFORM pg_advisory_xact_lock(hashtext('mcp_access_policy'), hashtext(target::text));
    END LOOP;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER edge_rules_mcp_access_commit_fence_trg
    AFTER INSERT OR UPDATE OR DELETE ON edge_rules
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION edge_rules_mcp_access_commit_fence();
END $mcp_replay_guard$;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER IF EXISTS edge_rules_mcp_access_commit_fence_trg ON edge_rules;
DROP FUNCTION IF EXISTS edge_rules_mcp_access_commit_fence();
