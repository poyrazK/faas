-- +goose Up
ALTER TABLE hosting_ci_runs ADD COLUMN IF NOT EXISTS canceled boolean NOT NULL DEFAULT false CHECK (canceled IN (true,false));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM hosting_ci_runs WHERE canceled) THEN
  RAISE EXCEPTION 'Preserve canceled CI identities until retiring the isolated pilot before downgrade';
 END IF;
END $$;
ALTER TABLE hosting_ci_runs DROP COLUMN canceled;
-- +goose StatementEnd
