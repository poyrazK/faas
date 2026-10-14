-- filename: 20261013200356891_object_storage_billing_allowance.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-1320: a finalized object-storage period records the plan allowance that
-- was removed before pricing, next to the rates already copied into the row.
-- Existing periods were priced with no allowance, which the zero default
-- states exactly.
ALTER TABLE object_storage_billing_periods
    ADD COLUMN IF NOT EXISTS included_stored_byte_hours bigint NOT NULL DEFAULT 0
        CONSTRAINT object_storage_billing_periods_included_stored_byte_hours_check
        CHECK (included_stored_byte_hours >= 0 AND included_stored_byte_hours <= 1152921504606846976),
    ADD COLUMN IF NOT EXISTS included_requests bigint NOT NULL DEFAULT 0
        CONSTRAINT object_storage_billing_periods_included_requests_check
        CHECK (included_requests >= 0 AND included_requests <= 1152921504606846976),
    ADD COLUMN IF NOT EXISTS included_egress_bytes bigint NOT NULL DEFAULT 0
        CONSTRAINT object_storage_billing_periods_included_egress_bytes_check
        CHECK (included_egress_bytes >= 0 AND included_egress_bytes <= 1152921504606846976);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE object_storage_billing_periods
    DROP COLUMN IF EXISTS included_egress_bytes,
    DROP COLUMN IF EXISTS included_requests,
    DROP COLUMN IF EXISTS included_stored_byte_hours;
-- +goose StatementEnd
