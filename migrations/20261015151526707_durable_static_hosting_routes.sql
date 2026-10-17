-- filename: 20261015151526707_durable_static_hosting_routes.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE hosting_sites
    ADD COLUMN IF NOT EXISTS durable_routing boolean NOT NULL DEFAULT false CHECK (durable_routing IN (true, false)),
    ADD COLUMN IF NOT EXISTS suspended boolean NOT NULL DEFAULT false CHECK (NOT suspended OR durable_routing);
ALTER TABLE hosting_releases
    ADD COLUMN IF NOT EXISTS retirement_generation bigint NOT NULL DEFAULT 0 CHECK (retirement_generation >= 0),
    ADD COLUMN IF NOT EXISTS retirement_published_at timestamptz CHECK (retirement_published_at IS NULL OR (state IN ('retiring', 'deleted') AND retirement_generation > 0));
CREATE TABLE IF NOT EXISTS hosting_route_publications (
    site_id uuid PRIMARY KEY REFERENCES hosting_sites(id) ON DELETE CASCADE,
    generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
    published_generation bigint NOT NULL DEFAULT 0 CHECK (published_generation >= 0 AND published_generation <= generation),
    published_at timestamptz,
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((published_generation > 0) = (published_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS hosting_route_publications_due ON hosting_route_publications(next_attempt_at, site_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM hosting_sites WHERE durable_routing) THEN
        RAISE EXCEPTION 'Retire/delete durable hosting sites and qualify edge shutdown before removing durable routing state';
    END IF;
END $$;
DROP TABLE hosting_route_publications;
ALTER TABLE hosting_releases DROP COLUMN retirement_published_at, DROP COLUMN retirement_generation;
ALTER TABLE hosting_sites DROP COLUMN suspended, DROP COLUMN durable_routing;
-- +goose StatementEnd
