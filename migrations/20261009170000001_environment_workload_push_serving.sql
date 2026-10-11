-- Workers and HTTP functions can become active only when the reviewed graph
-- freezes their supported queue binding identities. Push consumers require
-- their exact server-managed trigger; pull workers use the generic queue drain.
-- Serving requires one completed invocation on that graph's active deployment
-- in addition to any required HTTP route acknowledgements.
-- +goose Up
ALTER TABLE environment_workload_serving_receipts
    DROP CONSTRAINT IF EXISTS environment_workload_serving_receipts_expected_gateways_check;
ALTER TABLE environment_workload_serving_receipts
    ADD CONSTRAINT environment_workload_serving_receipts_expected_gateways_check
    CHECK (cardinality(expected_gateways) >= 0);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_graph_service_bindings_supported(target_graph uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
SELECT EXISTS (
    SELECT 1 FROM environment_workload_graphs graph
    WHERE graph.id=target_graph AND CASE WHEN jsonb_typeof(graph.members)='array'
        THEN jsonb_array_length(graph.members)>0 ELSE false END
        AND NOT EXISTS (
            SELECT 1
            FROM jsonb_array_elements(CASE WHEN jsonb_typeof(graph.members)='array' THEN graph.members ELSE '[]'::jsonb END) AS caller(value)
            CROSS JOIN LATERAL (SELECT CASE WHEN jsonb_typeof(caller.value->'service_bindings')='object'
                THEN caller.value->'service_bindings' ELSE '{}'::jsonb END AS bindings) frozen
            WHERE coalesce(jsonb_typeof(caller.value->'service_bindings'),'null') NOT IN ('object','null')
                OR coalesce(caller.value->>'service_bindings_configured','false')<>
                    (frozen.bindings<>'{}'::jsonb)::text
                OR (frozen.bindings<>'{}'::jsonb AND caller.value->>'execution_mode' NOT IN ('request','service','worker','job'))
                OR (caller.value->>'execution_mode'='job' AND frozen.bindings<>'{}'::jsonb AND
                    (coalesce(caller.value->>'schedule_configured','false')<>'true' OR coalesce(caller.value->>'job_smoke_configured','false')<>'true' OR
                     coalesce(caller.value->>'queue_bindings_configured','false')<>'false' OR
                     coalesce(nullif(caller.value->'queue_bindings','null'::jsonb),'{}'::jsonb)<>'{}'::jsonb OR
                     coalesce(nullif(caller.value->'queue_modes','null'::jsonb),'{}'::jsonb)<>'{}'::jsonb))
                OR EXISTS (
                    SELECT 1 FROM jsonb_each(frozen.bindings) AS binding(name,value)
                    WHERE binding.name !~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'
                        OR binding.name ~ '^tag-'
                        OR jsonb_typeof(binding.value)<>'object'
                        OR coalesce(binding.value->>'workload','') !~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'
                        OR coalesce(binding.value->>'workload','') ~ '^tag-'
                        OR coalesce(binding.value->>'env_key','') !~ '^[A-Z][A-Z0-9_]*$'
                        OR octet_length(coalesce(binding.value->>'env_key',''))>128
                        OR coalesce(binding.value->>'target_app_id','') !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                        OR binding.value->>'target_app_id'=caller.value->>'app_id'
                        OR NOT EXISTS (
                            SELECT 1
                            FROM jsonb_array_elements(CASE WHEN jsonb_typeof(graph.members)='array' THEN graph.members ELSE '[]'::jsonb END) AS target(value)
                            WHERE target.value->>'resource'='workload/'||(binding.value->>'workload')
                                AND target.value->>'app_id'=binding.value->>'target_app_id'
                                AND target.value->>'app_id'<>caller.value->>'app_id'
                                AND target.value->>'execution_mode' IN ('request','service')
                                AND ((nullif(target.value->>'candidate_deployment_id','') IS NULL)=
                                    (nullif(caller.value->>'candidate_deployment_id','') IS NULL))
                        )
                )
                OR (SELECT count(*)<>count(DISTINCT binding.value->>'env_key')
                    FROM jsonb_each(frozen.bindings) AS binding(name,value))
        )
)
$$;

-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_graph_serving_supported(target_graph uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
SELECT EXISTS (
    SELECT 1 FROM environment_workload_graphs graph
    WHERE graph.id=target_graph AND CASE WHEN jsonb_typeof(graph.members)='array'
        THEN jsonb_array_length(graph.members)>0 ELSE false END
        AND environment_workload_graph_service_bindings_supported(target_graph)
        AND NOT EXISTS (
            SELECT 1
            FROM jsonb_array_elements(graph.members) AS graph_member(value)
            WHERE NOT coalesce((
                (
                    graph_member.value->>'execution_mode' IN ('request','service')
                    AND coalesce(nullif(graph_member.value->'queue_modes','null'::jsonb),'{}'::jsonb)='{}'::jsonb
                    AND NOT EXISTS (
                        SELECT 1 FROM jsonb_each(coalesce(nullif(graph_member.value->'queue_bindings','null'::jsonb),'{}'::jsonb)) AS queue_binding(name,value)
                        WHERE queue_binding.value->'contract'->>'enabled'='true'
                    )
                ) OR (
                    graph_member.value->>'execution_mode'='request'
                    AND graph_member.value->>'function'='true'
                    AND jsonb_typeof(graph_member.value->'queue_modes')='object'
                    AND (SELECT count(*) FROM jsonb_each(coalesce(nullif(graph_member.value->'queue_modes','null'::jsonb),'{}'::jsonb)))>0
                    AND (SELECT count(*) FROM jsonb_each(coalesce(nullif(graph_member.value->'queue_modes','null'::jsonb),'{}'::jsonb)))=(
                        SELECT count(*) FROM jsonb_each(coalesce(nullif(graph_member.value->'queue_bindings','null'::jsonb),'{}'::jsonb)) AS queue_binding(name,value)
                        WHERE queue_binding.value->'contract'->>'enabled'='true'
                    )
                    AND NOT EXISTS (
                        SELECT 1
                        FROM jsonb_each(coalesce(nullif(graph_member.value->'queue_modes','null'::jsonb),'{}'::jsonb)) AS queue_mode(name,value)
                        WHERE queue_mode.value<>'"push"'::jsonb OR NOT EXISTS (
                            SELECT 1
                            FROM jsonb_each(coalesce(nullif(graph_member.value->'queue_bindings','null'::jsonb),'{}'::jsonb)) AS queue_binding(name,value)
                            WHERE queue_binding.name=queue_mode.name
                                AND queue_binding.value->'contract'->>'enabled'='true'
                                AND queue_binding.value->'contract'->>'mode'='push'
                                AND queue_binding.value->'contract'->>'workload_class'='http'
                                AND coalesce(queue_binding.value->>'binding_id','') ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                                AND coalesce(queue_binding.value->>'trigger_id','') ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                        )
                    )
                    AND NOT EXISTS (
                        SELECT 1
                        FROM jsonb_each(coalesce(nullif(graph_member.value->'queue_bindings','null'::jsonb),'{}'::jsonb)) AS queue_binding(name,value)
                        WHERE queue_binding.value->'contract'->>'enabled'='true'
                            AND NOT (coalesce(nullif(graph_member.value->'queue_modes','null'::jsonb),'{}'::jsonb) ? queue_binding.name)
                    )
                ) OR (
                    graph_member.value->>'execution_mode'='worker'
                    AND jsonb_typeof(graph_member.value->'queue_modes')='object'
                    AND (SELECT count(*) FROM jsonb_each(coalesce(nullif(graph_member.value->'queue_modes','null'::jsonb),'{}'::jsonb)))>0
                    AND (SELECT count(*) FROM jsonb_each(coalesce(nullif(graph_member.value->'queue_modes','null'::jsonb),'{}'::jsonb)))=(
                        SELECT count(*) FROM jsonb_each(coalesce(nullif(graph_member.value->'queue_bindings','null'::jsonb),'{}'::jsonb)) AS queue_binding(name,value)
                        WHERE queue_binding.value->'contract'->>'enabled'='true'
                    )
                    AND NOT EXISTS (
                        SELECT 1
                        FROM jsonb_each(coalesce(nullif(graph_member.value->'queue_modes','null'::jsonb),'{}'::jsonb)) AS queue_mode(name,value)
                        WHERE queue_mode.value NOT IN ('"push"'::jsonb,'"pull"'::jsonb) OR NOT EXISTS (
                            SELECT 1
                            FROM jsonb_each(coalesce(nullif(graph_member.value->'queue_bindings','null'::jsonb),'{}'::jsonb)) AS queue_binding(name,value)
                            WHERE queue_binding.name=queue_mode.name
                                AND queue_binding.value->'contract'->>'enabled'='true'
                                AND queue_binding.value->'contract'->>'mode'=queue_mode.value#>>'{}'
                                AND queue_binding.value->'contract'->>'workload_class'='worker'
                                AND coalesce(queue_binding.value->>'binding_id','') ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                                AND ((queue_mode.value='"push"'::jsonb AND coalesce(queue_binding.value->>'trigger_id','') ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')
                                    OR (queue_mode.value='"pull"'::jsonb AND coalesce(queue_binding.value->>'trigger_id','')=''))
                        )
                    )
                    AND NOT EXISTS (
                        SELECT 1
                        FROM jsonb_each(coalesce(nullif(graph_member.value->'queue_bindings','null'::jsonb),'{}'::jsonb)) AS queue_binding(name,value)
                        WHERE queue_binding.value->'contract'->>'enabled'='true'
                            AND NOT (coalesce(nullif(graph_member.value->'queue_modes','null'::jsonb),'{}'::jsonb) ? queue_binding.name)
                    )
                ) OR (
                    graph_member.value->>'execution_mode'='job'
                    AND graph_member.value->>'schedule_configured'='true'
                    AND graph_member.value->>'job_smoke_configured'='true'
                    AND coalesce(graph_member.value->>'job_id','') ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                    AND coalesce(graph_member.value->>'queue_bindings_configured','false')='false'
                    AND coalesce(nullif(graph_member.value->'queue_bindings','null'::jsonb),'{}'::jsonb)='{}'::jsonb
                    AND coalesce(nullif(graph_member.value->'queue_modes','null'::jsonb),'{}'::jsonb)='{}'::jsonb
                    AND NOT EXISTS (
                        SELECT 1 FROM jsonb_each(coalesce(nullif(graph_member.value->'queue_bindings','null'::jsonb),'{}'::jsonb)) AS queue_binding(name,value)
                        WHERE queue_binding.value->'contract'->>'enabled'='true'
                    )
                )
            ),false)
        )
        AND NOT EXISTS (
            SELECT 1
            FROM active_environment_git_sources source
            JOIN project_environments environment ON environment.id=source.environment_id
            CROSS JOIN LATERAL jsonb_array_elements(graph.members) AS graph_member(value)
            CROSS JOIN LATERAL jsonb_each(coalesce(nullif(graph_member.value->'queue_bindings','null'::jsonb),'{}'::jsonb)) AS queue_binding(name,value)
            LEFT JOIN queue_bindings binding ON binding.id::text=queue_binding.value->>'binding_id'
            LEFT JOIN triggers queue_trigger ON queue_trigger.id::text=queue_binding.value->>'trigger_id'
            WHERE source.id=graph.source_id
                AND graph.id=target_graph
                AND (graph_member.value->>'execution_mode'='worker' OR
                    (graph_member.value->>'execution_mode'='request' AND graph_member.value->>'function'='true'))
                AND queue_binding.value->'contract'->>'enabled'='true'
                AND (binding.id IS NULL
                    OR binding.app_id::text IS DISTINCT FROM graph_member.value->>'app_id'
                    OR binding.account_id IS DISTINCT FROM source.account_id
                    OR binding.environment_id IS DISTINCT FROM source.environment_id
                    OR binding.deployment_scope IS DISTINCT FROM environment.slug
                    OR binding.name IS DISTINCT FROM queue_binding.name
                    OR binding.queue_name IS DISTINCT FROM queue_binding.value->'contract'->>'queue_name'
                    OR binding.mode IS DISTINCT FROM queue_binding.value->'contract'->>'mode'
                    OR binding.workload_class IS DISTINCT FROM queue_binding.value->'contract'->>'workload_class'
                    OR binding.enabled IS DISTINCT FROM (queue_binding.value->'contract'->>'enabled')::boolean
                    OR binding.max_concurrency::text IS DISTINCT FROM queue_binding.value->'contract'->>'max_concurrency'
                    OR binding.retry_policy IS DISTINCT FROM coalesce(nullif(queue_binding.value->'contract'->'retry_policy','null'::jsonb),'{}'::jsonb)
                    OR binding.retired_at IS NOT NULL
                    OR (binding.mode='push' AND (queue_trigger.id IS NULL
                        OR queue_trigger.app_id IS DISTINCT FROM binding.app_id
                        OR queue_trigger.account_id IS DISTINCT FROM source.account_id
                        OR queue_trigger.queue_binding_id IS DISTINCT FROM binding.id
                        OR NOT queue_trigger.enabled
                        OR queue_trigger.kind IS DISTINCT FROM 'queue'
                        OR queue_trigger.source IS DISTINCT FROM 'queue'
                        OR queue_trigger.slug IS DISTINCT FROM queue_binding.value->'contract'->>'queue_name'
                        OR queue_trigger.queue_binding_scope IS DISTINCT FROM environment.slug
                        OR queue_trigger.queue_binding_environment_id IS DISTINCT FROM source.environment_id))
                    OR (binding.mode='pull' AND (coalesce(queue_binding.value->>'trigger_id','')<>'' OR queue_trigger.id IS NOT NULL))
                    OR binding.mode NOT IN ('push','pull'))
        )
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
    IF NOT environment_workload_graph_serving_supported(NEW.graph_id) OR
        NOT environment_workload_queue_serving_ack_authorized(NEW.graph_id,NEW.binding_id,NEW.mode,NEW.trigger_id,
            NEW.app_id,NEW.deployment_id,NEW.invocation_id) THEN
        RAISE EXCEPTION 'environment queue serving acknowledgement is outside the supported active graph' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;

-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_activation_authorized(candidate deployments) RETURNS boolean
LANGUAGE plpgsql AS $$
DECLARE frozen jsonb; token text; source_uuid uuid; source_generation bigint; source_intent bigint; environment_uuid uuid; revision_uuid uuid;
BEGIN
    frozen:=candidate.environment_workload_runtime;
    IF frozen IS NULL OR candidate.environment_workload_held OR candidate.status<>'live' THEN RETURN false; END IF;
    token:=nullif(current_setting('gregale.gitops_activation',true),'');
    IF token IS NULL OR token IS DISTINCT FROM current_setting('gregale.gitops_lease',true) THEN RETURN false; END IF;
    BEGIN
        source_uuid:=(frozen->>'source_id')::uuid;
        source_generation:=(frozen->>'generation')::bigint;
        source_intent:=(frozen->>'intent_version')::bigint;
        environment_uuid:=(frozen->>'environment_id')::uuid;
        revision_uuid:=(frozen->>'revision_id')::uuid;
    EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN
        RETURN false;
    END;
    RETURN EXISTS (
        SELECT 1
        FROM active_environment_git_sources source
        JOIN environment_gitops_jobs job ON job.source_id=source.id
        JOIN environment_workload_graphs graph ON graph.source_id=source.id
        CROSS JOIN LATERAL jsonb_array_elements(graph.members) AS graph_member(value)
        WHERE source.id=source_uuid AND source.mode='enforce' AND NOT source.suspended
            AND source.generation=source_generation AND source.intent_version=source_intent
            AND source.environment_id=environment_uuid AND source.approved_revision_id=revision_uuid
            AND graph.environment_id=source.environment_id AND graph.revision_id=source.approved_revision_id
            AND graph.generation=source.generation AND graph.intent_version=source.intent_version
            AND graph.plan_hash=frozen->>'plan_hash' AND graph.phase='prepared'
            AND environment_workload_graph_serving_supported(graph.id)
            AND graph_member.value->>'resource'=frozen->>'resource'
            AND graph_member.value->>'app_id'=candidate.app_id::text
            AND graph_member.value->>'candidate_deployment_id'=candidate.id::text
            AND job.desired_generation=source.generation AND job.claimed_generation=source.generation
            AND job.lease_until>clock_timestamp() AND job.lease_token=token AND token<>''
    );
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
CREATE OR REPLACE FUNCTION guard_environment_workload_serving_receipt() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE completion_only boolean;
BEGIN
    IF TG_OP='DELETE' THEN
        IF EXISTS (SELECT 1 FROM environment_workload_graphs WHERE id=OLD.graph_id) AND
            NOT environment_workload_serving_authorized(OLD.source_id,OLD.graph_id,OLD.release_set_id) THEN
            RAISE EXCEPTION 'environment workload serving receipts are immutable' USING ERRCODE='23514';
        END IF;
        RETURN OLD;
    END IF;
    completion_only:=false;
    IF TG_OP='UPDATE' THEN
        completion_only:=OLD.phase='pending' AND NEW.phase='served'
            AND ROW(NEW.graph_id,NEW.source_id,NEW.source_generation,NEW.intent_version,NEW.revision_id,NEW.plan_hash,
                NEW.release_set_id,NEW.expected_gateways,NEW.started_at)
                IS NOT DISTINCT FROM ROW(OLD.graph_id,OLD.source_id,OLD.source_generation,OLD.intent_version,OLD.revision_id,
                    OLD.plan_hash,OLD.release_set_id,OLD.expected_gateways,OLD.started_at)
            AND environment_workload_serving_ready(NEW.graph_id);
    END IF;
    IF (NOT environment_workload_serving_authorized(NEW.source_id,NEW.graph_id,NEW.release_set_id) AND NOT completion_only) OR
        NOT EXISTS (SELECT 1 FROM environment_workload_graphs graph
            WHERE graph.id=NEW.graph_id AND graph.source_id=NEW.source_id AND graph.generation=NEW.source_generation
                AND graph.intent_version=NEW.intent_version AND graph.revision_id=NEW.revision_id AND graph.plan_hash=NEW.plan_hash) OR
        NOT environment_workload_graph_serving_supported(NEW.graph_id) THEN
        RAISE EXCEPTION 'environment workload serving receipt requires its current supported graph and lease' USING ERRCODE='23514';
    END IF;
    IF TG_OP='UPDATE' AND ROW(NEW.graph_id,NEW.source_id,NEW.source_generation,NEW.intent_version,NEW.revision_id,NEW.plan_hash,NEW.started_at)
        IS DISTINCT FROM ROW(OLD.graph_id,OLD.source_id,OLD.source_generation,OLD.intent_version,OLD.revision_id,OLD.plan_hash,OLD.started_at) THEN
        RAISE EXCEPTION 'environment workload serving receipt identity is immutable' USING ERRCODE='23514';
    END IF;
    IF NEW.phase='served' AND NOT environment_workload_serving_ready(NEW.graph_id) THEN
        RAISE EXCEPTION 'environment workload serving receipt lacks complete gateway and queue acknowledgements' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM environment_workload_serving_queue_acks) OR
        EXISTS (SELECT 1 FROM environment_workload_serving_receipts WHERE cardinality(expected_gateways)=0) THEN
        RAISE EXCEPTION 'queue serving evidence exists; this migration is forward-only after first use';
    END IF;
END $$;
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_activation_authorized(candidate deployments) RETURNS boolean
LANGUAGE plpgsql AS $$
DECLARE frozen jsonb; token text; source_uuid uuid; source_generation bigint; source_intent bigint; environment_uuid uuid; revision_uuid uuid;
BEGIN
    frozen:=candidate.environment_workload_runtime;
    IF frozen IS NULL OR candidate.environment_workload_held OR candidate.status<>'live' THEN RETURN false; END IF;
    token:=nullif(current_setting('gregale.gitops_activation',true),'');
    IF token IS NULL OR token IS DISTINCT FROM current_setting('gregale.gitops_lease',true) THEN RETURN false; END IF;
    BEGIN
        source_uuid:=(frozen->>'source_id')::uuid;
        source_generation:=(frozen->>'generation')::bigint;
        source_intent:=(frozen->>'intent_version')::bigint;
        environment_uuid:=(frozen->>'environment_id')::uuid;
        revision_uuid:=(frozen->>'revision_id')::uuid;
    EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN RETURN false;
    END;
    RETURN EXISTS (
        SELECT 1 FROM active_environment_git_sources source
        JOIN environment_gitops_jobs job ON job.source_id=source.id
        JOIN environment_workload_graphs graph ON graph.source_id=source.id
        CROSS JOIN LATERAL jsonb_array_elements(graph.members) AS graph_member(value)
        WHERE source.id=source_uuid AND source.mode='enforce' AND NOT source.suspended
            AND source.generation=source_generation AND source.intent_version=source_intent
            AND source.environment_id=environment_uuid AND source.approved_revision_id=revision_uuid
            AND graph.environment_id=source.environment_id AND graph.revision_id=source.approved_revision_id
            AND graph.generation=source.generation AND graph.intent_version=source.intent_version
            AND graph.plan_hash=frozen->>'plan_hash' AND graph.phase='prepared'
            AND graph_member.value->>'resource'=frozen->>'resource'
            AND graph_member.value->>'app_id'=candidate.app_id::text
            AND graph_member.value->>'candidate_deployment_id'=candidate.id::text
            AND job.desired_generation=source.generation AND job.claimed_generation=source.generation
            AND job.lease_until>clock_timestamp() AND job.lease_token=token AND token<>''
    );
END $$;

-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_serving_receipt() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        IF EXISTS (SELECT 1 FROM environment_workload_graphs WHERE id=OLD.graph_id) AND
            NOT environment_workload_serving_authorized(OLD.source_id,OLD.graph_id,OLD.release_set_id) THEN
            RAISE EXCEPTION 'environment workload serving receipts are immutable' USING ERRCODE='23514';
        END IF;
        RETURN OLD;
    END IF;
    IF NOT environment_workload_serving_authorized(NEW.source_id,NEW.graph_id,NEW.release_set_id) OR
        NOT EXISTS (SELECT 1 FROM environment_workload_graphs graph
            WHERE graph.id=NEW.graph_id AND graph.source_id=NEW.source_id AND graph.generation=NEW.source_generation
                AND graph.intent_version=NEW.intent_version AND graph.revision_id=NEW.revision_id AND graph.plan_hash=NEW.plan_hash) THEN
        RAISE EXCEPTION 'environment workload serving receipt requires its current lease and active graph' USING ERRCODE='23514';
    END IF;
    IF TG_OP='UPDATE' AND ROW(NEW.graph_id,NEW.source_id,NEW.source_generation,NEW.intent_version,NEW.revision_id,NEW.plan_hash,NEW.started_at)
        IS DISTINCT FROM ROW(OLD.graph_id,OLD.source_id,OLD.source_generation,OLD.intent_version,OLD.revision_id,OLD.plan_hash,OLD.started_at) THEN
        RAISE EXCEPTION 'environment workload serving receipt identity is immutable' USING ERRCODE='23514';
    END IF;
    IF NEW.phase='served' AND (NOT EXISTS(SELECT 1 FROM environment_workload_serving_routes route WHERE route.graph_id=NEW.graph_id) OR
        (SELECT count(*) FROM environment_workload_serving_routes route WHERE route.graph_id=NEW.graph_id) <
            (SELECT jsonb_array_length(graph.members) FROM environment_workload_graphs graph WHERE graph.id=NEW.graph_id) OR
        EXISTS(SELECT 1 FROM environment_workload_graphs graph, jsonb_array_elements(graph.members) member
            WHERE graph.id=NEW.graph_id AND (member->>'execution_mode' NOT IN ('request','service') OR
                coalesce(member->'queue_modes','{}'::jsonb) NOT IN ('{}'::jsonb,'null'::jsonb))) OR
        EXISTS(SELECT 1 FROM environment_workload_serving_routes route WHERE route.graph_id=NEW.graph_id AND
            (SELECT count(*) FROM environment_workload_serving_route_acks ack WHERE ack.graph_id=route.graph_id
                AND ack.route_generation=route.route_generation)<cardinality(NEW.expected_gateways))) THEN
        RAISE EXCEPTION 'environment workload serving receipt lacks complete gateway acknowledgements' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;

-- +goose StatementEnd
DROP FUNCTION IF EXISTS environment_workload_serving_ready(uuid);
DROP FUNCTION IF EXISTS environment_workload_graph_serving_supported(uuid);
DROP FUNCTION IF EXISTS environment_workload_graph_service_bindings_supported(uuid);
ALTER TABLE environment_workload_serving_receipts
    DROP CONSTRAINT IF EXISTS environment_workload_serving_receipts_expected_gateways_check;
ALTER TABLE environment_workload_serving_receipts
    ADD CONSTRAINT environment_workload_serving_receipts_expected_gateways_check
    CHECK (cardinality(expected_gateways) > 0);
