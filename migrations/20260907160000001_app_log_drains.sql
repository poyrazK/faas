-- filename: 20260907160000001_app_log_drains.sql
-- +goose Up
-- +goose StatementBegin

-- Issue #1398 O4 — customer-configurable, provider-neutral runtime log
-- destinations. Credentials are sealed by apid before this table is written.
CREATE TABLE IF NOT EXISTS app_log_drains (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    app_id uuid NOT NULL,
    account_id uuid NOT NULL,
    kind text NOT NULL,
    target_url text NOT NULL,
    auth_header_sealed bytea,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT app_log_drains_kind_chk CHECK (kind = ANY (ARRAY['http_json'::text, 'otlp'::text])),
    CONSTRAINT app_log_drains_target_url_len_chk CHECK ((char_length(target_url) >= 8) AND (char_length(target_url) <= 2048)),
    CONSTRAINT app_log_drains_pkey PRIMARY KEY (id),
    CONSTRAINT app_log_drains_app_id_fkey FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE,
    CONSTRAINT app_log_drains_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS app_log_drains_app_target_uniq ON app_log_drains (app_id, target_url);
CREATE INDEX IF NOT EXISTS app_log_drains_enabled_idx ON app_log_drains (enabled, app_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_log_drains;
-- +goose StatementEnd
