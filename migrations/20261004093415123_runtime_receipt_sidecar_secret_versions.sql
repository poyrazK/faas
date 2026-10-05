-- ADR-568: primary secret suppressions retain a sidecar's explicit secret access.
-- +goose Up
ALTER TABLE instance_runtime_config_receipts ADD COLUMN IF NOT EXISTS sidecar_secret_versions jsonb NOT NULL DEFAULT '{}'
 CHECK (jsonb_typeof(sidecar_secret_versions)='object' AND octet_length(sidecar_secret_versions::text)<=1048576
  AND sidecar_secret_versions <@ secret_versions);
ALTER TABLE snapshot_runtime_config_receipts ADD COLUMN IF NOT EXISTS sidecar_secret_versions jsonb NOT NULL DEFAULT '{}'
 CHECK (jsonb_typeof(sidecar_secret_versions)='object' AND octet_length(sidecar_secret_versions::text)<=1048576
  AND sidecar_secret_versions <@ secret_versions);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_runtime_inputs_fresh(target_app uuid,target_scope text,boundary timestamptz,
 observed_variables jsonb,observed_secrets jsonb,observed_all_secrets boolean,observed_secret_refs jsonb,observed_sidecar_secrets jsonb)
RETURNS boolean LANGUAGE sql STABLE AS $$
 WITH managed AS (SELECT environment_scoped_secret_refs(target_app,target_scope) AS refs),
 suppressed AS (SELECT environment_scoped_secret_suppressions(target_app,target_scope) AS keys),
 eligible AS (
  SELECT s.key,s.scope,s.delivery_version FROM app_secrets s
  WHERE s.app_id=target_app AND s.scope=target_scope AND
   (s.managed_postgres_binding_id IS NULL OR EXISTS (
    SELECT 1 FROM managed_postgres_bindings b WHERE b.id=s.managed_postgres_binding_id
     AND b.account_id=s.account_id AND b.app_id=s.app_id AND b.scope=s.scope
     AND b.environment_key=s.key AND b.access IN ('read_write','read_only')))
 ),
 baseline AS (SELECT coalesce(jsonb_object_agg(s.key,'secret:'||s.key),'{}'::jsonb) AS refs,
  coalesce(jsonb_object_agg(s.scope||'/'||s.key,s.delivery_version),'{}'::jsonb) AS versions FROM eligible s)
 SELECT environment_runtime_base_inputs_fresh(target_app,target_scope,boundary,observed_variables,observed_secrets,false)
  AND observed_sidecar_secrets <@ observed_secrets
  AND managed.refs <@ observed_secret_refs
  AND NOT observed_secret_refs ?| suppressed.keys
  AND NOT EXISTS (SELECT 1 FROM jsonb_each_text(observed_secrets) v WHERE NOT EXISTS (
   SELECT 1 FROM eligible s WHERE v.key=s.scope||'/'||s.key AND v.value=s.delivery_version::text))
  AND (cardinality(suppressed.keys)=0 OR observed_secrets=(coalesce((SELECT jsonb_object_agg(s.scope||'/'||s.key,s.delivery_version)
   FROM eligible s WHERE EXISTS (
    SELECT 1 FROM jsonb_each_text(observed_secret_refs) r WHERE r.value='secret:'||s.key)),'{}'::jsonb)||observed_sidecar_secrets))
  AND NOT EXISTS(SELECT 1 FROM jsonb_each(managed.refs) r WHERE observed_variables ? r.key)
  AND NOT EXISTS (SELECT 1 FROM jsonb_each_text(observed_secret_refs) r WHERE
   NOT EXISTS (SELECT 1 FROM eligible s WHERE r.value='secret:'||s.key
    AND observed_secrets->>(target_scope||'/'||s.key)=s.delivery_version::text))
  AND (NOT observed_all_secrets OR
   CASE WHEN observed_secret_refs='{}'::jsonb AND managed.refs='{}'::jsonb AND cardinality(suppressed.keys)=0
    THEN observed_secrets=baseline.versions
    ELSE observed_secret_refs=((baseline.refs - suppressed.keys)||managed.refs) END)
 FROM managed,baseline,suppressed;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Legacy receipts have no sidecar delivery evidence and retain the stricter check.
CREATE OR REPLACE FUNCTION environment_runtime_inputs_fresh(target_app uuid,target_scope text,boundary timestamptz,
 observed_variables jsonb,observed_secrets jsonb,observed_all_secrets boolean,observed_secret_refs jsonb)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT environment_runtime_inputs_fresh(target_app,target_scope,boundary,observed_variables,observed_secrets,
  observed_all_secrets,observed_secret_refs,'{}'::jsonb);
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_runtime_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE; i instances%ROWTYPE;
BEGIN
 SELECT * INTO i FROM instances WHERE id=NEW.instance_id;
 IF NOT EXISTS(SELECT 1 FROM deployments WHERE id=i.deployment_id AND environment_workload_runtime IS NOT NULL) THEN RETURN NEW; END IF;
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=i.id;
 IF q.id IS NOT NULL THEN
  PERFORM s.id FROM environment_git_sources s JOIN environment_workload_graphs g ON g.source_id=s.id WHERE g.id=q.graph_id FOR UPDATE OF s;
  PERFORM a.id FROM apps a JOIN environment_workload_graphs g ON g.id=q.graph_id
   JOIN LATERAL jsonb_array_elements(g.members) m ON a.id=(m->>'app_id')::uuid ORDER BY a.id FOR UPDATE OF a;
  SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=i.id FOR UPDATE;
  PERFORM n.id FROM compute_nodes n WHERE n.id=i.node_id FOR SHARE OF n;
  PERFORM c.id FROM accounts c JOIN apps a ON a.account_id=c.id WHERE a.id=i.app_id FOR SHARE OF c;
  -- Terminal cleanup does not require the graph lock. Re-read and lock the
  -- incarnation after authority locks so a concurrent retirement wins.
  SELECT * INTO i FROM instances WHERE id=NEW.instance_id FOR UPDATE;
 END IF;
 IF q.id IS NULL OR q.phase<>'claimed' OR q.lease_until<=clock_timestamp()
  OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
  OR q.app_id<>i.app_id OR q.deployment_id<>i.deployment_id OR i.state<>'running'
  OR i.ram_mb IS DISTINCT FROM (SELECT a.ram_mb FROM apps a WHERE a.id=i.app_id)
  OR NOT EXISTS(SELECT 1 FROM compute_nodes n WHERE n.id=i.node_id AND n.active AND n.lifecycle='active')
  OR NEW.wake_id IS DISTINCT FROM i.wake_id OR NEW.scope IS DISTINCT FROM q.frozen_inputs->>'scope'
  OR NOT environment_workload_qualification_inputs_current(q.id)
  OR NOT environment_runtime_inputs_fresh(i.app_id,NEW.scope,NEW.boundary_at,NEW.variables,NEW.secret_versions,NEW.all_secrets,NEW.secret_refs,NEW.sidecar_secret_versions) THEN
  RAISE EXCEPTION 'environment runtime evidence requires its current qualification attempt and delivered inputs' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE VIEW environment_gitops_runtime_targets AS
WITH targets AS (
    SELECT s.id AS source_id, s.account_id, r.app_id, r.logical_name AS resource, e.slug AS environment_slug
    FROM environment_git_sources s
    JOIN project_environments e ON e.id = s.environment_id
    JOIN environment_gitops_resources r ON r.source_id = s.id
    JOIN apps a ON a.id = r.app_id AND a.account_id = s.account_id AND a.project_id = s.project_id
    WHERE cardinality(environment_scoped_secret_suppressions(r.app_id,e.slug))>0
       OR EXISTS (SELECT 1 FROM environment_managed_fields f WHERE f.source_id = s.id
        AND f.resource = r.logical_name AND (f.field_path LIKE 'variables/%' OR f.field_path LIKE 'secret_refs/%'))
       OR EXISTS (SELECT 1 FROM environment_gitops_runtime_effects x WHERE x.source_id = s.id
        AND x.app_id = r.app_id AND x.completed_at IS NULL)
), boundaries AS (
    SELECT t.*, greatest(
        coalesce((SELECT c.changed_at FROM app_runtime_config_changes c WHERE c.app_id = t.app_id), 'epoch'::timestamptz),
        coalesce((SELECT max(c.changed_at) FROM app_runtime_config_scope_changes c
            WHERE c.app_id = t.app_id AND c.scope IN ('default', t.environment_slug)), 'epoch'::timestamptz),
        coalesce((SELECT max(v.updated_at) FROM app_envs v JOIN environment_managed_fields f
            ON f.source_id = t.source_id AND f.resource = t.resource AND f.field_path = 'variables/' || v.key
            WHERE v.app_id = t.app_id AND v.scope = t.environment_slug), 'epoch'::timestamptz),
        coalesce((SELECT max(x.required_at) FROM environment_gitops_runtime_effects x
            WHERE x.source_id = t.source_id AND x.app_id = t.app_id AND x.completed_at IS NULL), 'epoch'::timestamptz)
    ) AS required_at FROM targets t
)
SELECT b.*,
    (SELECT count(*) FROM instances i JOIN deployments d ON d.id = i.deployment_id
        WHERE i.app_id = b.app_id AND d.scope = b.environment_slug
        AND i.state IN ('waking', 'cold_booting', 'running', 'warm', 'draining')
        AND NOT EXISTS (SELECT 1 FROM instance_runtime_config_receipts r WHERE r.instance_id = i.id AND r.wake_id = i.wake_id
            AND r.scope = b.environment_slug AND r.boundary_at >= b.required_at
            AND environment_runtime_inputs_fresh(b.app_id, r.scope, r.boundary_at, r.variables, r.secret_versions, r.all_secrets, r.secret_refs, r.sidecar_secret_versions))) AS stale_residents,
    (SELECT count(*) FROM instances i JOIN deployments d ON d.id = i.deployment_id
        WHERE i.app_id = b.app_id AND d.scope = b.environment_slug
        AND i.state IN ('waking', 'cold_booting', 'migrating')) AS starting_residents,
    (SELECT count(*) FROM snapshots p JOIN deployments d ON d.id = p.deployment_id
        WHERE d.app_id = b.app_id AND d.scope = b.environment_slug
        AND NOT p.stale AND NOT p.delete_pending
        AND NOT EXISTS (SELECT 1 FROM snapshot_runtime_config_receipts r WHERE r.snapshot_id = p.id
            AND r.scope = b.environment_slug AND r.boundary_at >= b.required_at
            AND environment_runtime_inputs_fresh(b.app_id, r.scope, r.boundary_at, r.variables, r.secret_versions, r.all_secrets, r.secret_refs, r.sidecar_secret_versions))) AS stale_snapshots
FROM boundaries b;
-- +goose StatementEnd

-- +goose Down
-- Preserve captured evidence and the primary suppression fence on binary rollback.
SELECT 1;
