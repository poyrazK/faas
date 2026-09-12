-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS object_upload_routes (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,62}$'),
    bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
    key_prefix text NOT NULL DEFAULT '' CHECK (length(key_prefix) <= 256),
    max_bytes bigint NOT NULL CHECK (max_bytes > 0),
    allowed_content_types text[] NOT NULL DEFAULT '{}',
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (app_id, name)
);
CREATE INDEX IF NOT EXISTS object_upload_routes_app_idx ON object_upload_routes(account_id, app_id);

CREATE TABLE IF NOT EXISTS object_upload_completions (
    id uuid PRIMARY KEY,
    route_id uuid NOT NULL REFERENCES object_upload_routes(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
    subject_id text NOT NULL CHECK (length(subject_id) BETWEEN 1 AND 128),
    object_key text NOT NULL CHECK (length(object_key) BETWEEN 1 AND 1024),
    bytes bigint NOT NULL CHECK (bytes >= 0),
    content_type text NOT NULL DEFAULT '',
    etag text NOT NULL DEFAULT '',
    status text NOT NULL CHECK (status IN ('completed','rejected','failed')),
    error_code text NOT NULL DEFAULT '',
    request_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS object_upload_completions_route_created_idx ON object_upload_completions(route_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS object_upload_completions;
DROP TABLE IF EXISTS object_upload_routes;
-- +goose StatementEnd
