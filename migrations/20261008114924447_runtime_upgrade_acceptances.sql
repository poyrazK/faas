-- filename: 20261008114924447_runtime_upgrade_acceptances.sql

-- +goose Up
CREATE TABLE deployment_runtime_upgrade_acceptances (
 deployment_id uuid PRIMARY KEY REFERENCES deployment_runtime_upgrade_baselines(deployment_id) ON DELETE CASCADE,
 target_release_id text NOT NULL REFERENCES runtime_releases(id),
 rootfs_key text NOT NULL CHECK (octet_length(rootfs_key) BETWEEN 1 AND 1024),
 instance_id uuid NOT NULL CHECK (instance_id <> '00000000-0000-0000-0000-000000000000'),
 node_id uuid NOT NULL CHECK (node_id <> '00000000-0000-0000-0000-000000000000'),
 wake_id uuid NOT NULL CHECK (wake_id <> '00000000-0000-0000-0000-000000000000'),
 profile text NOT NULL CHECK (profile = 'runtime-upgrade-prime-v1'),
 configuration_fingerprint text NOT NULL CHECK (configuration_fingerprint ~ '^[a-f0-9]{64}$'),
 secret_fingerprint text NOT NULL CHECK (secret_fingerprint ~ '^[a-f0-9]{64}$'),
 qualification_report_sha256 text NOT NULL CHECK (qualification_report_sha256 ~ '^[a-f0-9]{64}$'),
 started_at timestamptz NOT NULL CHECK (isfinite(started_at)),
 ready_at timestamptz NOT NULL CHECK (isfinite(ready_at) AND ready_at >= started_at)
);
CREATE TRIGGER runtime_upgrade_acceptance_immutable BEFORE UPDATE OR DELETE ON deployment_runtime_upgrade_acceptances
FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_target();

-- +goose Down
-- Forward-only: retain the original candidate cold-boot/readiness evidence.
SELECT 1;
