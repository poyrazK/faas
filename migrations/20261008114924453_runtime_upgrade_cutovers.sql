-- filename: 20261008114924453_runtime_upgrade_cutovers.sql

-- +goose Up
CREATE TABLE deployment_runtime_upgrade_cutovers (
 deployment_id uuid PRIMARY KEY REFERENCES deployment_runtime_upgrade_acceptances(deployment_id) ON DELETE CASCADE,
 serving_deployment_id uuid NOT NULL REFERENCES deployments(id) DEFERRABLE INITIALLY DEFERRED,
 target_release_id text NOT NULL REFERENCES runtime_releases(id),
 wake_id uuid NOT NULL CHECK (wake_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 qualification_report_sha256 text NOT NULL CHECK (qualification_report_sha256 ~ '^[a-f0-9]{64}$'),
 cutover_at timestamptz NOT NULL CHECK (isfinite(cutover_at)),
 CHECK (deployment_id<>serving_deployment_id)
);
CREATE TRIGGER runtime_upgrade_cutover_immutable BEFORE UPDATE OR DELETE ON deployment_runtime_upgrade_cutovers
FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_target();

-- Every deployment writer, including legacy rollout/fallback SQL, is fenced.
-- This ledger is written only by the private atomic apid state seam, before
-- weights change. It is historical authorization, not continuing health proof.
-- +goose StatementBegin
CREATE FUNCTION enforce_runtime_upgrade_traffic_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='live' AND NEW.traffic_percent>0
  AND EXISTS(SELECT 1 FROM deployment_runtime_upgrade_targets WHERE deployment_id=NEW.id)
  AND NOT EXISTS (
   SELECT 1 FROM deployment_runtime_upgrade_cutovers c
   JOIN deployment_runtime_upgrade_acceptances a ON a.deployment_id=c.deployment_id
   JOIN deployment_runtime_upgrade_targets t ON t.deployment_id=c.deployment_id
   WHERE c.deployment_id=NEW.id AND c.target_release_id=t.release_id AND c.target_release_id=a.target_release_id
    AND c.wake_id=a.wake_id AND c.qualification_report_sha256=a.qualification_report_sha256 AND a.rootfs_key=NEW.rootfs_key
  ) THEN
  RAISE EXCEPTION 'runtime upgrade requires atomic qualified cutover' USING ERRCODE='23514',CONSTRAINT='runtime_upgrade_traffic_fenced';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER deployments_runtime_upgrade_traffic_fence BEFORE INSERT OR UPDATE ON deployments
FOR EACH ROW EXECUTE FUNCTION enforce_runtime_upgrade_traffic_fence();


-- A pin acquired after traffic changed cannot evade the deployment trigger.
-- +goose StatementBegin
CREATE FUNCTION enforce_runtime_upgrade_pin_traffic_fence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE candidate deployments;
BEGIN
 SELECT * INTO candidate FROM deployments WHERE id=NEW.deployment_id FOR SHARE;
 IF candidate.status='live' AND candidate.traffic_percent>0 THEN
  RAISE EXCEPTION 'serving deployment cannot acquire runtime upgrade preparation'
   USING ERRCODE='23514',CONSTRAINT='runtime_upgrade_traffic_fenced';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER runtime_upgrade_pin_traffic_fence BEFORE INSERT ON deployment_runtime_upgrade_targets
FOR EACH ROW EXECUTE FUNCTION enforce_runtime_upgrade_pin_traffic_fence();

-- Refuse installation over an already serving preparation. Do not repair
-- weights or invent acceptance for an older, bypassed runtime upgrade.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM deployments d JOIN deployment_runtime_upgrade_targets t ON t.deployment_id=d.id
  WHERE d.status='live' AND d.traffic_percent>0) THEN
  RAISE EXCEPTION 'serving runtime upgrade preparation requires operator reconciliation' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: activation evidence and writer fences must survive rollback.
SELECT 1;
