-- filename: 20261022174512936_dashboards.sql
-- migration-compat: expand
-- ADR-20261022173833: an account's saved dashboards. widgets is the JSON
-- array of typed widget references apid validates; reading a dashboard
-- resolves them, so nothing here goes stale. Deleted with the account.
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS dashboards (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name text NOT NULL,
    widgets jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT dashboards_pkey PRIMARY KEY (id),
    CONSTRAINT dashboards_account_name_uniq UNIQUE (account_id, name),
    CONSTRAINT dashboards_name_shape CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    CONSTRAINT dashboards_widgets_shape CHECK (jsonb_typeof(widgets) = 'array' AND jsonb_array_length(widgets) <= 24 AND octet_length(widgets::text) <= 32768)
);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS dashboards;
