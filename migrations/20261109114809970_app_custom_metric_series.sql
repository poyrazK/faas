-- migration-compat: expand
-- filename: 20261109114809970_app_custom_metric_series.sql

-- +goose Up
-- Labelled custom metric series (ADR-20261108160000 slice C). An OTLP data
-- point with attributes is one series of its metric name: the row here holds
-- that series' latest value, and the metric's row in app_custom_metrics
-- holds the aggregate every name-only reader (alerts, release gates,
-- scaling, history) keeps reading: the sum for counters, the maximum of
-- fresh series for gauges. apid writes both in one transaction.
--
-- series_key is the canonical "k=v,k=v" form of labels (keys sorted), so the
-- primary key dedups a series however the SDK ordered its attributes. The
-- foreign key to the metric row cascades, so deleting a metric (by any apid
-- release) or its app removes its series too. Every step is re-runnable.
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS app_custom_metric_series (
    app_id      uuid             NOT NULL,
    name        text             NOT NULL,
    series_key  text             NOT NULL,
    labels      jsonb            NOT NULL,
    kind        text             NOT NULL,
    value       double precision NOT NULL,
    observed_at timestamptz      NOT NULL,
    CONSTRAINT app_custom_metric_series_pkey PRIMARY KEY (app_id, name, series_key),
    CONSTRAINT app_custom_metric_series_metric_fkey FOREIGN KEY (app_id, name)
        REFERENCES app_custom_metrics (app_id, name) ON DELETE CASCADE,
    -- '' is the unlabelled series of a name that also has labelled ones.
    CONSTRAINT app_custom_metric_series_key_check CHECK (octet_length(series_key) <= 1024),
    CONSTRAINT app_custom_metric_series_labels_check CHECK (jsonb_typeof(labels) = 'object'),
    CONSTRAINT app_custom_metric_series_kind_check CHECK (kind = ANY (ARRAY['gauge'::text, 'counter'::text])),
    CONSTRAINT app_custom_metric_series_value_check CHECK (value >= 0 AND value = value AND value < 'Infinity'::double precision),
    CONSTRAINT app_custom_metric_series_observed_at_check CHECK (isfinite(observed_at))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_custom_metric_series;
-- +goose StatementEnd
