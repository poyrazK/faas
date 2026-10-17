-- +goose Up
-- +goose StatementBegin
DO $$ BEGIN
IF NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'hosting_releases'::regclass AND attname = 'first_promoted_at' AND NOT attisdropped) THEN
ALTER TABLE hosting_releases ADD COLUMN first_promoted_at timestamptz
 CHECK (first_promoted_at IS NULL OR (isfinite(first_promoted_at) AND first_promoted_at >= created_at));
-- Ready releases were previously available through the routing inventory, even
-- without explicit promotion. Preserve that legacy visibility during upgrade.
UPDATE hosting_releases SET first_promoted_at = created_at WHERE state = 'ready';
END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Rolling back would expose never-promoted ready releases to old publishers.
-- Retire those releases and drain all publishers before an operator downgrade.
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM hosting_releases WHERE state = 'ready' AND first_promoted_at IS NULL) THEN
  RAISE EXCEPTION 'retire never-promoted ready releases before downgrading hosting';
 END IF;
END $$;
ALTER TABLE hosting_releases DROP COLUMN first_promoted_at;
-- +goose StatementEnd
