-- +goose Up
ALTER TABLE hosting_ci_runs ADD COLUMN IF NOT EXISTS preview_reserved boolean NOT NULL DEFAULT false
 CHECK (preview_reserved IN (true,false));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM hosting_ci_runs WHERE preview_reserved) THEN
  RAISE EXCEPTION 'Preserve reserved CI retry identities until retiring the isolated pilot before downgrade';
 END IF;
END $$;
ALTER TABLE hosting_ci_runs DROP COLUMN preview_reserved;
-- +goose StatementEnd
