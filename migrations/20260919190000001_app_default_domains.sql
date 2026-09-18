-- Release 1 / Deploy Confidence: durable per-app custom-domain default.
-- Keep this separate from apps so the feature does not widen the hot app row
-- or require a read-side app scan for every domain listing.
-- +goose Up
CREATE TABLE IF NOT EXISTS app_default_domains (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    domain citext NOT NULL REFERENCES custom_domains(domain) ON DELETE CASCADE,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS app_default_domains;
