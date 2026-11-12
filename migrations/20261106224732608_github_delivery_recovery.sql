-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '500ms';
CREATE TABLE IF NOT EXISTS github_transport_recoveries (
 delivery_guid text PRIMARY KEY,
 attempts integer NOT NULL CHECK(attempts BETWEEN 1 AND 3),
 next_attempt_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS github_delivery_targets (
 delivery_id text NOT NULL REFERENCES github_webhook_deliveries(delivery_id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 status text NOT NULL CHECK(status IN ('succeeded','pending','dead')),
 skip_reason text NOT NULL DEFAULT '',
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(delivery_id,account_id)
);
CREATE TABLE IF NOT EXISTS github_connection_verifications (
 account_id uuid NOT NULL,
 installation_id bigint NOT NULL,
 verified_at timestamptz NOT NULL,
 access jsonb NOT NULL,
 PRIMARY KEY(account_id,installation_id),
 FOREIGN KEY(account_id,installation_id) REFERENCES github_installations(account_id,installation_id) ON DELETE CASCADE
);
-- +goose Down
DROP TABLE github_connection_verifications;
DROP TABLE github_delivery_targets;
DROP TABLE github_transport_recoveries;
