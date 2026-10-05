-- filename: 20261003162226970_route_monitor_customers.sql

-- +goose Up
ALTER TABLE route_monitors ADD COLUMN IF NOT EXISTS customer_group_by text NOT NULL DEFAULT '' CHECK (customer_group_by IN ('','tenant','consumer'));
ALTER TABLE route_monitors ADD COLUMN IF NOT EXISTS customer_recovery_state jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(customer_recovery_state)='object' AND octet_length(customer_recovery_state::text)<=262144);

-- +goose Down
ALTER TABLE route_monitors DROP COLUMN customer_group_by;
ALTER TABLE route_monitors DROP COLUMN customer_recovery_state;
