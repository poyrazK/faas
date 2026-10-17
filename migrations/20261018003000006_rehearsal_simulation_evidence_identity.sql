-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A missing ledger row may leave this migration's complete schema behind.
-- Preserve later fences only when every owned object and a known function
-- definition are present; refuse partial or unrecognized schema.
-- +goose StatementBegin
DO $rehearsal_replay$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('scenario_simulation_attempts') AND conname='scenario_simulation_evidence_identity')
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_simulation_sealed_fence()') AND md5(prosrc)=ANY(ARRAY['de16aee16810ee904caa48a18d1b9d57'])) THEN
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('scenario_simulation_attempts') AND conname='scenario_simulation_evidence_identity')
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_simulation_sealed_fence()') AND md5(prosrc)=ANY(ARRAY['de16aee16810ee904caa48a18d1b9d57'])) THEN
    RAISE EXCEPTION 'partial or unrecognized schema for rehearsal migration 6; inspect recovery before continuing';
  END IF;
  IF to_regprocedure('scenario_simulation_sealed_fence()') IS NOT NULL AND NOT (EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_simulation_sealed_fence()') AND md5(prosrc)=ANY(ARRAY['1d578c2dfbdcca616108a4620aa6a17e','de16aee16810ee904caa48a18d1b9d57']))) THEN
    RAISE EXCEPTION 'unexpected predecessor fence scenario_simulation_sealed_fence';
  END IF;

ALTER TABLE scenario_simulation_attempts ADD CONSTRAINT scenario_simulation_evidence_identity
 CHECK (coalesce(evidence->>'id'=id::text,false) AND
        coalesce(evidence->>'status' IN ('pending','completed','interrupted','unsupported'),false));

CREATE OR REPLACE FUNCTION scenario_simulation_sealed_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF (NEW.id,NEW.app_id,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.created_at)
     OR (NEW.evidence-'status') IS DISTINCT FROM (OLD.evidence-'status')
     OR OLD.evidence->>'status'<>'pending'
     OR NEW.evidence->>'status' NOT IN ('completed','interrupted') THEN
   RAISE EXCEPTION 'scenario simulation evidence is immutable' USING ERRCODE='23514';
  END IF;
 END IF;
 PERFORM id FROM apps WHERE id=NEW.app_id FOR KEY SHARE;
 IF NOT EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE app_id=NEW.app_id AND simulation_plan IS NOT NULL AND sealed_at IS NULL) THEN
  RAISE EXCEPTION 'scenario simulation window is unavailable or sealed' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
END
$rehearsal_replay$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE simulation_plan IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot relax simulation evidence identity while enrolled apps remain';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE scenario_simulation_attempts DROP CONSTRAINT scenario_simulation_evidence_identity;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_simulation_sealed_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM id FROM apps WHERE id=NEW.app_id FOR KEY SHARE;
 IF NOT EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE app_id=NEW.app_id AND simulation_plan IS NOT NULL AND sealed_at IS NULL) THEN
  RAISE EXCEPTION 'scenario simulation window is unavailable or sealed' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
