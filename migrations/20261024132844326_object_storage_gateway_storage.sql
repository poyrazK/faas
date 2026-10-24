-- migration-compat: expand
-- filename: 20261024132844326_object_storage_gateway_storage.sql

-- ADR-20261024132844: under gateway-safety accounting Gregale meters stored
-- bytes itself. apid accrues each bucket's last inventoried bytes into
-- stored byte-hours per UTC month, so ADR-1430's storage rate can be billed
-- without a provider usage report. accrued_through is the instant the row is
-- complete to; month close needs it at the end of the month (or the bucket's
-- deletion) before it bills.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS object_storage_gateway_storage (
    bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
    period_start timestamptz NOT NULL
        CHECK (period_start = (date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')),
    accrued_from timestamptz NOT NULL,
    accrued_through timestamptz NOT NULL,
    stored_byte_hours numeric NOT NULL DEFAULT 0
        CHECK (stored_byte_hours >= 0 AND stored_byte_hours <= 1152921504606846976),
    PRIMARY KEY (bucket_id, period_start),
    CHECK (accrued_from >= period_start AND accrued_through >= accrued_from
        AND accrued_through <= ((period_start AT TIME ZONE 'UTC') + interval '1 month') AT TIME ZONE 'UTC')
);
CREATE INDEX IF NOT EXISTS object_storage_gateway_storage_period_idx
    ON object_storage_gateway_storage (period_start, bucket_id);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS object_storage_gateway_storage;
