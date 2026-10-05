-- +goose Up
CREATE SEQUENCE IF NOT EXISTS automation_definition_versions AS bigint;
CREATE TABLE IF NOT EXISTS workflow_automation_definitions (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (length(name)>0),
 version bigint NOT NULL CHECK(version>0),
 draft jsonb NOT NULL CHECK(jsonb_typeof(draft)='object' AND draft->>'name'=name),
 published jsonb CHECK(published IS NULL OR (jsonb_typeof(published)='object' AND published->>'name'=name)),
 published_version bigint NOT NULL DEFAULT 0 CHECK(published_version>=0 AND published_version<=version),
 enabled boolean NOT NULL DEFAULT true,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(app_id,name),
 CHECK ((published IS NULL) = (published_version=0))
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app_workflow_definitions(target_app uuid, manifest jsonb)
RETURNS jsonb LANGUAGE sql STABLE AS $$
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
-- The next function replacement preserves acceptance-time event snapshots.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION workflow_event_recipients(target_account uuid, event_source text, event_type text)
RETURNS TABLE(recipient jsonb) LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object(
   'id', md5('gregale.workflow.event:' || a.id::text || ':' || (definition->>'name'))::uuid,
   'account_id', a.account_id, 'app_id', a.id, 'deployment_id', d.id,
   'source', definition->'trigger'->>'source', 'type', definition->'trigger'->>'event_type',
   'filter', coalesce(definition->'trigger'->'filter', '{}'::jsonb), 'workflow', definition)
 FROM apps a JOIN accounts ac ON ac.id = a.account_id
 JOIN LATERAL (
   SELECT dep.id, dep.workflows FROM deployments dep
   WHERE dep.app_id = a.id AND dep.status = 'live' AND dep.scope = 'default'
   ORDER BY (dep.traffic_percent > 0) DESC, dep.created_at DESC, dep.id DESC LIMIT 1
 ) d ON true
 CROSS JOIN LATERAL jsonb_array_elements(app_workflow_definitions(a.id,d.workflows)) definition
 WHERE a.account_id = target_account AND a.status <> 'deleted'
   AND NOT a.maintenance_mode AND NOT a.platform_tenant_required
   AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL AND ac.plan <> 'free'
   AND definition->'trigger'->>'type' = 'event'
   AND coalesce(definition->'trigger'->>'enabled', 'true') = 'true'
   AND event_fanout_pattern_matches(definition->'trigger'->>'source', event_source)
   AND event_fanout_pattern_matches(definition->'trigger'->>'event_type', event_type);
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION workflow_event_recipients(target_account uuid, event_source text, event_type text)
RETURNS TABLE(recipient jsonb) LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object(
   'id', md5('gregale.workflow.event:' || a.id::text || ':' || (definition->>'name'))::uuid,
   'account_id', a.account_id, 'app_id', a.id, 'deployment_id', d.id,
   'source', definition->'trigger'->>'source', 'type', definition->'trigger'->>'event_type',
   'filter', coalesce(definition->'trigger'->'filter', '{}'::jsonb), 'workflow', definition)
 FROM apps a JOIN accounts ac ON ac.id = a.account_id
 JOIN LATERAL (
   SELECT dep.id, dep.workflows FROM deployments dep
   WHERE dep.app_id = a.id AND dep.status = 'live' AND dep.scope = 'default'
   ORDER BY (dep.traffic_percent > 0) DESC, dep.created_at DESC, dep.id DESC LIMIT 1
 ) d ON true
 CROSS JOIN LATERAL jsonb_array_elements(d.workflows) definition
 WHERE a.account_id = target_account AND a.status <> 'deleted'
   AND NOT a.maintenance_mode AND NOT a.platform_tenant_required
   AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL AND ac.plan <> 'free'
   AND definition->'trigger'->>'type' = 'event'
   AND coalesce(definition->'trigger'->>'enabled', 'true') = 'true'
   AND event_fanout_pattern_matches(definition->'trigger'->>'source', event_source)
   AND event_fanout_pattern_matches(definition->'trigger'->>'event_type', event_type);
$$;
-- +goose StatementEnd
DROP FUNCTION app_workflow_definitions(uuid,jsonb);
DROP TABLE workflow_automation_definitions;
DROP SEQUENCE automation_definition_versions;
