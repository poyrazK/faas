-- filename: 20261027220945429_agent_delivery_destinations.sql
-- migration-compat: expand

-- +goose Up
-- The new columns and the replaced CHECK lock agent_deliveries briefly; the
-- table is still dark-launched and small. New constraints are added NOT
-- VALID and validated separately.
SET LOCAL lock_timeout = '5s';
-- ADR-20261027090010 §8: deliveries may also write a run's output files to one of the
-- account's Gregale buckets, or mail the run's summary to the account's own
-- address. A bucket delivery records the bucket, the app that owns it and
-- the key prefix, fixed by the version.
ALTER TABLE agent_deliveries
    ADD COLUMN IF NOT EXISTS bucket_id uuid,
    ADD COLUMN IF NOT EXISTS bucket_app_id uuid,
    ADD COLUMN IF NOT EXISTS object_prefix text
        CHECK (object_prefix IS NULL OR octet_length(object_prefix) <= 256);
-- +goose StatementBegin
DO $do$
BEGIN
    IF position('''bucket''' IN (SELECT pg_get_constraintdef(oid) FROM pg_constraint
        WHERE conrelid = 'agent_deliveries'::regclass AND conname = 'agent_deliveries_destination_check')) = 0 THEN
        ALTER TABLE agent_deliveries DROP CONSTRAINT agent_deliveries_destination_check;
        ALTER TABLE agent_deliveries ADD CONSTRAINT agent_deliveries_destination_check
            CHECK (destination IN ('github_comment', 'bucket', 'email')) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
        WHERE conrelid = 'agent_deliveries'::regclass AND conname = 'agent_deliveries_bucket_check') THEN
        ALTER TABLE agent_deliveries ADD CONSTRAINT agent_deliveries_bucket_check CHECK (destination <> 'bucket'
            OR (bucket_id IS NOT NULL AND bucket_app_id IS NOT NULL AND object_prefix IS NOT NULL)) NOT VALID;
    END IF;
END;
$do$;
-- +goose StatementEnd
ALTER TABLE agent_deliveries VALIDATE CONSTRAINT agent_deliveries_destination_check;
ALTER TABLE agent_deliveries VALIDATE CONSTRAINT agent_deliveries_bucket_check;

-- +goose Down
-- Forward-only: keep recorded deliveries.
SELECT 1;
