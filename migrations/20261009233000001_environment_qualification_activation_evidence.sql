-- +goose Up
-- +goose StatementBegin
-- Receipt writers continue to use environment_workload_qualification_inputs_current,
-- which requires held snapshotting candidates. Read-side evidence may also
-- recognize the same immutable candidate after graph activation, but only while
-- that exact deployment remains in the active environment release set.
CREATE OR REPLACE FUNCTION environment_workload_qualification_evidence_current(request_id uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM environment_workload_qualification_requests target
  JOIN environment_workload_graphs g ON g.id=target.graph_id JOIN active_environment_git_sources s ON s.id=g.source_id
  JOIN project_environments e ON e.id=g.environment_id JOIN accounts c ON c.id=s.account_id
  WHERE target.id=request_id AND g.phase='prepared' AND s.mode='enforce' AND NOT s.suspended
   AND c.status='active' AND c.abuse_hold_at IS NULL
   AND g.generation=s.generation AND g.intent_version=s.intent_version AND g.revision_id=s.approved_revision_id
   AND g.environment_id=s.environment_id AND e.account_id=s.account_id AND e.project_id=s.project_id
   AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(g.members) m LEFT JOIN apps a ON a.id=(m->>'app_id')::uuid
    WHERE a.id IS NULL OR a.status NOT IN ('active','evicted_cold') OR a.account_id<>s.account_id OR a.project_id<>s.project_id
     OR m->'retained_deployments' IS DISTINCT FROM (SELECT coalesce(jsonb_agg(d.id::text ORDER BY d.id),'[]'::jsonb)
      FROM deployments d WHERE d.app_id=a.id AND d.scope=e.slug AND d.status='live'
       AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(g.members) candidate
        WHERE candidate->>'candidate_deployment_id'=d.id::text)))
   AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(g.members) m
    LEFT JOIN environment_workload_qualification_requests q ON q.graph_id=g.id AND q.resource=m->>'resource'
     AND q.app_id=(m->>'app_id')::uuid AND q.deployment_id=(m->>'candidate_deployment_id')::uuid
    LEFT JOIN deployments d ON d.id=q.deployment_id LEFT JOIN apps a ON a.id=q.app_id
    WHERE m ? 'candidate_deployment_id' AND (q.id IS NULL
     OR NOT ((d.status='snapshotting' AND d.environment_workload_held)
      OR (d.status='live' AND NOT d.environment_workload_held AND EXISTS(
       SELECT 1 FROM project_release_sets release
       JOIN project_release_members member ON member.release_id=release.id
       WHERE release.project_id=s.project_id AND release.environment_slug=e.slug AND release.active
        AND member.app_id=d.app_id AND member.deployment_id=d.id)))
     OR coalesce(d.rootfs_bytes,0)<=0 OR q.artifact IS DISTINCT FROM environment_workload_artifact(d)
     OR q.frozen_inputs IS DISTINCT FROM d.environment_workload_runtime OR d.scope IS DISTINCT FROM e.slug OR d.app_id IS DISTINCT FROM a.id
     OR jsonb_strip_nulls(q.frozen_inputs->'baseline') IS DISTINCT FROM jsonb_strip_nulls(a.manifest)
     OR q.frozen_inputs->>'start_command' IS DISTINCT FROM coalesce(a.start_command,'') OR q.frozen_inputs->>'app_type' IS DISTINCT FROM a.type
     OR q.frozen_inputs->>'runtime_base' IS DISTINCT FROM coalesce(a.runtime,'') OR q.frozen_inputs->>'workload_class' IS DISTINCT FROM a.workload_class
     OR q.frozen_inputs ? 'deployment_inputs' AND EXISTS(SELECT 1 FROM deployments original
      WHERE original.app_id=a.id AND original.scope=e.slug AND original.status='live'
       AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(g.members) candidate
        WHERE candidate->>'candidate_deployment_id'=original.id::text)
       AND environment_workload_deployment_inputs(original) IS DISTINCT FROM q.frozen_inputs->'deployment_inputs'))));
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS environment_workload_qualification_evidence_current(uuid);
