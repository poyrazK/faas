-- adr: 531
-- +goose Up
CREATE TABLE IF NOT EXISTS project_environment_promotion_feature_flags (
    promotion_id uuid PRIMARY KEY REFERENCES project_environment_promotions(id) ON DELETE CASCADE,
    source_snapshot jsonb NOT NULL CHECK (jsonb_typeof(source_snapshot) = 'object'),
    previous_target_snapshot jsonb NOT NULL CHECK (jsonb_typeof(previous_target_snapshot) = 'object'),
    source_hash text NOT NULL CHECK (source_hash ~ '^[a-f0-9]{64}$'),
    previous_target_hash text NOT NULL CHECK (previous_target_hash ~ '^[a-f0-9]{64}$'),
    target_version bigint NOT NULL DEFAULT 0 CHECK (target_version BETWEEN 0 AND 9007199254740991),
    rollback_version bigint NOT NULL DEFAULT 0 CHECK (rollback_version BETWEEN 0 AND 9007199254740991),
    CHECK (rollback_version = 0 OR (target_version > 0 AND rollback_version > target_version))
);

-- Retain the original environment UUIDs in the snapshots. Deleting and
-- recreating an environment must not attach this operation to its new lifetime.

-- +goose Down
DROP TABLE project_environment_promotion_feature_flags;
