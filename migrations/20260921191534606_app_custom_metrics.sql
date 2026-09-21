-- filename: 20260921191534606_app_custom_metrics.sql
-- +goose Up
-- +goose StatementBegin

-- Customer-pushed application metrics used as a scaling signal (ADR-202).
--
-- Every other scaling signal is something the platform measures about an
-- app: request rate, CPU, in-flight requests, local queue depth, broker
-- consumer lag. This table holds the numbers only the app's own domain
-- knows -- unprocessed orders, documents awaiting OCR -- which are not
-- derivable from request traffic at all. An app can serve zero requests and
-- be catastrophically behind.
--
-- PUSHED rather than scraped, because a parked app has no process. A
-- scale-to-zero platform whose custom-metric signal requires a running
-- instance cannot scale from zero on it, which is the thing a backlog metric
-- most needs to do. The pusher may be the app, a cron, a database trigger,
-- or the customer's own infrastructure; the platform does not care which.
--
-- Keyed (app_id, name) so a push to an existing name is an UPSERT. That is
-- what bounds the table: only a NEW name can add a row, and the per-app name
-- cap (api.MaxCustomMetricsPerApp) is enforced at that point by apid. Worst
-- case is apps x cap rows, which is small next to instances -- and the
-- scaling trigger reads these rows every tick for every app it owns, so the
-- bound is a latency property, not just a storage one.
CREATE TABLE IF NOT EXISTS app_custom_metrics (
    app_id      uuid        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    -- name is the customer's identifier for the metric, matched against
    -- ScalingTarget.Name when metric='custom'. Constrained to the same
    -- shape as other customer-authored identifiers so it is safe in a
    -- Prometheus label and in a URL path.
    name        text        NOT NULL,
    -- value is the FLEET-TOTAL quantity (ADR-202): the scheduler computes
    -- ceil(value / target), the ClassBacklog arithmetic already used by
    -- queue_depth and queue_lag. Stored as double precision because a
    -- customer metric is not necessarily integral (a ratio, a seconds
    -- measure); the scheduler is what decides it means instances.
    --
    -- NOT NULL with a finite check: a NaN would become the numerator of a
    -- capacity calculation, and a negative backlog has no meaning.
    value       double precision NOT NULL,
    -- observed_at is the push time, and it is load-bearing. A value older
    -- than api.CustomMetricFreshness reports "no signal" to the arbiter
    -- rather than being used. If the pusher dies, the last value is frozen;
    -- treating a frozen backlog as current would pin the fleet at whatever
    -- it was when the pusher stopped, indefinitely, and bill for it.
    observed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, name),
    CONSTRAINT app_custom_metrics_name_shape
        CHECK (name ~ '^[a-z][a-z0-9_]{0,62}$'),
    CONSTRAINT app_custom_metrics_value_finite
        CHECK (value >= 0 AND value = value AND value < 'Infinity'::double precision)
);

-- The scaling trigger reads every metric for one app per tick, so the
-- primary key's leading app_id already serves that access path. No second
-- index: an extra index on a table written on every customer push costs
-- write amplification for a read that the PK already covers.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_custom_metrics;
-- +goose StatementEnd
