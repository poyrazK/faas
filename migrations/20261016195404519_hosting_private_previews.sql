-- +goose Up
-- +goose StatementBegin
DO $$ BEGIN
IF to_regclass('hosting_previews') IS NULL THEN
CREATE TABLE hosting_previews (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
 site_id uuid NOT NULL REFERENCES hosting_sites(id) ON DELETE RESTRICT,
 release_id uuid NOT NULL REFERENCES hosting_releases(id) ON DELETE RESTRICT,
 hostname text NOT NULL UNIQUE CHECK (length(hostname) BETWEEN 40 AND 253 AND hostname = lower(hostname)),
 origin_id text NOT NULL CHECK (origin_id ~ '^[0-9a-f]{64}$'),
 expires_at timestamptz NOT NULL,
 revoked boolean NOT NULL DEFAULT false,
 generation bigint NOT NULL DEFAULT 1 CHECK (generation IN (1, 2)),
 published_generation bigint NOT NULL DEFAULT 0 CHECK (published_generation BETWEEN 0 AND generation),
 published_at timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 last_error_code text NOT NULL DEFAULT '' CHECK (last_error_code IN ('', 'publication_unavailable', 'origin_unavailable')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (expires_at > created_at),
 CHECK (revoked = (generation = 2)),
 CHECK ((published_generation > 0) = (published_at IS NOT NULL))
);
-- Refresh existing durable heads with the explicit preview_allowed field.
UPDATE hosting_route_publications SET generation=generation+1,
 next_attempt_at=clock_timestamp(), pending_since=COALESCE(pending_since,clock_timestamp()),
 last_attempt_at=NULL,retry_count=0,last_error_code='';
END IF;
END $$;
CREATE INDEX IF NOT EXISTS hosting_previews_due ON hosting_previews(next_attempt_at, id);
CREATE INDEX IF NOT EXISTS hosting_previews_release ON hosting_previews(release_id);
CREATE INDEX IF NOT EXISTS hosting_previews_site ON hosting_previews(site_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM hosting_previews) THEN
  RAISE EXCEPTION 'Revoke previews and await safe cleanup before downgrade';
 END IF;
END $$;
DROP TABLE hosting_previews;
-- +goose StatementEnd
