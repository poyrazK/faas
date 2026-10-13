-- +goose Up
-- ADR-747: customer-defined SLOs. apid writes definitions; the hourly
-- budget rows (meterd) arrive in a later migration.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS app_slos (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name text NOT NULL,
    sli text NOT NULL,
    latency_threshold_ms integer,
    objective_bp integer NOT NULL,
    window_days integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT app_slos_pkey PRIMARY KEY (id),
    CONSTRAINT app_slos_app_name_uniq UNIQUE (app_id, name),
    CONSTRAINT app_slos_name_shape CHECK (name ~ '^[a-z][a-z0-9_-]{0,62}$'),
    CONSTRAINT app_slos_sli_chk CHECK (sli IN ('availability', 'latency')),
    CONSTRAINT app_slos_latency_chk CHECK (
        (sli = 'availability' AND latency_threshold_ms IS NULL)
        OR (sli = 'latency' AND latency_threshold_ms IN (5, 10, 25, 50, 100, 250, 500, 1000, 2000, 5000, 10000))),
    CONSTRAINT app_slos_objective_chk CHECK (objective_bp BETWEEN 9000 AND 9999),
    CONSTRAINT app_slos_window_chk CHECK (window_days IN (7, 30))
);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS app_slos;
