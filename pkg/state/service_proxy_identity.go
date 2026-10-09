package state

import (
	"context"
	"fmt"
	"net/netip"
)

// LiveInstancesByHostIP provides a fresh, indexed source-identity read for
// the guest service proxy. Managed scheduled Jobs receive an app/deployment
// identity only while a claimed scheduled task and its active GitOps release
// prove the exact frozen Job workload. VM slots can be recycled immediately,
// so callers must not authenticate a graph hop from a time-based HostIP cache.
func (s *PgStore) LiveInstancesByHostIP(ctx context.Context, nodeID, hostIP string) ([]Instance, error) {
	address, err := netip.ParseAddr(hostIP)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	query := `select distinct coalesce(i.app_id::text, managed_job.app_id),
		coalesce(i.deployment_id::text, managed_job.deployment_id)
		from instances i
		left join lateral (
			select intent.app_id::text as app_id, member.deployment_id::text as deployment_id
			from job_tasks task
			join job_runs run on run.id=task.run_id and run.job_id=i.job_id
			join schedule_occurrences occurrence on occurrence.id=run.occurrence_id
			join jobs managed on managed.id=i.job_id
			join app_environment_workload_intents intent on intent.job_id=managed.id
			join active_environment_git_sources source on source.account_id=intent.account_id
				and source.environment_id=intent.environment_id
			join environment_gitops_resources resource on resource.source_id=source.id and resource.app_id=intent.app_id
			join project_environments environment on environment.id=source.environment_id
			join project_release_sets release on release.active and release.project_id=source.project_id
				and release.environment_slug=environment.slug
			join project_release_members member on member.release_id=release.id and member.app_id=intent.app_id
			join deployments deployment on deployment.id=member.deployment_id and deployment.app_id=member.app_id
			where i.app_id is null and i.kind='job_task' and i.job_id is not null
			  and task.instance_id=i.id and task.status='claimed' and task.started_at is not null
			  and task.lease_token is not null and task.lease_expires_at>clock_timestamp()
			  and intent.account_id=managed.account_id and intent.account_id=source.account_id
			  and intent.environment_id=source.environment_id
			  and run.account_id=managed.account_id and occurrence.account_id=managed.account_id
			  and run.trigger_kind='scheduled' and run.aggregate_status='running' and run.started_at is not null
			  and occurrence.job_id=managed.id and occurrence.job_run_id=run.id
			  and occurrence.schedule_revision=managed.schedule_revision and occurrence.status='running'
			  and occurrence.created_at>=release.created_at and occurrence.started_at>=release.created_at
			  and run.created_at>=release.created_at and run.started_at>=release.created_at
			  and managed.kind='recurring' and managed.status='active'
			  and intent.schedule is not null and intent.source->>'kind'='image'
			  and managed.image_ref=intent.source->>'image'
			  and managed.image_materialization_status='ready' and coalesce(managed.image_storage_key,'')<>''
			  and managed.image_resolved_digest=substring(intent.source->>'image' FROM '@(sha256:[a-f0-9]{64})$')
			  and managed.cron_schedule=intent.schedule->>'cron'
			  and managed.cron_timezone=intent.schedule->>'timezone'
			  and managed.schedule_policy is not distinct from NULLIF(intent.schedule->'schedule_policy','null'::jsonb)
			  and managed.failure_rules is not distinct from NULLIF(intent.schedule->'failure_rules','null'::jsonb)
			  and source.mode='enforce' and not source.suspended and source.approved_revision_id is not null
			  and source.generation::text=deployment.environment_workload_runtime->>'generation'
			  and source.intent_version::text=deployment.environment_workload_runtime->>'intent_version'
			  and source.approved_revision_id::text=deployment.environment_workload_runtime->>'revision_id'
			  and source.id::text=deployment.environment_workload_runtime->>'source_id'
			  and source.environment_id::text=deployment.environment_workload_runtime->>'environment_id'
			  and deployment.environment_workload_runtime->>'app_id'=intent.app_id::text
			  and deployment.environment_workload_runtime->>'resource'=resource.logical_name
			  and resource.logical_name like 'workload/%'
			  and deployment.environment_workload_runtime->>'workload_class'='job'
			  and deployment.environment_workload_runtime->'source'=intent.source
			  and deployment.environment_workload_runtime->'schedule'=intent.schedule
			  and deployment.environment_workload_runtime->'runtime'=intent.runtime
			  and deployment.environment_workload_runtime->'service_bindings'=intent.service_bindings
			  and coalesce(deployment.environment_workload_runtime->'variables','{}'::jsonb)=coalesce(intent.variables,'{}'::jsonb)
			  and case when jsonb_typeof(deployment.environment_workload_runtime->'service_bindings')='object'
				then deployment.environment_workload_runtime->'service_bindings'<>'{}'::jsonb else false end
			  and deployment.status='live' and not deployment.environment_workload_held
			  and deployment.environment_workload_runtime->>'scope'=environment.slug
			  and deployment.scope=environment.slug and release.account_id=source.account_id
			  and release.project_id=source.project_id
			  and run.image_ref_snapshot=managed.image_ref
			  and run.image_resolved_digest_snapshot=managed.image_resolved_digest
			  and run.image_storage_key_snapshot=managed.image_storage_key
			  and run.command=managed.command and run.ram_mb_snapshot=managed.ram_mb
			  and run.retry_max=managed.retry_max and run.task_timeout_s=managed.task_timeout_s
			  and run.parallelism=managed.max_parallelism and run.env_overrides=managed.env_overrides
			  and run.effective_env_snapshot=managed.env_overrides || run.env_overrides
			  and run.failure_rules is not distinct from managed.failure_rules and run.tasks=1
			  and run.execution_class='standard' and run.failure_policy='continue'
			  and run.input_manifest_version=0 and coalesce(run.input_digest,'')=''
			  and run.input_manifest_uri is null and run.input_manifest_sha256 is null
			  and managed.max_parallelism=1 and managed.retry_max=0 and cardinality(managed.command)=0
			  and managed.env_overrides=coalesce(deployment.environment_workload_runtime->'variables','{}'::jsonb)
			  and coalesce(deployment.environment_workload_runtime->'queue_bindings','{}'::jsonb)='{}'::jsonb
		) managed_job on true
		where i.host_ip = $1::inet and i.state in ('running', 'draining')`
	args := []any{address.String()}
	if nodeID != "" {
		query += ` and exists (select 1 from compute_nodes n where n.id = i.node_id and n.name = $2)`
		args = append(args, nodeID)
	}
	// Two distinct identities suffice to detect ambiguity; duplicates from
	// the same deployment are collapsed by DISTINCT.
	query += ` limit 2`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("state: lookup live instance by host IP: %w", err)
	}
	defer rows.Close()
	var instances []Instance
	for rows.Next() {
		var instance Instance
		if err := rows.Scan(&instance.AppID, &instance.DeploymentID); err != nil {
			return nil, fmt.Errorf("state: scan live instance by host IP: %w", err)
		}
		instance.HostIP = address.String()
		instance.NodeID = nodeID
		instance.State = string(StateRunning)
		instances = append(instances, instance)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate live instances by host IP: %w", err)
	}
	return instances, nil
}
