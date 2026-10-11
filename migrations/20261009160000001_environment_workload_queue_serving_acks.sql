-- Queue consumption needs its own proof: an HTTP route acknowledgement
-- cannot show that a worker received and completed a reviewed queue item.
-- +goose Up
CREATE TABLE IF NOT EXISTS environment_workload_serving_queue_acks (
    graph_id uuid NOT NULL REFERENCES environment_workload_graphs(id) ON DELETE CASCADE,
    binding_id uuid NOT NULL,
    mode text NOT NULL CHECK (mode IN ('push','pull')),
    trigger_id uuid,
    app_id uuid NOT NULL,
    deployment_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    acknowledged_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (graph_id, binding_id)
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_queue_serving_ack_authorized(
    target_graph uuid,
    target_binding uuid,
    target_mode text,
    target_trigger uuid,
    target_app uuid,
    target_deployment uuid,
    target_invocation uuid
) RETURNS boolean
LANGUAGE sql STABLE AS $$
SELECT EXISTS (
    SELECT 1
    FROM environment_workload_graphs graph
    JOIN active_environment_git_sources source ON source.id=graph.source_id
    JOIN project_environments environment ON environment.id=source.environment_id
    JOIN project_release_sets release ON release.project_id=source.project_id
        AND release.environment_slug=environment.slug AND release.active
    JOIN project_release_members release_member ON release_member.release_id=release.id
        AND release_member.app_id=target_app AND release_member.deployment_id=target_deployment
    JOIN deployments deployment ON deployment.id=target_deployment AND deployment.app_id=target_app
        AND deployment.status='live' AND NOT deployment.environment_workload_held
        AND deployment.scope=environment.slug
    JOIN invocations invocation ON invocation.id=target_invocation AND invocation.app_id=target_app
        AND invocation.source='queue' AND invocation.state='completed'
        AND invocation.completed_at IS NOT NULL AND invocation.deployment_scope=environment.slug
        AND invocation.queue_binding_id=target_binding
    CROSS JOIN LATERAL jsonb_array_elements(graph.members) AS graph_member(value)
    CROSS JOIN LATERAL jsonb_each(coalesce(graph_member.value->'queue_bindings','{}'::jsonb)) AS queue_binding(name,value)
    JOIN queue_bindings binding ON binding.id=target_binding
        AND binding.app_id=target_app AND binding.account_id=source.account_id AND binding.environment_id=source.environment_id
        AND binding.deployment_scope=environment.slug AND binding.name=queue_binding.name
        AND binding.mode=target_mode AND binding.enabled
        AND ((binding.workload_class='worker' AND graph_member.value->>'execution_mode'='worker') OR
            (binding.workload_class='http' AND graph_member.value->>'execution_mode'='request'
                AND graph_member.value->>'function'='true'))
        AND binding.retired_at IS NULL
    WHERE graph.id=target_graph AND graph.phase='prepared'
        AND source.mode='enforce' AND NOT source.suspended
        AND source.generation=graph.generation AND source.intent_version=graph.intent_version
        AND source.approved_revision_id=graph.revision_id
        AND graph.environment_id=source.environment_id
        AND ((graph_member.value->>'execution_mode'='worker' AND binding.workload_class='worker'
                AND target_mode IN ('push','pull')) OR
            (graph_member.value->>'execution_mode'='request' AND graph_member.value->>'function'='true'
                AND binding.workload_class='http' AND target_mode='push'))
        AND graph_member.value->>'app_id'=target_app::text
        AND coalesce(queue_binding.value->>'binding_id','')=binding.id::text
        AND queue_binding.value->'contract'->>'mode'=target_mode
        AND queue_binding.value->'contract'->>'workload_class'=binding.workload_class
        AND queue_binding.value->'contract'->>'enabled'='true'
        AND queue_binding.value->'contract'->>'queue_name'=binding.queue_name
        AND queue_binding.value->'contract'->>'max_concurrency'=binding.max_concurrency::text
        AND coalesce(nullif(queue_binding.value->'contract'->'retry_policy','null'::jsonb),'{}'::jsonb)=binding.retry_policy
        AND ((target_mode='push' AND target_trigger IS NOT NULL
                AND coalesce(queue_binding.value->>'trigger_id','')=target_trigger::text
                AND EXISTS (
                    SELECT 1 FROM triggers queue_trigger
                    JOIN trigger_records record ON record.trigger_id=queue_trigger.id
                        AND record.item_identifier=invocation.id::text AND record.state='succeeded'
                    WHERE queue_trigger.id=target_trigger AND queue_trigger.account_id=source.account_id
                        AND queue_trigger.app_id=target_app AND queue_trigger.queue_binding_id=binding.id
                        AND queue_trigger.enabled AND queue_trigger.kind='queue' AND queue_trigger.source='queue'
                        AND queue_trigger.slug=queue_binding.value->'contract'->>'queue_name'
                        AND queue_trigger.queue_binding_scope=environment.slug
                        AND queue_trigger.queue_binding_environment_id=source.environment_id)) OR
            (target_mode='pull' AND target_trigger IS NULL
                AND coalesce(queue_binding.value->>'trigger_id','')=''))
        AND (graph_member.value->>'candidate_deployment_id'=target_deployment::text OR
            (nullif(graph_member.value->>'candidate_deployment_id','') IS NULL AND
             coalesce(graph_member.value->'retained_deployments','[]'::jsonb) ? target_deployment::text))
)
$$;

-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_queue_serving_ack() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        IF EXISTS (SELECT 1 FROM environment_workload_graphs WHERE id=OLD.graph_id) THEN
            RAISE EXCEPTION 'environment queue serving acknowledgements are retained' USING ERRCODE='23514';
        END IF;
        RETURN OLD;
    END IF;
    IF TG_OP='UPDATE' AND ROW(NEW.graph_id,NEW.binding_id,NEW.mode,NEW.trigger_id,NEW.app_id,NEW.deployment_id,NEW.invocation_id)
        IS DISTINCT FROM ROW(OLD.graph_id,OLD.binding_id,OLD.mode,OLD.trigger_id,OLD.app_id,OLD.deployment_id,OLD.invocation_id) THEN
        RAISE EXCEPTION 'environment queue serving acknowledgement identity is immutable' USING ERRCODE='23514';
    END IF;
    IF NOT environment_workload_queue_serving_ack_authorized(NEW.graph_id,NEW.binding_id,NEW.mode,NEW.trigger_id,
        NEW.app_id,NEW.deployment_id,NEW.invocation_id) THEN
        RAISE EXCEPTION 'environment queue serving acknowledgement is outside the active reviewed graph' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;

-- +goose StatementEnd
DROP TRIGGER IF EXISTS environment_workload_queue_serving_ack_guard ON environment_workload_serving_queue_acks;
CREATE TRIGGER environment_workload_queue_serving_ack_guard
BEFORE INSERT OR UPDATE OR DELETE ON environment_workload_serving_queue_acks
FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_queue_serving_ack();

-- +goose Down
DROP TRIGGER IF EXISTS environment_workload_queue_serving_ack_guard ON environment_workload_serving_queue_acks;
DROP FUNCTION IF EXISTS guard_environment_workload_queue_serving_ack();
DROP TABLE IF EXISTS environment_workload_serving_queue_acks;
DROP FUNCTION IF EXISTS environment_workload_queue_serving_ack_authorized(uuid,uuid,text,uuid,uuid,uuid,uuid);
