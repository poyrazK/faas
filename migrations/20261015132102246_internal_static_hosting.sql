-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS hosting_sites (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    environment text NOT NULL CHECK (length(environment) BETWEEN 1 AND 63),
    name text NOT NULL CHECK (name ~ '^[a-z0-9]([a-z0-9-]{0,46}[a-z0-9])?$'),
    hostname text NOT NULL UNIQUE CHECK (length(hostname) BETWEEN 1 AND 253 AND hostname = lower(hostname)),
    origin_id text NOT NULL CHECK (origin_id ~ '^[0-9a-f]{64}$'),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deleting')),
    active_release_id uuid,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT hosting_sites_environment_fk FOREIGN KEY (project_id, environment) REFERENCES project_environments(project_id, slug) ON DELETE RESTRICT,
    UNIQUE(account_id, project_id, environment, name)
);

CREATE TABLE IF NOT EXISTS hosting_releases (
    id uuid PRIMARY KEY,
    site_id uuid NOT NULL REFERENCES hosting_sites(id) ON DELETE CASCADE,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'ready', 'retiring', 'deleted')),
    manifest jsonb NOT NULL CHECK (jsonb_typeof(manifest) = 'object' AND octet_length(manifest::text) <= 1048576),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    retired_at timestamptz,
    CHECK ((state IN ('retiring', 'deleted')) = (retired_at IS NOT NULL)),
    UNIQUE(site_id, id)
);
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'hosting_sites'::regclass AND conname = 'hosting_sites_active_release_fk') THEN
        ALTER TABLE hosting_sites ADD CONSTRAINT hosting_sites_active_release_fk
            FOREIGN KEY (id, active_release_id) REFERENCES hosting_releases(site_id, id) DEFERRABLE INITIALLY DEFERRED;
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS hosting_releases_site ON hosting_releases(site_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
ALTER TABLE hosting_sites DROP CONSTRAINT hosting_sites_active_release_fk;
DROP TABLE hosting_releases;
DROP TABLE hosting_sites;
