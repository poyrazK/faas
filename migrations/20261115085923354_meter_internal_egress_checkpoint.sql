-- migration-compat: expand
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

-- Billing review R8-2 (ADR-20261114100000): meterd subtracts the bytes a guest
-- sent to internal destinations from usage_minutes.net_tx_bytes. vmmd reports
-- them as a cumulative counter per instance; the checkpoint keeps the last
-- reading meterd committed and the bytes it could not yet subtract because
-- the minute's row had fewer egress bytes than that (the internal counter is
-- polled every 15 s, the veth counter every 250 ms). service_reply_pending
-- queues the bytes the gateway saw the instance send back to other apps'
-- service calls until the next subtraction. Expand only: older
-- meterds never read or write these columns.
ALTER TABLE meter_network_checkpoints
  ADD COLUMN IF NOT EXISTS internal_tx_bytes bigint NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS internal_tx_valid boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS internal_tx_pending bigint NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS service_reply_pending bigint NOT NULL DEFAULT 0;

-- Every existing row holds the default 0, so NOT VALID loses nothing: new
-- writes are checked at once and no scan holds the table. The guards keep a
-- replay of this migration from failing on the constraints it already made.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint
                  WHERE conrelid = 'meter_network_checkpoints'::regclass
                    AND conname = 'meter_network_checkpoints_internal_tx_bytes_check') THEN
    ALTER TABLE meter_network_checkpoints
      ADD CONSTRAINT meter_network_checkpoints_internal_tx_bytes_check CHECK (internal_tx_bytes >= 0) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint
                  WHERE conrelid = 'meter_network_checkpoints'::regclass
                    AND conname = 'meter_network_checkpoints_internal_tx_pending_check') THEN
    ALTER TABLE meter_network_checkpoints
      ADD CONSTRAINT meter_network_checkpoints_internal_tx_pending_check CHECK (internal_tx_pending >= 0) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint
                  WHERE conrelid = 'meter_network_checkpoints'::regclass
                    AND conname = 'meter_network_checkpoints_service_reply_pending_check') THEN
    ALTER TABLE meter_network_checkpoints
      ADD CONSTRAINT meter_network_checkpoints_service_reply_pending_check CHECK (service_reply_pending >= 0) NOT VALID;
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE meter_network_checkpoints
  DROP COLUMN IF EXISTS service_reply_pending,
  DROP COLUMN IF EXISTS internal_tx_pending,
  DROP COLUMN IF EXISTS internal_tx_valid,
  DROP COLUMN IF EXISTS internal_tx_bytes;
-- +goose StatementEnd
