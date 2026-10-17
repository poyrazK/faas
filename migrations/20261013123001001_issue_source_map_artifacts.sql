-- +goose Up
CREATE TABLE IF NOT EXISTS issue_source_map_artifacts (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL,
 manifest_sha256 text NOT NULL CHECK(manifest_sha256 ~ '^[0-9a-f]{64}$'),
 canonical_manifest jsonb,
 map_count integer NOT NULL CHECK(map_count BETWEEN 1 AND 32),
 created_at timestamptz NOT NULL DEFAULT now(),
 created_by_account_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 retired_at timestamptz,
 retired_by_account_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 PRIMARY KEY(app_id,deployment_id),
 CONSTRAINT issue_source_map_artifacts_deployment_owner_fkey FOREIGN KEY(deployment_id,app_id) REFERENCES deployments(id,app_id) ON DELETE CASCADE,
 CHECK((canonical_manifest IS NULL) = (retired_at IS NOT NULL)),
 CHECK(canonical_manifest IS NULL OR octet_length(canonical_manifest::text) <= 4259840)
);

-- +goose Down
DROP TABLE IF EXISTS issue_source_map_artifacts;
