-- +goose Up
-- +goose StatementBegin
-- API keys in one rotation family retain one stable identity. New, unrelated
-- keys receive independent identities from the column default.
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS runs_principal_id uuid NOT NULL DEFAULT gen_random_uuid();

CREATE OR REPLACE FUNCTION preserve_api_key_runs_principal() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    predecessor_principal uuid;
BEGIN
    IF NEW.rotated_from_id IS NOT NULL THEN
        SELECT runs_principal_id INTO predecessor_principal
          FROM api_keys
         WHERE id = NEW.rotated_from_id;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'rotated API key predecessor % does not exist', NEW.rotated_from_id;
        END IF;
        NEW.runs_principal_id := predecessor_principal;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS api_keys_preserve_runs_principal ON api_keys;
CREATE TRIGGER api_keys_preserve_runs_principal
    BEFORE INSERT ON api_keys
    FOR EACH ROW EXECUTE FUNCTION preserve_api_key_runs_principal();

-- Existing rows are intentionally left unowned: only their legacy broad
-- account principals can see them. New API-key-created runs stamp the owner.
ALTER TABLE executions ADD COLUMN IF NOT EXISTS runs_principal_id uuid;

CREATE INDEX IF NOT EXISTS executions_account_principal_created_idx
    ON executions (account_id, runs_principal_id, created_at DESC, id DESC)
    WHERE runs_principal_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM executions WHERE runs_principal_id IS NOT NULL) THEN
        RAISE EXCEPTION 'remove agent-owned executions before rolling back Run ownership';
    END IF;
END $$;

DROP INDEX IF EXISTS executions_account_principal_created_idx;
ALTER TABLE executions DROP COLUMN IF EXISTS runs_principal_id;
DROP TRIGGER IF EXISTS api_keys_preserve_runs_principal ON api_keys;
DROP FUNCTION IF EXISTS preserve_api_key_runs_principal();
ALTER TABLE api_keys DROP COLUMN IF EXISTS runs_principal_id;
-- +goose StatementEnd
