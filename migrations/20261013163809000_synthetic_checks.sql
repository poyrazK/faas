-- +goose Up
-- ADR-748: customer-defined synthetic HTTP checks. apid writes definitions;
-- meterd's run history arrives in a later migration. The path is
-- origin-relative (the host is always the app's own), and the shortest
-- interval is five minutes so a check cannot pin an app resident.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS synthetic_checks (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name text NOT NULL,
    method text NOT NULL,
    path text NOT NULL,
    expected_status integer,
    timeout_ms integer NOT NULL,
    interval_seconds integer NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT synthetic_checks_pkey PRIMARY KEY (id),
    CONSTRAINT synthetic_checks_app_name_uniq UNIQUE (app_id, name),
    CONSTRAINT synthetic_checks_name_shape CHECK (name ~ '^[a-z][a-z0-9_-]{0,62}$'),
    CONSTRAINT synthetic_checks_method_chk CHECK (method IN ('GET', 'HEAD')),
    CONSTRAINT synthetic_checks_path_chk CHECK (char_length(path) <= 512 AND path ~ '^/([^/[:space:]\\][^[:space:]\\]*)?$'),
    CONSTRAINT synthetic_checks_status_chk CHECK (expected_status IS NULL OR expected_status BETWEEN 100 AND 599),
    CONSTRAINT synthetic_checks_timeout_chk CHECK (timeout_ms BETWEEN 1000 AND 30000),
    CONSTRAINT synthetic_checks_interval_chk CHECK (interval_seconds IN (300, 900, 3600))
);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS synthetic_checks;
