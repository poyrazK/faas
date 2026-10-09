-- filename: 20261009100000001_edge_rules_metadata_and_expiry.sql
--
-- Edge rules gain an operator-facing name and description, and an optional
-- expiry after which the gateway stops applying the rule (time-boxed
-- maintenance windows, temporary blocks). Expired rows are retained so the
-- listing can show what lapsed; the gateway read excludes them.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE edge_rules
    ADD COLUMN IF NOT EXISTS name        text,
    ADD COLUMN IF NOT EXISTS description text,
    ADD COLUMN IF NOT EXISTS expires_at  timestamptz;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'edge_rules_name_chk' AND conrelid = 'edge_rules'::regclass
    ) THEN
        ALTER TABLE edge_rules
            ADD CONSTRAINT edge_rules_name_chk
            CHECK (name IS NULL OR (length(btrim(name)) BETWEEN 1 AND 100));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'edge_rules_description_chk' AND conrelid = 'edge_rules'::regclass
    ) THEN
        ALTER TABLE edge_rules
            ADD CONSTRAINT edge_rules_description_chk
            CHECK (description IS NULL OR length(description) <= 1000);
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE edge_rules DROP CONSTRAINT IF EXISTS edge_rules_description_chk;
ALTER TABLE edge_rules DROP CONSTRAINT IF EXISTS edge_rules_name_chk;
ALTER TABLE edge_rules
    DROP COLUMN IF EXISTS expires_at,
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS name;
-- +goose StatementEnd
