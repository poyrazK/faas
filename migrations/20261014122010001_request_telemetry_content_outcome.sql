-- ADR-1188: learned cacheability. How an anonymous GET body compared with
-- the previous response for its URL on the same gateway; empty when the
-- response was not observed. Never a body or hash.

-- +goose Up
ALTER TABLE request_telemetry ADD COLUMN IF NOT EXISTS content_outcome text NOT NULL DEFAULT '' CHECK (content_outcome IN (
 '', 'uncacheable', 'first',
 'same_60', 'same_300', 'same_3600', 'same_long',
 'changed_60', 'changed_300', 'changed_3600', 'changed_long'
));

-- +goose Down
ALTER TABLE request_telemetry DROP COLUMN IF EXISTS content_outcome;
