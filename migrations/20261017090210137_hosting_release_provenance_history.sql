-- +goose Up
-- +goose StatementBegin
ALTER TABLE hosting_releases ADD COLUMN IF NOT EXISTS provenance jsonb
 CHECK (provenance IS NULL OR (jsonb_typeof(provenance)='object' AND octet_length(provenance::text)<=4096));
CREATE TABLE IF NOT EXISTS hosting_promotion_events (
 site_id uuid NOT NULL REFERENCES hosting_sites(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK (revision BETWEEN 2 AND 9007199254740991),
 previous_revision bigint NOT NULL CHECK (previous_revision=revision-1),
 release_id uuid NOT NULL,
 previous_release_id uuid,
 actor_account_id uuid NOT NULL,
 actor_key_id uuid,
 accepted_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(accepted_at)),
 provenance jsonb CHECK (provenance IS NULL OR (jsonb_typeof(provenance)='object' AND octet_length(provenance::text)<=4096)),
 PRIMARY KEY(site_id,revision)
);
-- Release and actor identifiers are snapshots, deliberately not FKs: normal
-- release/key retirement must not remove or prevent retained history.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM hosting_promotion_events) OR EXISTS(SELECT 1 FROM hosting_releases WHERE provenance IS NOT NULL) THEN
  RAISE EXCEPTION 'Recorded history/provenance requires explicit archival before downgrade';
 END IF;
END $$;
DROP TABLE hosting_promotion_events;
ALTER TABLE hosting_releases DROP COLUMN provenance;
-- +goose StatementEnd
