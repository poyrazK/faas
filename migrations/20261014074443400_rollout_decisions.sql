-- migration-compat: expand
-- filename: 20261014074443400_rollout_decisions.sql
--
-- ADR-1447: explained rollout decisions.
--   * deployments.rollout_decision holds the evidence recorded with the
--     latest rollout transition. It is written only in the same UPDATE as an
--     advance or recovery, never on a hold. The structural ceiling is twice
--     api.RolloutDecisionMaxBytes because jsonb's text form adds spacing.
--   * deploy.rollout_held audit rows record why a canary step is held.
--   * rollout.completed / rollout.aborted webhook payloads carry the record
--     when it explains that outcome: an advance for completion, an abort for
--     an abort.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS rollout_decision jsonb;
ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_rollout_decision_chk;
ALTER TABLE deployments ADD CONSTRAINT deployments_rollout_decision_chk CHECK (
    rollout_decision IS NULL OR (
        jsonb_typeof(rollout_decision) = 'object'
        AND rollout_decision->>'kind' IN ('advance', 'abort')
        AND octet_length(rollout_decision::text) <= 4096
    )
) NOT VALID;
ALTER TABLE deployments VALIDATE CONSTRAINT deployments_rollout_decision_chk;

-- Extend whatever the constraint admits now: goose runs with allow-missing,
-- so this can apply after later migrations that added their own kinds.
DO $kinds$
DECLARE previous_check text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO previous_check FROM pg_constraint
  WHERE conrelid = 'deployment_audit'::regclass AND conname = 'deployment_audit_kind_chk';
 IF previous_check IS NOT NULL AND position('deploy.rollout_held' IN previous_check) = 0 THEN
  ALTER TABLE deployment_audit DROP CONSTRAINT deployment_audit_kind_chk;
  EXECUTE format('ALTER TABLE deployment_audit ADD CONSTRAINT deployment_audit_kind_chk CHECK ((%s) OR kind = ''deploy.rollout_held'')',
   regexp_replace(previous_check, '^CHECK \((.*)\)$', '\1'));
 END IF;
END $kinds$;

CREATE OR REPLACE FUNCTION enqueue_rollout_outcome_webhooks()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    event_name text;
    event_payload jsonb;
BEGIN
    IF NEW.rollout_state IS NOT DISTINCT FROM OLD.rollout_state
       OR OLD.rollout_state NOT IN ('pending', 'rolling_out') THEN
        RETURN NEW;
    END IF;

    IF NEW.rollout_state = 'complete' AND NEW.status = 'live' THEN
        event_name := 'rollout.completed';
        event_payload := jsonb_build_object(
            'app_id', NEW.app_id,
            'deployment_id', NEW.id,
            'rollout_state', 'complete',
            'traffic_percent', NEW.traffic_percent,
            'completed_at', COALESCE(NEW.rollout_completed_at, now())
        ) || jsonb_strip_nulls(jsonb_build_object('decision',
            CASE WHEN NEW.rollout_decision->>'kind' = 'advance' THEN NEW.rollout_decision END));
    ELSIF NEW.rollout_state = 'aborted' AND OLD.status = 'live'
          AND NEW.status IN ('live', 'superseded') THEN
        event_name := 'rollout.aborted';
        event_payload := jsonb_build_object(
            'app_id', NEW.app_id,
            'deployment_id', NEW.id,
            'rollout_state', 'aborted',
            'traffic_percent', NEW.traffic_percent,
            'reason', COALESCE(NULLIF(NEW.rollout_aborted_reason, ''), 'rollout aborted'),
            'aborted_at', COALESCE(NEW.rollout_aborted_at, now())
        ) || jsonb_strip_nulls(jsonb_build_object('decision',
            CASE WHEN NEW.rollout_decision->>'kind' = 'abort' THEN NEW.rollout_decision END));
    ELSE
        RETURN NEW;
    END IF;

    INSERT INTO app_webhook_deliveries
        (webhook_id, app_id, account_id, event, payload)
    SELECT h.id, NEW.app_id, h.account_id, event_name, event_payload
      FROM app_webhooks h
      JOIN apps a ON a.id = NEW.app_id AND a.account_id = h.account_id
     WHERE h.enabled
       AND ((h.scope = 'app' AND h.app_id = NEW.app_id)
            OR (h.scope = 'account' AND h.app_id IS NULL))
       AND (event_name = ANY(h.event_filter)
            OR (h.scope = 'app' AND cardinality(h.event_filter) = 0));
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM deployment_audit WHERE kind = 'deploy.rollout_held') THEN
        RAISE EXCEPTION 'retain rollout hold audit events before rollback';
    END IF;
END $$;

CREATE OR REPLACE FUNCTION enqueue_rollout_outcome_webhooks()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    event_name text;
    event_payload jsonb;
BEGIN
    IF NEW.rollout_state IS NOT DISTINCT FROM OLD.rollout_state
       OR OLD.rollout_state NOT IN ('pending', 'rolling_out') THEN
        RETURN NEW;
    END IF;

    IF NEW.rollout_state = 'complete' AND NEW.status = 'live' THEN
        event_name := 'rollout.completed';
        event_payload := jsonb_build_object(
            'app_id', NEW.app_id,
            'deployment_id', NEW.id,
            'rollout_state', 'complete',
            'traffic_percent', NEW.traffic_percent,
            'completed_at', COALESCE(NEW.rollout_completed_at, now())
        );
    ELSIF NEW.rollout_state = 'aborted' AND OLD.status = 'live'
          AND NEW.status IN ('live', 'superseded') THEN
        event_name := 'rollout.aborted';
        event_payload := jsonb_build_object(
            'app_id', NEW.app_id,
            'deployment_id', NEW.id,
            'rollout_state', 'aborted',
            'traffic_percent', NEW.traffic_percent,
            'reason', COALESCE(NULLIF(NEW.rollout_aborted_reason, ''), 'rollout aborted'),
            'aborted_at', COALESCE(NEW.rollout_aborted_at, now())
        );
    ELSE
        RETURN NEW;
    END IF;

    INSERT INTO app_webhook_deliveries
        (webhook_id, app_id, account_id, event, payload)
    SELECT h.id, NEW.app_id, h.account_id, event_name, event_payload
      FROM app_webhooks h
      JOIN apps a ON a.id = NEW.app_id AND a.account_id = h.account_id
     WHERE h.enabled
       AND ((h.scope = 'app' AND h.app_id = NEW.app_id)
            OR (h.scope = 'account' AND h.app_id IS NULL))
       AND (event_name = ANY(h.event_filter)
            OR (h.scope = 'app' AND cardinality(h.event_filter) = 0));
    RETURN NEW;
END;
$$;

DELETE FROM deployment_audit WHERE kind = 'deploy.rollout_held';
DO $kinds$
DECLARE previous_check text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO previous_check FROM pg_constraint
  WHERE conrelid = 'deployment_audit'::regclass AND conname = 'deployment_audit_kind_chk';
 IF previous_check IS NOT NULL AND position('deploy.rollout_held' IN previous_check) > 0 THEN
  ALTER TABLE deployment_audit DROP CONSTRAINT deployment_audit_kind_chk;
  EXECUTE 'ALTER TABLE deployment_audit ADD CONSTRAINT deployment_audit_kind_chk ' ||
   regexp_replace(previous_check, ' OR \(kind = ''deploy\.rollout_held''::text\)', '');
 END IF;
END $kinds$;

ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_rollout_decision_chk;
ALTER TABLE deployments DROP COLUMN IF EXISTS rollout_decision;
-- +goose StatementEnd
