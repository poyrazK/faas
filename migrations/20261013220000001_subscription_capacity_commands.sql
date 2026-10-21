-- ADR-1178: command identity and original capacity outcome are committed together.
-- +goose Up
CREATE TABLE IF NOT EXISTS event_subscription_capacity_commands (
 subscription_id uuid NOT NULL REFERENCES event_subscription_delivery_controls(subscription_id) ON DELETE CASCADE,
 command_id uuid NOT NULL CHECK(command_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object'),
 recorded_at timestamptz NOT NULL,
 retain_until timestamptz NOT NULL CHECK(retain_until>recorded_at),
 PRIMARY KEY(subscription_id,command_id)
);
CREATE INDEX IF NOT EXISTS event_subscription_capacity_commands_expiry ON event_subscription_capacity_commands(retain_until,subscription_id,command_id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM event_subscription_capacity_commands) THEN
  RAISE EXCEPTION 'prune retained subscription capacity commands before downgrade';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE event_subscription_capacity_commands;
