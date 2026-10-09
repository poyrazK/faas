package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func environmentGitOpsJobName(environmentID, appID string) string {
	sum := sha256.Sum256([]byte(environmentID + "#" + appID))
	return "git-" + hex.EncodeToString(sum[:16])
}

func environmentGitOpsScheduledJobsReadyTx(ctx context.Context, tx sqlc.DBTX, graph EnvironmentWorkloadGraph, status string, lock bool) (bool, error) {
	for _, member := range graph.Members {
		if member.ExecutionMode != api.ExecutionModeJob || !member.ScheduleConfigured {
			continue
		}
		if !canonicalStateUUID(member.JobID) || graph.ResourceIDs[member.Resource] != member.AppID {
			return false, nil
		}
		query := `SELECT i.account_id::text,i.app_id::text,i.environment_id::text,i.job_id::text,i.source,i.schedule,i.service_bindings,i.variables
			FROM app_environment_workload_intents i
			WHERE i.account_id=(SELECT account_id FROM active_environment_git_sources WHERE id=$1::uuid)
				AND i.app_id=$2::uuid AND i.environment_id=$3::uuid AND i.job_id=$4::uuid`
		if lock {
			query += ` FOR UPDATE OF i`
		}
		row := tx.QueryRow(ctx, query, graph.SourceID, member.AppID, graph.EnvironmentID, member.JobID)
		var intent EnvironmentWorkloadIntent
		var sourceRaw, scheduleRaw, serviceBindingsRaw, variablesRaw []byte
		err := row.Scan(&intent.AccountID, &intent.AppID, &intent.EnvironmentID, &intent.JobID, &sourceRaw, &scheduleRaw, &serviceBindingsRaw, &variablesRaw)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, mapErr(err)
		}
		jobQuery := `SELECT ` + jobSelectCols + ` FROM jobs WHERE id=$1::uuid`
		if lock {
			jobQuery += ` FOR UPDATE`
		}
		job, err := scanJob(tx.QueryRow(ctx, jobQuery, member.JobID))
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, mapErr(err)
		}
		if json.Unmarshal(sourceRaw, &intent.Source) != nil || json.Unmarshal(scheduleRaw, &intent.Schedule) != nil ||
			json.Unmarshal(serviceBindingsRaw, &intent.ServiceBindings) != nil || json.Unmarshal(variablesRaw, &intent.Variables) != nil ||
			!environmentGitOpsScheduledJobMatches(graph, member, intent, job, status) {
			return false, nil
		}
	}
	return true, nil
}

func activateEnvironmentGitOpsScheduledJobsTx(ctx context.Context, tx pgx.Tx, graph EnvironmentWorkloadGraph) error {
	for _, member := range graph.Members {
		if member.ExecutionMode != api.ExecutionModeJob || !member.ScheduleConfigured {
			continue
		}
		tag, err := tx.Exec(ctx, `UPDATE jobs SET status='active',updated_at=clock_timestamp()
			WHERE id=$1::uuid AND status='paused'`, member.JobID)
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() != 1 {
			return ErrConflict
		}
	}
	return nil
}

func environmentGitOpsScheduledJobAcksTx(ctx context.Context, tx sqlc.DBTX, graphID, releaseID string) ([]EnvironmentWorkloadServingScheduledJobAck, error) {
	rows, err := tx.Query(ctx, `SELECT app_id,job_id,job_run_id,occurrence_id,acknowledged_at,service_bindings
		FROM public.environment_workload_scheduled_job_acks($1::uuid,$2::uuid) ORDER BY app_id`, graphID, releaseID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	acks := make([]EnvironmentWorkloadServingScheduledJobAck, 0)
	for rows.Next() {
		var ack EnvironmentWorkloadServingScheduledJobAck
		var serviceBindingsRaw []byte
		if err := rows.Scan(&ack.AppID, &ack.JobID, &ack.JobRunID, &ack.OccurrenceID, &ack.AcknowledgedAt, &serviceBindingsRaw); err != nil {
			return nil, mapErr(err)
		}
		if err := json.Unmarshal(serviceBindingsRaw, &ack.ServiceBindings); err != nil {
			return nil, mapErr(err)
		}
		acks = append(acks, ack)
	}
	return acks, mapErr(rows.Err())
}

// syncEnvironmentGitOpsJobTx gives a scheduled workload one durable Gregale
// Job identity. It always creates or updates the Job paused; qualification and
// activation are a later, separate transition.
func syncEnvironmentGitOpsJobTx(ctx context.Context, tx pgx.Tx, source EnvironmentGitSource, app gitOpsIntentApp,
	intent EnvironmentWorkloadIntent, workload api.EnvironmentWorkload, plan api.Plan) (EnvironmentWorkloadIntent, error) {
	if maps.Equal(app.Variables, workload.Variables) {
		intent.Variables = maps.Clone(workload.Variables)
	}
	if intent.Variables == nil {
		intent.Variables = map[string]string{}
	}
	variables, err := json.Marshal(intent.Variables)
	if err != nil {
		return intent, ErrInvalidArgument
	}
	if intent.Schedule == nil {
		if intent.JobID == "" {
			return intent, nil
		}
		if _, err := tx.Exec(ctx, `UPDATE jobs SET status='deleted',kind='batch',cron_schedule=NULL,
			cron_timezone='UTC',schedule_policy=NULL,failure_rules=NULL,updated_at=now()
			WHERE id=$1::uuid AND account_id=$2::uuid AND status<>'deleted'`, intent.JobID, source.AccountID); err != nil {
			return intent, mapErr(err)
		}
		intent.JobID = ""
		return intent, nil
	}
	if reason := environmentGitOpsScheduledJobUnsupported(app, intent, workload, plan); reason != "" {
		return intent, fmt.Errorf("%w: %s", ErrConflict, reason)
	}
	image := intent.Source
	if image == nil {
		image, _, _ = observedWorkloadSource(app, "")
	}
	if image == nil || image.Kind != "image" {
		return intent, ErrConflict
	}
	index := plan.PlanIndex()
	if !plan.JobsAllowed() || api.JobMaxPerAccount[index] <= 0 || api.JobRAMMB[index] <= 0 || api.JobTaskTimeoutSec[index] <= 0 || api.JobMaxParallelismPerRun[index] <= 0 {
		return intent, ErrConflict
	}
	jobName := environmentGitOpsJobName(source.EnvironmentID, app.ID)
	envOverrides := json.RawMessage(variables)
	if intent.JobID == "" {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE account_id=$1::uuid AND status<>'deleted'`, source.AccountID).Scan(&count); err != nil {
			return intent, mapErr(err)
		}
		limit := api.JobMaxPerAccount[index]
		if count >= limit {
			return intent, &JobQuotaError{Scope: JobQuotaScopePerAccount, Limit: limit, Observed: count}
		}
		row := tx.QueryRow(ctx, `INSERT INTO jobs(account_id,kind,name,image_ref,ram_mb,task_timeout_s,max_parallelism,
			retry_max,env_overrides,status,command,cron_schedule,cron_timezone,schedule_policy,failure_rules)
			VALUES($1::uuid,'recurring',$2,$3,$4,$5,1,0,$6::jsonb,'paused','{}'::text[],$7,$8,$9::jsonb,$10::jsonb)
			RETURNING `+jobSelectCols,
			source.AccountID, jobName, image.Image, api.JobRAMMB[index], api.JobTaskTimeoutSec[index], []byte(envOverrides),
			intent.Schedule.Cron, intent.Schedule.Timezone, policyJSON(intent.Schedule.SchedulePolicy), policyJSON(intent.Schedule.FailureRules))
		created, err := scanJob(row)
		if err != nil {
			return intent, mapErr(err)
		}
		intent.JobID = created.ID
		return intent, nil
	}

	var activeTasks bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM job_runs r JOIN job_tasks t ON t.run_id=r.id
		WHERE r.job_id=$1::uuid AND t.status IN ('queued','claimed'))`, intent.JobID).Scan(&activeTasks); err != nil {
		return intent, mapErr(err)
	}
	if activeTasks {
		return intent, ErrConflict
	}
	row := tx.QueryRow(ctx, `UPDATE jobs SET
		kind='recurring',image_resolved_digest=CASE WHEN image_ref IS DISTINCT FROM $3 THEN NULL ELSE image_resolved_digest END,
		image_storage_key=CASE WHEN image_ref IS DISTINCT FROM $3 THEN NULL ELSE image_storage_key END,
		image_materialization_status=CASE WHEN image_ref IS DISTINCT FROM $3 THEN 'pending' ELSE image_materialization_status END,
		image_materialization_error=CASE WHEN image_ref IS DISTINCT FROM $3 THEN NULL ELSE image_materialization_error END,
		image_materialized_at=CASE WHEN image_ref IS DISTINCT FROM $3 THEN NULL ELSE image_materialized_at END,
		image_materialization_attempts=CASE WHEN image_ref IS DISTINCT FROM $3 THEN 0 ELSE image_materialization_attempts END,
		image_materialization_next_attempt_at=CASE WHEN image_ref IS DISTINCT FROM $3 THEN NULL ELSE image_materialization_next_attempt_at END,
		image_materialization_lease_owner=CASE WHEN image_ref IS DISTINCT FROM $3 THEN NULL ELSE image_materialization_lease_owner END,
		image_materialization_lease_until=CASE WHEN image_ref IS DISTINCT FROM $3 THEN NULL ELSE image_materialization_lease_until END,
		name=$2,image_ref=$3,ram_mb=$4,task_timeout_s=$5,max_parallelism=1,retry_max=0,
		env_overrides=$6::jsonb,status='paused',command='{}'::text[],cron_schedule=$7,cron_timezone=$8,
		schedule_policy=$9::jsonb,failure_rules=$10::jsonb,updated_at=now()
		WHERE id=$1::uuid AND account_id=$11::uuid AND status<>'deleted'
		RETURNING `+jobSelectCols,
		intent.JobID, jobName, image.Image, api.JobRAMMB[index], api.JobTaskTimeoutSec[index], []byte(envOverrides),
		intent.Schedule.Cron, intent.Schedule.Timezone, policyJSON(intent.Schedule.SchedulePolicy),
		policyJSON(intent.Schedule.FailureRules), source.AccountID)
	if _, err := scanJob(row); err != nil {
		return intent, mapErr(err)
	}
	return intent, nil
}
