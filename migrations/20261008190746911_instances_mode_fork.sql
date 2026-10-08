-- filename: 20261008190746911_instances_mode_fork.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-732: widen instances.mode with 'fork', a quarantined, non-serving
-- production fork. Forks bill like any live instance (only 'mirror' is
-- skipped by capture_instance_billing_interval), are never routed, and are
-- excluded from max_concurrency by the scheduler's admission ledger.
ALTER TABLE instances
    DROP CONSTRAINT IF EXISTS instances_mode_check;

ALTER TABLE instances
    ADD CONSTRAINT instances_mode_check
    CHECK (mode IN ('normal', 'mirror', 'job', 'worker', 'service', 'fork')) NOT VALID;

ALTER TABLE instances
    VALIDATE CONSTRAINT instances_mode_check;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Fails while fork rows exist; schedd destroys forks at their TTL, so wait
-- for them to end (or stop them) before rolling back.
ALTER TABLE instances
    DROP CONSTRAINT IF EXISTS instances_mode_check;

ALTER TABLE instances
    ADD CONSTRAINT instances_mode_check
    CHECK (mode IN ('normal', 'mirror', 'job', 'worker', 'service'));
-- +goose StatementEnd
