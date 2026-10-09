-- +goose Up
CREATE TABLE route_lifecycle_approvals (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 baseline_deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 candidate_deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 receipt jsonb NOT NULL,
 approved_at timestamptz NOT NULL,
 valid_until timestamptz NOT NULL CHECK (valid_until > approved_at),
 invalidated_at timestamptz,
 CHECK (baseline_deployment_id <> candidate_deployment_id)
);
CREATE INDEX route_lifecycle_approvals_candidate ON route_lifecycle_approvals(app_id,baseline_deployment_id,candidate_deployment_id,approved_at DESC) WHERE invalidated_at IS NULL;
-- A replacement capture permanently invalidates receipts, even if old bytes
-- are later restored. Retain the original receipt for inspection and audit.
-- +goose StatementBegin
CREATE FUNCTION invalidate_route_lifecycle_approvals() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE route_lifecycle_approvals SET invalidated_at=clock_timestamp()
 WHERE app_id=OLD.app_id AND invalidated_at IS NULL
 AND (baseline_deployment_id=OLD.deployment_id OR candidate_deployment_id=OLD.deployment_id);
 RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER invalidate_route_lifecycle_approvals AFTER UPDATE OR DELETE ON deployment_openapi_docs
FOR EACH ROW EXECUTE FUNCTION invalidate_route_lifecycle_approvals();
-- +goose Down
DROP TRIGGER IF EXISTS invalidate_route_lifecycle_approvals ON deployment_openapi_docs;
DROP FUNCTION IF EXISTS invalidate_route_lifecycle_approvals();
DROP TABLE IF EXISTS route_lifecycle_approvals;
