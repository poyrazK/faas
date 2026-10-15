-- filename: 20261015161614983_static_hosting_publication_status.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE hosting_route_publications
    ADD COLUMN pending_since timestamptz,
    ADD COLUMN last_attempt_at timestamptz,
    ADD COLUMN retry_count bigint NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    ADD COLUMN last_error_code text NOT NULL DEFAULT '' CHECK (last_error_code IN ('', 'origin_unavailable', 'publication_unavailable', 'stale_generation'));
UPDATE hosting_route_publications SET pending_since = clock_timestamp() WHERE generation > published_generation;
ALTER TABLE hosting_route_publications
    ALTER COLUMN pending_since SET DEFAULT clock_timestamp(),
    ADD CONSTRAINT hosting_route_publications_pending CHECK ((generation > published_generation) = (pending_since IS NOT NULL));
CREATE INDEX hosting_route_publications_pending_since ON hosting_route_publications(pending_since)
    WHERE generation > published_generation;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX hosting_route_publications_pending_since;
ALTER TABLE hosting_route_publications
    DROP CONSTRAINT hosting_route_publications_pending,
    DROP COLUMN pending_since, DROP COLUMN last_attempt_at, DROP COLUMN retry_count, DROP COLUMN last_error_code;
-- +goose StatementEnd
