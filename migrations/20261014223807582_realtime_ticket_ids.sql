-- +goose Up
-- A ticket ID lets a backend revoke one subscription ticket without keeping
-- its bearer token, or revoke every ticket of one principal. Existing rows get
-- a random ID; a ticket lives at most five minutes anyway.
ALTER TABLE managed_realtime_subscription_tickets ADD COLUMN IF NOT EXISTS id uuid NOT NULL DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX IF NOT EXISTS managed_realtime_subscription_tickets_id ON managed_realtime_subscription_tickets (endpoint_id, id);
CREATE INDEX IF NOT EXISTS managed_realtime_subscription_tickets_principal ON managed_realtime_subscription_tickets (endpoint_id, principal);

-- +goose Down
DROP INDEX IF EXISTS managed_realtime_subscription_tickets_principal;
DROP INDEX IF EXISTS managed_realtime_subscription_tickets_id;
ALTER TABLE managed_realtime_subscription_tickets DROP COLUMN IF EXISTS id;
