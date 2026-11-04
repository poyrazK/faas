-- filename: 20261022091530218_log_saved_searches.sql
-- migration-compat: expand
-- ADR-20261018083536 slice D: an app's named log search filters, stored as
-- a canonical query string of filters only (no window), so the dashboard,
-- the CLI and log_search_matches alerts share one definition. apid is the
-- only writer; the app's deletion deletes its searches.
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS log_saved_searches (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name text NOT NULL,
    query text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT log_saved_searches_pkey PRIMARY KEY (id),
    CONSTRAINT log_saved_searches_app_name_uniq UNIQUE (app_id, name),
    CONSTRAINT log_saved_searches_name_shape CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    CONSTRAINT log_saved_searches_query_size CHECK (octet_length(query) <= 2048)
);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS log_saved_searches;
