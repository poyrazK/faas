-- filename: 20261008114924433_runtime_upgrade_baselines.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS deployment_runtime_upgrade_baselines (
 deployment_id uuid PRIMARY KEY REFERENCES deployment_runtime_upgrade_targets(deployment_id) ON DELETE CASCADE,
 serving_deployment_id uuid NOT NULL REFERENCES deployments(id) DEFERRABLE INITIALLY DEFERRED,
 serving_rootfs_key text NOT NULL CHECK (octet_length(serving_rootfs_key) BETWEEN 1 AND 1024),
 serving_runtime_release_id text NOT NULL REFERENCES runtime_releases(id),
 target_release_id text NOT NULL REFERENCES runtime_releases(id),
 configuration_fingerprint text NOT NULL CHECK (configuration_fingerprint ~ '^[a-f0-9]{64}$'),
 secret_fingerprint text NOT NULL CHECK (secret_fingerprint ~ '^[a-f0-9]{64}$'),
 input_fingerprint text NOT NULL CHECK (input_fingerprint ~ '^[a-f0-9]{64}$'),
 input_secret_fingerprint text NOT NULL CHECK (input_secret_fingerprint ~ '^[a-f0-9]{64}$'),
 captured_at timestamptz NOT NULL DEFAULT now(),
 CHECK (deployment_id <> serving_deployment_id)
);
DROP TRIGGER IF EXISTS runtime_upgrade_baseline_immutable ON deployment_runtime_upgrade_baselines;
CREATE TRIGGER runtime_upgrade_baseline_immutable BEFORE UPDATE OR DELETE ON deployment_runtime_upgrade_baselines
FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_target();

-- +goose Down
-- Forward-only: retries retain their reviewed serving and configuration baseline.
SELECT 1;
