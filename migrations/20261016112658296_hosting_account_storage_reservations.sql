-- filename: 20261016112658296_hosting_account_storage_reservations.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE hosting_releases ADD COLUMN IF NOT EXISTS artifact_bytes bigint;
UPDATE hosting_releases r SET artifact_bytes = (
 SELECT COALESCE(sum((file->>'size')::bigint), 0)
 FROM jsonb_array_elements(r.manifest->'files') AS file
) WHERE artifact_bytes IS NULL;
ALTER TABLE hosting_releases ALTER COLUMN artifact_bytes SET NOT NULL;
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'hosting_releases'::regclass AND conname = 'hosting_releases_artifact_bytes_check') THEN
        ALTER TABLE hosting_releases ADD CONSTRAINT hosting_releases_artifact_bytes_check
            CHECK (artifact_bytes BETWEEN 0 AND 104857600);
    END IF;
END $$;
-- No default: older writers must fail rather than admit unaccounted releases.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM hosting_releases) THEN
  RAISE EXCEPTION 'drain the internal hosting pilot before dropping storage accounting';
 END IF;
END $$;
ALTER TABLE hosting_releases DROP COLUMN artifact_bytes;
-- +goose StatementEnd
