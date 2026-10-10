-- Persist the final, gateway-acknowledged transition from an activated dark
-- workload graph to its approved request/service routes.
-- +goose Up
CREATE TABLE IF NOT EXISTS environment_workload_serving_receipts (
    graph_id uuid PRIMARY KEY REFERENCES environment_workload_graphs(id) ON DELETE CASCADE,
    source_id uuid NOT NULL REFERENCES environment_git_sources(id) ON DELETE CASCADE,
    release_set_id uuid NOT NULL,
    source_generation bigint NOT NULL CHECK (source_generation > 0),
    intent_version bigint NOT NULL CHECK (intent_version > 0),
    revision_id uuid NOT NULL,
    plan_hash text NOT NULL CHECK (plan_hash ~ '^[a-f0-9]{64}$'),
    expected_gateways text[] NOT NULL CHECK (cardinality(expected_gateways) > 0),
    phase text NOT NULL DEFAULT 'pending' CHECK (phase IN ('pending', 'served')),
    started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    served_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((phase = 'served') = (served_at IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS environment_workload_serving_routes (
    graph_id uuid NOT NULL REFERENCES environment_workload_serving_receipts(graph_id) ON DELETE CASCADE,
    app_id uuid NOT NULL,
    deployment_id uuid NOT NULL,
    cutover_required boolean NOT NULL,
    route_generation bigint NOT NULL UNIQUE CHECK (route_generation > 0),
    PRIMARY KEY (graph_id, app_id),
    UNIQUE (graph_id, route_generation)
);

CREATE TABLE IF NOT EXISTS environment_workload_serving_route_acks (
    graph_id uuid NOT NULL,
    route_generation bigint NOT NULL,
    node_name text NOT NULL CHECK (length(btrim(node_name)) BETWEEN 1 AND 255),
    acknowledged_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (graph_id, route_generation, node_name),
    FOREIGN KEY (graph_id, route_generation)
        REFERENCES environment_workload_serving_routes(graph_id, route_generation) ON DELETE CASCADE
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_serving_authorized(
    target_source uuid, target_graph uuid, target_release uuid
) RETURNS boolean
LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
  SELECT 1
  FROM active_environment_git_sources src
  JOIN environment_gitops_jobs job ON job.source_id=src.id
  JOIN environment_workload_graphs graph ON graph.id=target_graph AND graph.source_id=src.id
  JOIN project_environments env ON env.id=src.environment_id
  JOIN project_release_sets release ON release.id=target_release AND release.project_id=src.project_id
      AND release.environment_slug=env.slug AND release.active
  WHERE src.id=target_source AND src.mode='enforce' AND NOT src.suspended
    AND src.generation=graph.generation AND src.intent_version=graph.intent_version
    AND src.approved_revision_id=graph.revision_id AND graph.phase='prepared'
    AND job.desired_generation=src.generation AND job.claimed_generation=src.generation
    AND job.lease_until>clock_timestamp() AND job.lease_token<>''
    AND job.lease_token=current_setting('gregale.gitops_lease',true)
    AND current_setting('gregale.gitops_serving',true)=job.lease_token
 )
$$;

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
 IF NEW.phase='served' AND (NOT EXISTS(SELECT 1 FROM environment_workload_serving_routes r WHERE r.graph_id=NEW.graph_id) OR
    (SELECT count(*) FROM environment_workload_serving_routes r WHERE r.graph_id=NEW.graph_id) <
      (SELECT jsonb_array_length(graph.members) FROM environment_workload_graphs graph WHERE graph.id=NEW.graph_id) OR
    EXISTS(SELECT 1 FROM environment_workload_graphs graph, jsonb_array_elements(graph.members) member
      WHERE graph.id=NEW.graph_id AND (member->>'execution_mode' NOT IN ('request','service') OR
        coalesce(member->'queue_modes','{}'::jsonb) NOT IN ('{}'::jsonb,'null'::jsonb))) OR
    EXISTS(SELECT 1 FROM environment_workload_serving_routes r WHERE r.graph_id=NEW.graph_id AND
      (SELECT count(*) FROM environment_workload_serving_route_acks a WHERE a.graph_id=r.graph_id AND a.route_generation=r.route_generation)
       < cardinality(NEW.expected_gateways))) THEN
  RAISE EXCEPTION 'environment workload serving receipt lacks complete gateway acknowledgements' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;

-- +goose StatementEnd
DROP TRIGGER IF EXISTS environment_workload_serving_receipt_guard ON environment_workload_serving_receipts;
CREATE TRIGGER environment_workload_serving_receipt_guard
BEFORE INSERT OR UPDATE OR DELETE ON environment_workload_serving_receipts
FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_serving_receipt();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_serving_route() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE receipt environment_workload_serving_receipts%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM environment_workload_serving_receipts WHERE graph_id=OLD.graph_id) THEN
   SELECT * INTO receipt FROM environment_workload_serving_receipts WHERE graph_id=OLD.graph_id;
   IF NOT environment_workload_serving_authorized(receipt.source_id,receipt.graph_id,receipt.release_set_id) THEN
    RAISE EXCEPTION 'environment workload serving routes are immutable' USING ERRCODE='23514';
   END IF;
  END IF;
  RETURN OLD;
 END IF;
 SELECT * INTO receipt FROM environment_workload_serving_receipts WHERE graph_id=NEW.graph_id FOR UPDATE;
 IF receipt.graph_id IS NULL OR NOT environment_workload_serving_authorized(receipt.source_id,receipt.graph_id,receipt.release_set_id) OR
    receipt.phase<>'pending' OR NOT EXISTS(
      SELECT 1 FROM project_release_sets release
      JOIN project_release_members rm ON rm.release_id=release.id AND rm.app_id=NEW.app_id AND rm.deployment_id=NEW.deployment_id
      JOIN environment_workload_graphs graph ON graph.id=receipt.graph_id
      CROSS JOIN LATERAL jsonb_array_elements(graph.members) member
      WHERE release.id=receipt.release_set_id AND release.active
        AND member->>'app_id'=NEW.app_id::text AND member->>'execution_mode' IN ('request','service')
        AND coalesce(member->'queue_modes','{}'::jsonb) IN ('{}'::jsonb,'null'::jsonb)
        AND ((NEW.cutover_required AND member->>'candidate_deployment_id'=NEW.deployment_id::text) OR
             (NOT NEW.cutover_required AND nullif(member->>'candidate_deployment_id','') IS NULL AND
              coalesce(member->'retained_deployments','[]'::jsonb) ? NEW.deployment_id::text))) THEN
  RAISE EXCEPTION 'environment workload serving route is outside the reviewed active graph' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;

-- +goose StatementEnd
DROP TRIGGER IF EXISTS environment_workload_serving_route_guard ON environment_workload_serving_routes;
CREATE TRIGGER environment_workload_serving_route_guard
BEFORE INSERT OR UPDATE OR DELETE ON environment_workload_serving_routes
FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_serving_route();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_serving_route_ack() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE receipt environment_workload_serving_receipts%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN
  SELECT * INTO receipt FROM environment_workload_serving_receipts WHERE graph_id=OLD.graph_id;
  IF receipt.graph_id IS NOT NULL AND NOT environment_workload_serving_authorized(receipt.source_id,receipt.graph_id,receipt.release_set_id) THEN
   RAISE EXCEPTION 'environment workload serving acknowledgements are immutable' USING ERRCODE='23514';
  END IF;
  RETURN OLD;
 END IF;
 SELECT * INTO receipt FROM environment_workload_serving_receipts WHERE graph_id=NEW.graph_id FOR UPDATE;
 IF receipt.graph_id IS NULL OR receipt.phase<>'pending' OR
    NOT environment_workload_serving_authorized(receipt.source_id,receipt.graph_id,receipt.release_set_id) OR
    NOT (NEW.node_name=ANY(receipt.expected_gateways)) OR
    NOT EXISTS(SELECT 1 FROM environment_workload_serving_routes WHERE graph_id=NEW.graph_id AND route_generation=NEW.route_generation) THEN
  RAISE EXCEPTION 'environment workload serving acknowledgement is not expected' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;

-- +goose StatementEnd
DROP TRIGGER IF EXISTS environment_workload_serving_route_ack_guard ON environment_workload_serving_route_acks;
CREATE TRIGGER environment_workload_serving_route_ack_guard
BEFORE INSERT OR UPDATE OR DELETE ON environment_workload_serving_route_acks
FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_serving_route_ack();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_serving_traffic_authorized(
    target_app uuid, target_scope text, target_deployment uuid, target_traffic integer
) RETURNS boolean
LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
  SELECT 1
  FROM active_environment_git_sources src
  JOIN environment_gitops_jobs job ON job.source_id=src.id
  JOIN project_environments env ON env.id=src.environment_id AND env.slug=target_scope
  JOIN environment_workload_graphs graph ON graph.source_id=src.id AND graph.phase='prepared'
  JOIN environment_workload_serving_receipts receipt ON receipt.graph_id=graph.id AND receipt.phase='pending'
  JOIN environment_workload_serving_routes route ON route.graph_id=receipt.graph_id AND route.app_id=target_app
  JOIN project_release_sets release ON release.id=receipt.release_set_id AND release.active
  JOIN project_release_members rm ON rm.release_id=release.id AND rm.app_id=route.app_id AND rm.deployment_id=route.deployment_id
  CROSS JOIN LATERAL jsonb_array_elements(graph.members) member
  WHERE src.id=receipt.source_id AND src.mode='enforce' AND NOT src.suspended
    AND src.generation=graph.generation AND src.intent_version=graph.intent_version
    AND src.approved_revision_id=graph.revision_id AND graph.plan_hash=receipt.plan_hash
    AND member->>'app_id'=target_app::text AND member->>'execution_mode' IN ('request','service')
    AND coalesce(member->'queue_modes','{}'::jsonb) IN ('{}'::jsonb,'null'::jsonb)
    AND route.cutover_required AND member->>'candidate_deployment_id'=route.deployment_id::text
    AND job.desired_generation=src.generation AND job.claimed_generation=src.generation
    AND job.lease_until>clock_timestamp() AND job.lease_token<>''
    AND job.lease_token=current_setting('gregale.gitops_lease',true)
    AND current_setting('gregale.gitops_serving',true)=job.lease_token
    AND ((target_deployment=route.deployment_id AND target_traffic=100) OR
         (target_deployment<>route.deployment_id AND target_traffic=0))
 )
$$;

-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_serving_traffic() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.traffic_percent IS DISTINCT FROM OLD.traffic_percent AND
    EXISTS(SELECT 1 FROM deployments managed WHERE managed.app_id=OLD.app_id AND managed.scope=OLD.scope AND managed.environment_workload_runtime IS NOT NULL) AND
    NOT environment_workload_serving_traffic_authorized(NEW.app_id,NEW.scope,NEW.id,NEW.traffic_percent) AND
    NOT (NEW.traffic_percent=0 AND OLD.environment_workload_held AND NOT NEW.environment_workload_held AND
      environment_workload_activation_authorized(NEW)) THEN
  RAISE EXCEPTION 'GitOps-managed workload traffic requires a current serving transition' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;

-- +goose StatementEnd
DROP TRIGGER IF EXISTS environment_workload_serving_traffic_guard ON deployments;
CREATE TRIGGER environment_workload_serving_traffic_guard
BEFORE UPDATE OF traffic_percent ON deployments
FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_serving_traffic();

-- +goose Down
DROP TRIGGER IF EXISTS environment_workload_serving_traffic_guard ON deployments;
DROP FUNCTION IF EXISTS guard_environment_workload_serving_traffic();
DROP FUNCTION IF EXISTS environment_workload_serving_traffic_authorized(uuid,text,uuid,integer);
DROP TRIGGER IF EXISTS environment_workload_serving_route_ack_guard ON environment_workload_serving_route_acks;
DROP FUNCTION IF EXISTS guard_environment_workload_serving_route_ack();
DROP TRIGGER IF EXISTS environment_workload_serving_route_guard ON environment_workload_serving_routes;
DROP FUNCTION IF EXISTS guard_environment_workload_serving_route();
DROP TRIGGER IF EXISTS environment_workload_serving_receipt_guard ON environment_workload_serving_receipts;
DROP FUNCTION IF EXISTS guard_environment_workload_serving_receipt();
DROP FUNCTION IF EXISTS environment_workload_serving_authorized(uuid,uuid,uuid);
DROP TABLE IF EXISTS environment_workload_serving_route_acks;
DROP TABLE IF EXISTS environment_workload_serving_routes;
DROP TABLE IF EXISTS environment_workload_serving_receipts;
