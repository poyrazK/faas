-- name: WriteBuildDependencyGate :exec
INSERT INTO deployment_build_dependency_gates(deployment_id,progress,check_after)
VALUES ($1,$2,$3)
ON CONFLICT (deployment_id) DO UPDATE SET progress=EXCLUDED.progress,check_after=EXCLUDED.check_after;

-- name: ReadBuildDependencyGate :one
SELECT progress FROM deployment_build_dependency_gates WHERE deployment_id=$1 FOR UPDATE;

-- name: ProjectBuildDependencyGate :exec
UPDATE deployments SET stage_state=jsonb_set(stage_state,'{build_dependency_gate}',$2::jsonb,true) WHERE id=$1;

-- name: ReadBuildDependencyClaim :one
SELECT b.started_at FROM builds b
WHERE b.id=$1 AND b.deployment_id=$2 AND b.status='running'
FOR UPDATE;

-- name: DeferBuildDependencyClaim :exec
UPDATE builds SET status='queued',started_at=NULL WHERE id=$1 AND status='running';

-- name: ReadBuildDependencyTarget :one
SELECT d.status,d.traffic_percent,coalesce(d.parked_reason,'')::text AS parked_reason,
 NOT EXISTS (SELECT 1 FROM deployments newer WHERE newer.app_id=d.app_id AND newer.scope=d.scope AND newer.revision>d.revision)::boolean AS latest
FROM deployments d JOIN apps a ON a.id=d.app_id
JOIN deployments candidate ON candidate.id=sqlc.arg(candidate_id)::uuid
JOIN apps owner ON owner.id=candidate.app_id
WHERE d.id=sqlc.arg(dependency_id)::uuid AND a.id=sqlc.arg(dependency_app_id)::uuid
 AND a.account_id=owner.account_id AND a.project_id=owner.project_id AND owner.project_id IS NOT NULL
 AND a.id<>owner.id AND a.workload_name=sqlc.arg(service)::text AND a.status<>'deleted'
 AND coalesce(a.preview_of_slug,'')='' AND coalesce(owner.preview_of_slug,'')=''
 AND a.workload_class<>'job' AND coalesce(a.manifest->>'execution_mode','')<>'job'
 AND d.scope=candidate.scope AND d.environment_workload_runtime IS NULL
FOR SHARE OF d;
