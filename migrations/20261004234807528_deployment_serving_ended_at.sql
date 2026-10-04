-- filename: 20261004234807528_deployment_serving_ended_at.sql

-- Record when a deployment stopped serving traffic, so a default rollback
-- (and the automatic rollback) returns to the deployment that served most
-- recently. Those targets were ordered by created_at. After an explicit
-- rollback to v1 superseded the newer v4, a canary from v1 to v5 completed,
-- and a plain rollback chose v4 (created after v1) instead of v1, which had
-- just been replaced (production-us, 2026-10-04). Rows superseded before
-- this migration keep a NULL stamp and fall back to created_at.

-- +goose Up
ALTER TABLE deployments ADD COLUMN serving_ended_at timestamptz;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION stamp_deployment_serving_ended_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status = 'live' AND NEW.traffic_percent > 0 THEN
  NEW.serving_ended_at := NULL;
 ELSIF OLD.status = 'live' AND OLD.traffic_percent > 0 THEN
  NEW.serving_ended_at := now();
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER deployment_serving_ended_at BEFORE UPDATE OF status, traffic_percent ON deployments
 FOR EACH ROW EXECUTE FUNCTION stamp_deployment_serving_ended_at();

-- +goose Down
DROP TRIGGER deployment_serving_ended_at ON deployments;
DROP FUNCTION stamp_deployment_serving_ended_at();
ALTER TABLE deployments DROP COLUMN serving_ended_at;
