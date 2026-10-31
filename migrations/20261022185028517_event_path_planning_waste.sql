-- migration-compat: expand
-- filename: 20261022185028517_event_path_planning_waste.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- The V12 profile (docs/ops/evidence/20261022-v12-provisional) spent 34.5 s of
-- 229 s planning SQL-language function bodies, which PostgreSQL 16 parses,
-- rewrites and plans on every call, and rewrote rows nothing had changed.
-- Every function below returns what it returned before.
--
-- The compute-budget scopes read calls this once per admission check, about
-- four times per invocation, almost always without a deployment: STRICT skips
-- the call for a NULL deployment, whose answer is NULL, and PL/pgSQL caches
-- the plan for the calls that remain.
CREATE OR REPLACE FUNCTION financial_compute_deployment_environment(target_deployment uuid)
RETURNS uuid LANGUAGE plpgsql STABLE STRICT AS $$
DECLARE
    environment uuid;
BEGIN
    SELECT COALESCE(owner.environment_id, named.id) INTO environment
    FROM deployments d JOIN apps a ON a.id=d.app_id
    LEFT JOIN deployment_runtime_environment_owners owner ON owner.deployment_id=d.id
    LEFT JOIN project_environments named ON owner.deployment_id IS NULL AND named.project_id=a.project_id
     AND named.slug=CASE WHEN COALESCE(NULLIF(d.scope,''),'default')='default' THEN 'production' ELSE d.scope END
    WHERE d.id=target_deployment;
    RETURN environment;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- stamp_invocation_async_subject calls this on every reserving invocation
-- insert and update.
CREATE OR REPLACE FUNCTION invocation_async_subject(app uuid, namespace uuid)
RETURNS uuid LANGUAGE plpgsql STABLE STRICT AS $$
DECLARE
    subject uuid;
BEGIN
    SELECT CASE WHEN o.id IS NOT NULL AND NOT o.personal_org AND o.resource_ownership_enabled
                AND o.async_quota_pooled_at IS NOT NULL THEN o.id ELSE a.account_id END INTO subject
    FROM apps a LEFT JOIN orgs o ON o.id=a.org_id WHERE a.id=app AND a.account_id=namespace;
    RETURN subject;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Every published event runs this once per app of its account. Only a
-- definition with an event trigger can match, so workflow_event_recipients
-- below now skips apps without any.
CREATE OR REPLACE FUNCTION app_workflow_definitions(target_app uuid, manifest jsonb)
RETURNS jsonb LANGUAGE plpgsql STABLE AS $$
BEGIN
    RETURN (
        SELECT coalesce(jsonb_agg(CASE WHEN definition->'trigger'->>'type' IN ('schedule','event') AND EXISTS(SELECT 1 FROM workflow_automation_failure_guards g WHERE g.app_id=target_app AND g.name=definition->>'name' AND g.paused_at IS NOT NULL) THEN jsonb_set(definition,'{trigger,enabled}','false'::jsonb,true) ELSE definition END ORDER BY definition->>'name'),'[]'::jsonb)
        FROM (
            SELECT definition FROM jsonb_array_elements(CASE WHEN jsonb_typeof(manifest)='array' THEN manifest ELSE '[]'::jsonb END) definition
            WHERE NOT EXISTS(SELECT 1 FROM workflow_automation_definitions w WHERE w.app_id=target_app
              AND w.name=definition->>'name' AND w.published IS NOT NULL)
            UNION ALL
            SELECT CASE WHEN published->'trigger'->>'type' IN ('schedule','event')
             THEN jsonb_set(published, '{trigger,enabled}', to_jsonb(enabled), true) ELSE published END
            FROM workflow_automation_definitions WHERE app_id=target_app AND published IS NOT NULL
        ) effective);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- app_workflow_definitions returns no definitions for an app whose live
-- deployment declares no workflows and that has no published automation, so
-- it is no longer called for one.
CREATE OR REPLACE FUNCTION workflow_event_recipients(target_account uuid, event_source text, event_type text)
RETURNS TABLE(recipient jsonb) LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object(
   'id', md5('gregale.workflow.event:' || a.id::text || ':' || (definition->>'name'))::uuid,
   'account_id', a.account_id, 'app_id', a.id, 'deployment_id', d.id,
   'source', definition->'trigger'->>'source', 'type', definition->'trigger'->>'event_type',
   'filter', coalesce(definition->'trigger'->'filter', '{}'::jsonb), 'workflow', definition)
 FROM apps a JOIN app_resource_accounts ac ON ac.app_id=a.id
 JOIN LATERAL (
   SELECT dep.id, dep.workflows FROM deployments dep
   WHERE dep.app_id = a.id AND dep.status = 'live' AND dep.scope = 'default'
   ORDER BY (dep.traffic_percent > 0) DESC, dep.created_at DESC, dep.id DESC LIMIT 1
 ) d ON true
 CROSS JOIN LATERAL jsonb_array_elements(CASE
   WHEN jsonb_typeof(d.workflows)='array' AND d.workflows <> '[]'::jsonb
     OR EXISTS(SELECT 1 FROM workflow_automation_definitions w WHERE w.app_id=a.id AND w.published IS NOT NULL)
   THEN app_workflow_definitions(a.id,d.workflows) ELSE '[]'::jsonb END) definition
 WHERE a.account_id = target_account AND a.status <> 'deleted'
   AND NOT a.maintenance_mode AND NOT a.platform_tenant_required
   AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL AND ac.plan <> 'free'
   AND definition->'trigger'->>'type' = 'event'
   AND coalesce(definition->'trigger'->>'enabled', 'true') = 'true'
   AND event_fanout_pattern_matches(definition->'trigger'->>'source', event_source)
   AND event_fanout_pattern_matches(definition->'trigger'->>'event_type', event_type);
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- An event with no ordered recipient keeps its snapshot as inserted. Writing
-- it back anyway updated the new outbox row a second time, which re-ran the
-- backlog projection and the workflow code-pin guard for every event.
CREATE OR REPLACE FUNCTION capture_event_recipient_ordering_snapshot() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    snapshot jsonb;
    lane record;
BEGIN
    IF NEW.recipient_snapshot IS NULL OR jsonb_array_length(NEW.recipient_snapshot) = 0 THEN
        RETURN NEW;
    END IF;

    FOR lane IN
        SELECT DISTINCT event_order_lane_lock_id(
            item.recipient->>'app_id', item.recipient->'work'->>'policy_name', NEW.payload,
            item.recipient->'work'->>'key_selector') AS lock_id
        FROM jsonb_array_elements(NEW.recipient_snapshot) item(recipient)
        WHERE jsonb_typeof(item.recipient->'work') = 'object'
          AND item.recipient->'work'->>'policy_name' <> ''
          AND event_order_lane_active(item.recipient->>'app_id',
              item.recipient->'work'->>'policy_name',
              coalesce((item.recipient->'work'->>'ordered')::boolean, false))
          AND event_order_lane_lock_id(
            item.recipient->>'app_id', item.recipient->'work'->>'policy_name', NEW.payload,
            item.recipient->'work'->>'key_selector') IS NOT NULL
        ORDER BY lock_id
    LOOP
        PERFORM pg_advisory_xact_lock(lane.lock_id);
    END LOOP;

    SELECT coalesce(jsonb_agg(
        CASE WHEN jsonb_typeof(item.recipient->'work') = 'object'
                   AND event_order_lane_active(item.recipient->>'app_id',
                       item.recipient->'work'->>'policy_name',
                       coalesce((item.recipient->'work'->>'ordered')::boolean, false))
                   AND event_order_lane_lock_id(item.recipient->>'app_id',
                       item.recipient->'work'->>'policy_name', NEW.payload,
                       item.recipient->'work'->>'key_selector') IS NOT NULL
             THEN jsonb_set(item.recipient, '{work,routing_order}',
                       to_jsonb(nextval('event_fanout_acceptance_order_seq')::bigint), true)
             ELSE item.recipient
        END ORDER BY item.position), '[]'::jsonb)
    INTO snapshot
    FROM jsonb_array_elements(NEW.recipient_snapshot) WITH ORDINALITY AS item(recipient, position);

    IF snapshot IS DISTINCT FROM NEW.recipient_snapshot THEN
        UPDATE event_fanout_outbox SET recipient_snapshot = snapshot WHERE id = NEW.id;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION financial_compute_deployment_environment(target_deployment uuid)
RETURNS uuid LANGUAGE sql STABLE CALLED ON NULL INPUT AS $$
 SELECT COALESCE(owner.environment_id, named.id)
 FROM deployments d JOIN apps a ON a.id=d.app_id
 LEFT JOIN deployment_runtime_environment_owners owner ON owner.deployment_id=d.id
 LEFT JOIN project_environments named ON owner.deployment_id IS NULL AND named.project_id=a.project_id
  AND named.slug=CASE WHEN COALESCE(NULLIF(d.scope,''),'default')='default' THEN 'production' ELSE d.scope END
 WHERE d.id=target_deployment
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invocation_async_subject(app uuid, namespace uuid)
RETURNS uuid LANGUAGE sql STABLE CALLED ON NULL INPUT AS $$
 SELECT CASE WHEN o.id IS NOT NULL AND NOT o.personal_org AND o.resource_ownership_enabled
             AND o.async_quota_pooled_at IS NOT NULL THEN o.id ELSE a.account_id END
 FROM apps a LEFT JOIN orgs o ON o.id=a.org_id WHERE a.id=app AND a.account_id=namespace;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app_workflow_definitions(target_app uuid, manifest jsonb)
RETURNS jsonb LANGUAGE sql STABLE AS $$
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION workflow_event_recipients(target_account uuid, event_source text, event_type text)
RETURNS TABLE(recipient jsonb) LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object(
   'id', md5('gregale.workflow.event:' || a.id::text || ':' || (definition->>'name'))::uuid,
   'account_id', a.account_id, 'app_id', a.id, 'deployment_id', d.id,
   'source', definition->'trigger'->>'source', 'type', definition->'trigger'->>'event_type',
   'filter', coalesce(definition->'trigger'->'filter', '{}'::jsonb), 'workflow', definition)
 FROM apps a JOIN app_resource_accounts ac ON ac.app_id=a.id
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION capture_event_recipient_ordering_snapshot() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    snapshot jsonb;
    lane record;
BEGIN
    IF NEW.recipient_snapshot IS NULL OR jsonb_array_length(NEW.recipient_snapshot) = 0 THEN
        RETURN NEW;
    END IF;

    FOR lane IN
        SELECT DISTINCT event_order_lane_lock_id(
            item.recipient->>'app_id', item.recipient->'work'->>'policy_name', NEW.payload,
            item.recipient->'work'->>'key_selector') AS lock_id
        FROM jsonb_array_elements(NEW.recipient_snapshot) item(recipient)
        WHERE jsonb_typeof(item.recipient->'work') = 'object'
          AND item.recipient->'work'->>'policy_name' <> ''
          AND event_order_lane_active(item.recipient->>'app_id',
              item.recipient->'work'->>'policy_name',
              coalesce((item.recipient->'work'->>'ordered')::boolean, false))
          AND event_order_lane_lock_id(
            item.recipient->>'app_id', item.recipient->'work'->>'policy_name', NEW.payload,
            item.recipient->'work'->>'key_selector') IS NOT NULL
        ORDER BY lock_id
    LOOP
        PERFORM pg_advisory_xact_lock(lane.lock_id);
    END LOOP;

    SELECT coalesce(jsonb_agg(
        CASE WHEN jsonb_typeof(item.recipient->'work') = 'object'
                   AND event_order_lane_active(item.recipient->>'app_id',
                       item.recipient->'work'->>'policy_name',
                       coalesce((item.recipient->'work'->>'ordered')::boolean, false))
                   AND event_order_lane_lock_id(item.recipient->>'app_id',
                       item.recipient->'work'->>'policy_name', NEW.payload,
                       item.recipient->'work'->>'key_selector') IS NOT NULL
             THEN jsonb_set(item.recipient, '{work,routing_order}',
                       to_jsonb(nextval('event_fanout_acceptance_order_seq')::bigint), true)
             ELSE item.recipient
        END ORDER BY item.position), '[]'::jsonb)
    INTO snapshot
    FROM jsonb_array_elements(NEW.recipient_snapshot) WITH ORDINALITY AS item(recipient, position);

    UPDATE event_fanout_outbox SET recipient_snapshot = snapshot WHERE id = NEW.id;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
