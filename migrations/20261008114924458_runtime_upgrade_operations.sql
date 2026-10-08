-- filename: 20261008114924458_runtime_upgrade_operations.sql

-- +goose Up
CREATE TABLE runtime_upgrade_operations (
 id uuid PRIMARY KEY CHECK (id<>'00000000-0000-0000-0000-000000000000'::uuid),
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL UNIQUE REFERENCES deployments(id) ON DELETE CASCADE,
 serving_deployment_id uuid NOT NULL REFERENCES deployments(id) DEFERRABLE INITIALLY DEFERRED,
 target_release_id text NOT NULL REFERENCES runtime_releases(id),
 source_sha256 text NOT NULL CHECK (source_sha256 ~ '^[a-f0-9]{64}$'),
 qualification_report_sha256 text NOT NULL CHECK (qualification_report_sha256 ~ '^[a-f0-9]{64}$'),
 phase text NOT NULL DEFAULT 'prepared' CHECK (phase IN ('prepared','waiting','complete','blocked')),
 blocker text NOT NULL DEFAULT '' CHECK (blocker IN ('','deadline_exceeded','candidate_changed','baseline_changed','qualification_changed','readiness_changed','intent_changed')),
 wake_id uuid CHECK (wake_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
 deadline_at timestamptz NOT NULL CHECK (isfinite(deadline_at) AND deadline_at>created_at),
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(next_attempt_at)),
 lease_token uuid CHECK (lease_token<>'00000000-0000-0000-0000-000000000000'::uuid),
 lease_until timestamptz CHECK (isfinite(lease_until)),
 finished_at timestamptz CHECK (isfinite(finished_at)),
 CHECK (deployment_id<>serving_deployment_id),
 CHECK ((lease_token IS NULL)=(lease_until IS NULL)),
 CHECK ((phase IN ('prepared','waiting') AND blocker='' AND wake_id IS NULL AND finished_at IS NULL)
  OR (phase='complete' AND blocker='' AND wake_id IS NOT NULL AND finished_at IS NOT NULL AND lease_token IS NULL)
  OR (phase='blocked' AND blocker<>'' AND wake_id IS NULL AND finished_at IS NOT NULL AND lease_token IS NULL))
);
CREATE UNIQUE INDEX runtime_upgrade_operations_active_app ON runtime_upgrade_operations(app_id) WHERE phase IN ('prepared','waiting');
CREATE INDEX runtime_upgrade_operations_due ON runtime_upgrade_operations(next_attempt_at,created_at,id) WHERE phase IN ('prepared','waiting');

-- +goose StatementBegin
CREATE FUNCTION guard_runtime_upgrade_operation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
 IF (NEW.id,NEW.account_id,NEW.app_id,NEW.deployment_id,NEW.serving_deployment_id,NEW.target_release_id,
     NEW.source_sha256,NEW.qualification_report_sha256,NEW.created_at,NEW.deadline_at)
  IS DISTINCT FROM
    (OLD.id,OLD.account_id,OLD.app_id,OLD.deployment_id,OLD.serving_deployment_id,OLD.target_release_id,
     OLD.source_sha256,OLD.qualification_report_sha256,OLD.created_at,OLD.deadline_at)
  OR OLD.phase IN ('complete','blocked')
  OR (OLD.phase='waiting' AND NEW.phase='prepared') THEN
  RAISE EXCEPTION 'immutable runtime upgrade intent or terminal operation' USING ERRCODE='23514';
 END IF;
 END IF;
 IF NEW.phase='complete' AND NOT EXISTS (
  SELECT 1 FROM deployment_runtime_upgrade_cutovers c WHERE c.deployment_id=NEW.deployment_id
   AND c.serving_deployment_id=NEW.serving_deployment_id AND c.target_release_id=NEW.target_release_id
   AND c.qualification_report_sha256=NEW.qualification_report_sha256 AND c.wake_id=NEW.wake_id
 ) THEN
  RAISE EXCEPTION 'runtime upgrade completion requires retained cutover' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER runtime_upgrade_operation_guard BEFORE INSERT OR UPDATE ON runtime_upgrade_operations FOR EACH ROW EXECUTE FUNCTION guard_runtime_upgrade_operation();

-- +goose Down
-- Forward-only: do not erase durable operation history on binary rollback.
SELECT 1;
