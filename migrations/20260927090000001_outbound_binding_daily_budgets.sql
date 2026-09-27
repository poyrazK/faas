-- +goose Up
ALTER TABLE outbound_app_bindings
    ADD COLUMN IF NOT EXISTS daily_request_limit bigint;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'outbound_app_bindings_daily_request_limit_chk'
          AND conrelid = 'outbound_app_bindings'::regclass
    ) THEN
        ALTER TABLE outbound_app_bindings
            ADD CONSTRAINT outbound_app_bindings_daily_request_limit_chk
                CHECK (daily_request_limit IS NULL OR daily_request_limit BETWEEN 1 AND 100000000);
    END IF;
END$$;
-- +goose StatementEnd

-- Keep per-binding usage separate from integration-wide admission state so
-- each bound app can be metered independently without changing the existing
-- shared integration cap.
CREATE TABLE IF NOT EXISTS outbound_app_binding_usage (
    integration_id      uuid NOT NULL,
    app_id              uuid NOT NULL,
    daily_usage_date    date NOT NULL DEFAULT ((now() AT TIME ZONE 'UTC')::date),
    daily_request_count bigint NOT NULL DEFAULT 0 CHECK (daily_request_count >= 0),
    PRIMARY KEY (integration_id, app_id),
    CONSTRAINT outbound_app_binding_usage_binding_fk
        FOREIGN KEY (app_id, integration_id)
        REFERENCES outbound_app_bindings (app_id, integration_id)
        ON DELETE CASCADE
);

-- +goose Down
DROP TABLE IF EXISTS outbound_app_binding_usage;
ALTER TABLE outbound_app_bindings
    DROP CONSTRAINT outbound_app_bindings_daily_request_limit_chk,
    DROP COLUMN daily_request_limit;
