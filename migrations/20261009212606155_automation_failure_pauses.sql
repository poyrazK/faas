-- ADR-830: opt-in runtime failure pauses.
-- +goose Up
CREATE TABLE IF NOT EXISTS workflow_automation_failure_policies (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 128),
 version bigint NOT NULL CHECK(version>0),
 enabled boolean NOT NULL DEFAULT false,
 failure_threshold integer NOT NULL CHECK(failure_threshold BETWEEN 1 AND 10000),
 min_completed_runs integer NOT NULL CHECK(min_completed_runs BETWEEN 1 AND 10000),
 window_seconds integer NOT NULL CHECK(window_seconds BETWEEN 60 AND 86400),
 PRIMARY KEY(app_id,name)
);
CREATE TABLE IF NOT EXISTS workflow_automation_failure_guards (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 128),
 generation bigint NOT NULL DEFAULT 0 CHECK(generation>=0),
 monitoring_since timestamptz NOT NULL DEFAULT clock_timestamp(),
 paused_at timestamptz,
 PRIMARY KEY(app_id,name)
);
CREATE TABLE IF NOT EXISTS workflow_automation_failure_history (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 128),
 generation bigint NOT NULL CHECK(generation>0),
 state text NOT NULL CHECK(state IN ('paused','resumed')),
 reason text NOT NULL CHECK(reason IN ('failure_threshold','operator_resume')),
 recorded_at timestamptz NOT NULL,
 failures bigint NOT NULL CHECK(failures>=0),
 completed_runs bigint NOT NULL CHECK(completed_runs>=failures),
 policy_version bigint NOT NULL CHECK(policy_version>0),
 actor_account_id uuid REFERENCES accounts(id),
 UNIQUE(app_id,name,generation)
);
CREATE INDEX IF NOT EXISTS workflow_automation_failure_finished_idx ON workflow_runs(app_id,workflow_name,finished_at)
 WHERE status IN ('succeeded','failed','dead') AND cancelled_at IS NULL AND operation_id IS NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app_workflow_definitions(target_app uuid, manifest jsonb) RETURNS jsonb
    LANGUAGE sql STABLE
    AS $$
 SELECT coalesce(jsonb_agg(CASE WHEN definition->'trigger'->>'type' IN ('schedule','event') AND EXISTS(SELECT 1 FROM workflow_automation_failure_guards g WHERE g.app_id=target_app AND g.name=definition->>'name' AND g.paused_at IS NOT NULL) THEN jsonb_set(definition,'{trigger,enabled}','false'::jsonb,true) ELSE definition END ORDER BY definition->>'name'),'[]'::jsonb)
 FROM (
  SELECT definition FROM jsonb_array_elements(CASE WHEN jsonb_typeof(manifest)='array' THEN manifest ELSE '[]'::jsonb END) definition
  WHERE NOT EXISTS(SELECT 1 FROM workflow_automation_definitions w WHERE w.app_id=target_app
    AND w.name=definition->>'name' AND w.published IS NOT NULL)
  UNION ALL
  SELECT CASE WHEN published->'trigger'->>'type' IN ('schedule','event')
   THEN jsonb_set(published, '{trigger,enabled}', to_jsonb(enabled), true) ELSE published END
  FROM workflow_automation_definitions WHERE app_id=target_app AND published IS NOT NULL
 ) effective;
$$;
-- +goose StatementEnd
ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((event = ANY (ARRAY['usage_statement.finalized'::text, 'app.parked'::text, 'app.woken'::text, 'issue.created'::text, 'issue.assigned'::text, 'issue.resolved'::text, 'issue.reopened'::text, 'issue.ignored'::text, 'issue.regressed'::text, 'issue.impact_threshold_reached'::text, 'routes.requirements.violated'::text, 'routes.requirements.recovered'::text, 'routes.requirements.changed'::text, 'routes.health.blocked'::text, 'routes.health.resumed'::text, 'routes.health.aborted'::text, 'routes.monitor.violated'::text, 'routes.monitor.recovered'::text, 'workflow.finished'::text, 'automation.paused'::text, 'app.health.changed'::text, 'routes.monitor.escalated'::text, 'event_recovery.completed'::text, 'event_recovery.cancelled'::text, 'event_recovery.expired'::text, 'profile.route_regressed'::text, 'profile.route_recovered'::text])));

ALTER TABLE workflow_webhook_receipts DROP CONSTRAINT IF EXISTS workflow_webhook_receipts_ignored_reason_check;
ALTER TABLE workflow_webhook_receipts ADD CONSTRAINT workflow_webhook_receipts_ignored_reason_check CHECK ((ignored_reason = ANY (ARRAY['automation_paused'::text, 'automation_failure_paused'::text, 'event_filtered'::text, 'automation_unpublished'::text])));

-- +goose Down
-- Refuse to silently resume paused work during a downgrade.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_automation_failure_guards WHERE paused_at IS NOT NULL) THEN RAISE EXCEPTION 'resume failure-paused automations before rollback'; END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app_workflow_definitions(target_app uuid, manifest jsonb) RETURNS jsonb
    LANGUAGE sql STABLE
    AS $$
 SELECT coalesce(jsonb_agg(definition ORDER BY definition->>'name'),'[]'::jsonb)
 FROM (
  SELECT definition FROM jsonb_array_elements(CASE WHEN jsonb_typeof(manifest)='array' THEN manifest ELSE '[]'::jsonb END) definition
  WHERE NOT EXISTS(SELECT 1 FROM workflow_automation_definitions w WHERE w.app_id=target_app
    AND w.name=definition->>'name' AND w.published IS NOT NULL)
  UNION ALL
  SELECT CASE WHEN published->'trigger'->>'type' IN ('schedule','event')
   THEN jsonb_set(published, '{trigger,enabled}', to_jsonb(enabled), true) ELSE published END
  FROM workflow_automation_definitions WHERE app_id=target_app AND published IS NOT NULL
 ) effective;
$$;
-- +goose StatementEnd
DROP INDEX IF EXISTS workflow_automation_failure_finished_idx;
DROP TABLE IF EXISTS workflow_automation_failure_history;
DROP TABLE IF EXISTS workflow_automation_failure_guards;
DROP TABLE IF EXISTS workflow_automation_failure_policies;
-- Preserve notification vocabulary for committed outbox events.
