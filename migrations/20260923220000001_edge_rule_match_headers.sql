-- +goose Up
ALTER TABLE edge_rules
    ADD COLUMN IF NOT EXISTS match_headers jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'edge_rules_match_headers_shape_chk'
          AND conrelid = 'edge_rules'::regclass
    ) THEN
        ALTER TABLE edge_rules
            ADD CONSTRAINT edge_rules_match_headers_shape_chk
            CHECK (jsonb_typeof(match_headers) = 'object' AND jsonb_object_length(match_headers) <= 10);
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE edge_rules
    DROP CONSTRAINT IF EXISTS edge_rules_match_headers_shape_chk;
ALTER TABLE edge_rules
    DROP COLUMN IF EXISTS match_headers;
