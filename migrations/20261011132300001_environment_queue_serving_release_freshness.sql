-- Queue serving proof must come from a delivery completed after the active
-- release was published. Re-evaluate existing acknowledgements as well as new
-- inserts, so a retained row cannot prove a later release is serving.
-- +goose Up
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
        AND invocation.completed_at>=release.created_at AND invocation.deployment_scope=environment.slug
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
CREATE OR REPLACE FUNCTION environment_workload_serving_ready(target_graph uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
SELECT EXISTS (
    SELECT 1
    FROM environment_workload_serving_receipts receipt
    JOIN environment_workload_graphs graph ON graph.id=receipt.graph_id
    JOIN active_environment_git_sources source ON source.id=receipt.source_id
    JOIN project_environments environment ON environment.id=source.environment_id
    JOIN project_release_sets release ON release.id=receipt.release_set_id AND release.active
    WHERE receipt.graph_id=target_graph AND source.mode='enforce' AND NOT source.suspended
        AND source.generation=graph.generation AND source.intent_version=graph.intent_version
        AND source.approved_revision_id=graph.revision_id
        AND environment_workload_graph_serving_supported(graph.id)
        AND (SELECT count(*) FROM environment_workload_serving_routes route WHERE route.graph_id=graph.id)=(
            SELECT count(*) FROM jsonb_array_elements(graph.members) AS graph_member(value)
            WHERE graph_member.value->>'execution_mode' IN ('request','service')
        )
        AND NOT EXISTS (
            SELECT 1 FROM jsonb_array_elements(graph.members) AS graph_member(value)
            WHERE graph_member.value->>'execution_mode' IN ('request','service')
                AND NOT EXISTS (
                    SELECT 1 FROM environment_workload_serving_routes route
                    JOIN project_release_members release_member ON release_member.release_id=release.id
                        AND release_member.app_id=route.app_id AND release_member.deployment_id=route.deployment_id
                    WHERE route.graph_id=graph.id AND route.app_id::text=graph_member.value->>'app_id'
                        AND ((route.cutover_required AND route.deployment_id::text=graph_member.value->>'candidate_deployment_id') OR
                            (NOT route.cutover_required AND nullif(graph_member.value->>'candidate_deployment_id','') IS NULL
                                AND coalesce(graph_member.value->'retained_deployments','[]'::jsonb) ? route.deployment_id::text))
                )
        )
        AND (NOT EXISTS (SELECT 1 FROM environment_workload_serving_routes route WHERE route.graph_id=graph.id)
            OR cardinality(receipt.expected_gateways)>0)
        AND NOT EXISTS (
            SELECT 1 FROM environment_workload_serving_routes route
            WHERE route.graph_id=graph.id AND (SELECT count(*) FROM environment_workload_serving_route_acks ack
                WHERE ack.graph_id=route.graph_id AND ack.route_generation=route.route_generation)<>cardinality(receipt.expected_gateways)
        )
        AND NOT EXISTS (
            SELECT 1
            FROM jsonb_array_elements(graph.members) AS graph_member(value)
            CROSS JOIN LATERAL jsonb_each(coalesce(nullif(graph_member.value->'queue_bindings','null'::jsonb),'{}'::jsonb)) AS queue_binding(name,value)
            LEFT JOIN queue_bindings binding ON binding.id::text=queue_binding.value->>'binding_id'
            WHERE (graph_member.value->>'execution_mode'='worker' OR
                (graph_member.value->>'execution_mode'='request' AND graph_member.value->>'function'='true'))
                AND queue_binding.value->'contract'->>'enabled'='true'
                AND (binding.id IS NULL OR binding.app_id::text<>graph_member.value->>'app_id'
                    OR binding.account_id<>source.account_id
                    OR binding.environment_id<>source.environment_id OR binding.deployment_scope<>environment.slug
                    OR binding.name<>queue_binding.name OR binding.queue_name<>queue_binding.value->'contract'->>'queue_name'
                    OR binding.mode<>queue_binding.value->'contract'->>'mode'
                    OR binding.workload_class<>queue_binding.value->'contract'->>'workload_class'
                    OR binding.enabled IS DISTINCT FROM (queue_binding.value->'contract'->>'enabled')::boolean
                    OR binding.max_concurrency::text<>queue_binding.value->'contract'->>'max_concurrency'
                    OR coalesce(nullif(queue_binding.value->'contract'->'retry_policy','null'::jsonb),'{}'::jsonb)<>binding.retry_policy
                    OR binding.retired_at IS NOT NULL
                    OR (binding.mode='push' AND NOT EXISTS (
                        SELECT 1 FROM triggers queue_trigger
                        WHERE queue_trigger.id::text=queue_binding.value->>'trigger_id'
                            AND queue_trigger.app_id=binding.app_id AND queue_trigger.queue_binding_id=binding.id
                            AND queue_trigger.account_id=source.account_id
                            AND queue_trigger.enabled AND queue_trigger.kind='queue' AND queue_trigger.source='queue'
                            AND queue_trigger.slug=queue_binding.value->'contract'->>'queue_name'
                            AND queue_trigger.queue_binding_scope=environment.slug
                            AND queue_trigger.queue_binding_environment_id=source.environment_id
                    ))
                    OR (binding.mode='pull' AND coalesce(queue_binding.value->>'trigger_id','')<>'')
                    OR binding.mode NOT IN ('push','pull'))
        )
        AND (SELECT count(*) FROM environment_workload_serving_queue_acks ack WHERE ack.graph_id=graph.id)=(
            SELECT count(*)
            FROM jsonb_array_elements(graph.members) AS graph_member(value)
            CROSS JOIN LATERAL jsonb_each(coalesce(nullif(graph_member.value->'queue_bindings','null'::jsonb),'{}'::jsonb)) AS queue_binding(name,value)
            WHERE (graph_member.value->>'execution_mode'='worker' OR
                (graph_member.value->>'execution_mode'='request' AND graph_member.value->>'function'='true'))
                AND queue_binding.value->'contract'->>'enabled'='true'
        )
        AND NOT EXISTS (
            SELECT 1
            FROM jsonb_array_elements(graph.members) AS graph_member(value)
            CROSS JOIN LATERAL jsonb_each(coalesce(nullif(graph_member.value->'queue_bindings','null'::jsonb),'{}'::jsonb)) AS queue_binding(name,value)
            LEFT JOIN environment_workload_serving_queue_acks ack ON ack.graph_id=graph.id
                AND ack.binding_id::text=queue_binding.value->>'binding_id'
            WHERE (graph_member.value->>'execution_mode'='worker' OR
                (graph_member.value->>'execution_mode'='request' AND graph_member.value->>'function'='true'))
                AND queue_binding.value->'contract'->>'enabled'='true'
                AND (ack.graph_id IS NULL OR ack.mode<>queue_binding.value->'contract'->>'mode'
                    OR NOT environment_workload_queue_serving_ack_authorized(ack.graph_id,ack.binding_id,ack.mode,
                        ack.trigger_id,ack.app_id,ack.deployment_id,ack.invocation_id)
                    OR ack.app_id::text<>graph_member.value->>'app_id'
                    OR (ack.mode='push' AND ack.trigger_id::text<>queue_binding.value->>'trigger_id')
                    OR (ack.mode='pull' AND ack.trigger_id IS NOT NULL)
                    OR NOT EXISTS (
                        SELECT 1 FROM project_release_members release_member
                        JOIN deployments deployment ON deployment.id=release_member.deployment_id
                            AND deployment.app_id=release_member.app_id AND deployment.status='live'
                            AND NOT deployment.environment_workload_held AND deployment.scope=environment.slug
                        WHERE release_member.release_id=release.id AND release_member.app_id=ack.app_id
                            AND release_member.deployment_id=ack.deployment_id
                            AND (graph_member.value->>'candidate_deployment_id'=ack.deployment_id::text OR
                                (nullif(graph_member.value->>'candidate_deployment_id','') IS NULL
                                    AND coalesce(graph_member.value->'retained_deployments','[]'::jsonb) ? ack.deployment_id::text))
                    ))
        )
)
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM environment_workload_serving_queue_acks) THEN
        RAISE EXCEPTION 'queue serving evidence exists; freshness contract is forward-only after first use';
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_serving_ready(target_graph uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
SELECT EXISTS (
    SELECT 1
    FROM environment_workload_serving_receipts receipt
    JOIN environment_workload_graphs graph ON graph.id=receipt.graph_id
    JOIN active_environment_git_sources source ON source.id=receipt.source_id
    JOIN project_environments environment ON environment.id=source.environment_id
    JOIN project_release_sets release ON release.id=receipt.release_set_id AND release.active
    WHERE receipt.graph_id=target_graph AND source.mode='enforce' AND NOT source.suspended
        AND source.generation=graph.generation AND source.intent_version=graph.intent_version
        AND source.approved_revision_id=graph.revision_id
        AND environment_workload_graph_serving_supported(graph.id)
        AND (SELECT count(*) FROM environment_workload_serving_routes route WHERE route.graph_id=graph.id)=(
            SELECT count(*) FROM jsonb_array_elements(graph.members) AS graph_member(value)
            WHERE graph_member.value->>'execution_mode' IN ('request','service')
        )
        AND NOT EXISTS (
            SELECT 1 FROM jsonb_array_elements(graph.members) AS graph_member(value)
            WHERE graph_member.value->>'execution_mode' IN ('request','service')
                AND NOT EXISTS (
                    SELECT 1 FROM environment_workload_serving_routes route
                    JOIN project_release_members release_member ON release_member.release_id=release.id
                        AND release_member.app_id=route.app_id AND release_member.deployment_id=route.deployment_id
                    WHERE route.graph_id=graph.id AND route.app_id::text=graph_member.value->>'app_id'
                        AND ((route.cutover_required AND route.deployment_id::text=graph_member.value->>'candidate_deployment_id') OR
                            (NOT route.cutover_required AND nullif(graph_member.value->>'candidate_deployment_id','') IS NULL
                                AND coalesce(graph_member.value->'retained_deployments','[]'::jsonb) ? route.deployment_id::text))
                )
        )
        AND (NOT EXISTS (SELECT 1 FROM environment_workload_serving_routes route WHERE route.graph_id=graph.id)
            OR cardinality(receipt.expected_gateways)>0)
        AND NOT EXISTS (
            SELECT 1 FROM environment_workload_serving_routes route
            WHERE route.graph_id=graph.id AND (SELECT count(*) FROM environment_workload_serving_route_acks ack
                WHERE ack.graph_id=route.graph_id AND ack.route_generation=route.route_generation)<>cardinality(receipt.expected_gateways)
        )
        AND NOT EXISTS (
            SELECT 1
            FROM jsonb_array_elements(graph.members) AS graph_member(value)
            CROSS JOIN LATERAL jsonb_each(coalesce(nullif(graph_member.value->'queue_bindings','null'::jsonb),'{}'::jsonb)) AS queue_binding(name,value)
            LEFT JOIN queue_bindings binding ON binding.id::text=queue_binding.value->>'binding_id'
            WHERE (graph_member.value->>'execution_mode'='worker' OR
                (graph_member.value->>'execution_mode'='request' AND graph_member.value->>'function'='true'))
                AND queue_binding.value->'contract'->>'enabled'='true'
                AND (binding.id IS NULL OR binding.app_id::text<>graph_member.value->>'app_id'
                    OR binding.account_id<>source.account_id
                    OR binding.environment_id<>source.environment_id OR binding.deployment_scope<>environment.slug
                    OR binding.name<>queue_binding.name OR binding.queue_name<>queue_binding.value->'contract'->>'queue_name'
                    OR binding.mode<>queue_binding.value->'contract'->>'mode'
                    OR binding.workload_class<>queue_binding.value->'contract'->>'workload_class'
                    OR binding.enabled IS DISTINCT FROM (queue_binding.value->'contract'->>'enabled')::boolean
                    OR binding.max_concurrency::text<>queue_binding.value->'contract'->>'max_concurrency'
                    OR coalesce(nullif(queue_binding.value->'contract'->'retry_policy','null'::jsonb),'{}'::jsonb)<>binding.retry_policy
                    OR binding.retired_at IS NOT NULL
                    OR (binding.mode='push' AND NOT EXISTS (
                        SELECT 1 FROM triggers queue_trigger
                        WHERE queue_trigger.id::text=queue_binding.value->>'trigger_id'
                            AND queue_trigger.app_id=binding.app_id AND queue_trigger.queue_binding_id=binding.id
                            AND queue_trigger.account_id=source.account_id
                            AND queue_trigger.enabled AND queue_trigger.kind='queue' AND queue_trigger.source='queue'
                            AND queue_trigger.slug=queue_binding.value->'contract'->>'queue_name'
                            AND queue_trigger.queue_binding_scope=environment.slug
                            AND queue_trigger.queue_binding_environment_id=source.environment_id
                    ))
                    OR (binding.mode='pull' AND coalesce(queue_binding.value->>'trigger_id','')<>'')
                    OR binding.mode NOT IN ('push','pull'))
        )
        AND (SELECT count(*) FROM environment_workload_serving_queue_acks ack WHERE ack.graph_id=graph.id)=(
            SELECT count(*)
            FROM jsonb_array_elements(graph.members) AS graph_member(value)
            CROSS JOIN LATERAL jsonb_each(coalesce(nullif(graph_member.value->'queue_bindings','null'::jsonb),'{}'::jsonb)) AS queue_binding(name,value)
            WHERE (graph_member.value->>'execution_mode'='worker' OR
                (graph_member.value->>'execution_mode'='request' AND graph_member.value->>'function'='true'))
                AND queue_binding.value->'contract'->>'enabled'='true'
        )
        AND NOT EXISTS (
            SELECT 1
            FROM jsonb_array_elements(graph.members) AS graph_member(value)
            CROSS JOIN LATERAL jsonb_each(coalesce(nullif(graph_member.value->'queue_bindings','null'::jsonb),'{}'::jsonb)) AS queue_binding(name,value)
            LEFT JOIN environment_workload_serving_queue_acks ack ON ack.graph_id=graph.id
                AND ack.binding_id::text=queue_binding.value->>'binding_id'
            WHERE (graph_member.value->>'execution_mode'='worker' OR
                (graph_member.value->>'execution_mode'='request' AND graph_member.value->>'function'='true'))
                AND queue_binding.value->'contract'->>'enabled'='true'
                AND (ack.graph_id IS NULL OR ack.mode<>queue_binding.value->'contract'->>'mode'
                    OR ack.app_id::text<>graph_member.value->>'app_id'
                    OR (ack.mode='push' AND ack.trigger_id::text<>queue_binding.value->>'trigger_id')
                    OR (ack.mode='pull' AND ack.trigger_id IS NOT NULL)
                    OR NOT EXISTS (
                        SELECT 1 FROM project_release_members release_member
                        JOIN deployments deployment ON deployment.id=release_member.deployment_id
                            AND deployment.app_id=release_member.app_id AND deployment.status='live'
                            AND NOT deployment.environment_workload_held AND deployment.scope=environment.slug
                        WHERE release_member.release_id=release.id AND release_member.app_id=ack.app_id
                            AND release_member.deployment_id=ack.deployment_id
                            AND (graph_member.value->>'candidate_deployment_id'=ack.deployment_id::text OR
                                (nullif(graph_member.value->>'candidate_deployment_id','') IS NULL
                                    AND coalesce(graph_member.value->'retained_deployments','[]'::jsonb) ? ack.deployment_id::text))
                    ))
        )
)
$$;
-- +goose StatementEnd

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
