-- +goose Up
-- ADR-747 slice 2: per-SLO hourly good/total counts, written by meterd from
-- the gateway's Prometheus series so a 30-day budget outlives Prometheus's
-- 15-day retention. `hour` is the start of the counted hour.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS app_slo_hourly (
    slo_id uuid NOT NULL REFERENCES app_slos(id) ON DELETE CASCADE,
    hour timestamp with time zone NOT NULL,
    good bigint NOT NULL,
    total bigint NOT NULL,
    CONSTRAINT app_slo_hourly_pkey PRIMARY KEY (slo_id, hour),
    CONSTRAINT app_slo_hourly_counts_chk CHECK (good >= 0 AND total >= good),
    CONSTRAINT app_slo_hourly_hour_chk CHECK (hour = date_trunc('hour', hour))
);
CREATE INDEX IF NOT EXISTS app_slo_hourly_hour_idx ON app_slo_hourly (hour);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS app_slo_hourly;
