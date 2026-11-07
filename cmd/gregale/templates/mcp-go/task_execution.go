package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrRetryableMCPTask opts a handler failure into the configured retry policy.
// Wrap it only when repeating the handler's external effects is safe.
var ErrRetryableMCPTask = errors.New("retryable MCP task failure")

type mcpTaskExecutionPolicy struct {
	maxAttempts, maxRunning, maxRunningPerOwner int
	retryBaseDelayMS, retryMaxDelayMS           int
}

func resolveTaskExecutionPolicy(settings mcpTaskConfig) (mcpTaskExecutionPolicy, error) {
	p := mcpTaskExecutionPolicy{settings.MaxAttempts, settings.MaxRunning, settings.MaxRunningPerOwner, settings.RetryBaseDelayMS, settings.RetryMaxDelayMS}
	for _, setting := range []struct {
		value    *int
		fallback int
	}{
		{&p.maxAttempts, 3}, {&p.maxRunning, 16}, {&p.maxRunningPerOwner, 4},
		{&p.retryBaseDelayMS, 1000}, {&p.retryMaxDelayMS, 60000},
	} {
		if *setting.value == 0 {
			*setting.value = setting.fallback
		}
	}
	if p.maxAttempts < 1 || p.maxAttempts > 10 || p.maxRunning < 1 || p.maxRunningPerOwner < 1 || p.maxRunningPerOwner > p.maxRunning ||
		p.retryBaseDelayMS < 100 || p.retryMaxDelayMS < p.retryBaseDelayMS || p.retryMaxDelayMS > 86400000 {
		return p, errors.New("MCP task execution limits or retry policy are outside their supported ranges")
	}
	return p, nil
}

func (runtime *mcpTaskRuntime) lockExecution(ctx context.Context, tx pgx.Tx) error {
	// Node, Go and Python share this lock, including lease renewals. The
	// subsequent statement gets a fresh READ COMMITTED snapshot after waiting.
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, taskTable+":execution:"+runtime.namespace)
	return err
}

func (runtime *mcpTaskRuntime) claim(ctx context.Context) (*durableMCPTask, error) {
	if len(runtime.taskExecutors) == 0 {
		return nil, nil
	}
	tx, err := runtime.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := runtime.lockExecution(ctx, tx); err != nil {
		return nil, err
	}
	// Settle cancellation before recovering an expired lease. A reduced budget
	// also settles queued retries, but input and drain resumes keep their attempt.
	if _, err := tx.Exec(ctx, `UPDATE gregale_mcp_tasks SET status=CASE WHEN cancel_requested_at IS NOT NULL THEN 'cancelled' ELSE 'failed' END,
		updated_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL,next_attempt_at=NULL,resume_pending=false
		WHERE namespace=$1 AND (cancel_requested_at IS NOT NULL OR attempt_count >= $2)
		AND ((status='running' AND lease_expires_at<=clock_timestamp())
		     OR (status='queued' AND (cancel_requested_at IS NOT NULL OR NOT resume_pending)))`, runtime.namespace, runtime.execution.maxAttempts); err != nil {
		return nil, err
	}
	params := []any{runtime.namespace, runtime.execution.maxAttempts, runtime.execution.maxRunning, runtime.execution.maxRunningPerOwner}
	filters := make([]string, 0, len(runtime.taskExecutors))
	for handler := range runtime.taskExecutors {
		filters = append(filters, fmt.Sprintf("(queued.tool_name=$%d AND queued.handler_version=$%d)", len(params)+1, len(params)+2))
		params = append(params, handler.toolName, handler.version)
	}
	query := fmt.Sprintf(`WITH candidate AS (
		SELECT queued.namespace,queued.task_id FROM gregale_mcp_tasks AS queued
		WHERE queued.namespace=$1 AND queued.expires_at>clock_timestamp()
		AND queued.cancel_requested_at IS NULL
		AND (queued.attempt_count<$2 OR (queued.status='queued' AND queued.resume_pending))
		AND (%s)
		AND ((queued.status='queued' AND (queued.next_attempt_at IS NULL OR queued.next_attempt_at<=clock_timestamp()))
		     OR (queued.status='running' AND queued.lease_expires_at<=clock_timestamp()))
		AND (SELECT COUNT(*) FROM gregale_mcp_tasks AS live WHERE live.namespace=$1 AND live.status='running'
		     AND live.expires_at>clock_timestamp() AND live.lease_expires_at>clock_timestamp())<$3
		AND (SELECT COUNT(*) FROM gregale_mcp_tasks AS live WHERE live.namespace=$1 AND COALESCE((SELECT owner_group FROM gregale_mcp_task_owners WHERE namespace=$1 AND owner_hash=live.owner_hash),''::bytea)=COALESCE((SELECT owner_group FROM gregale_mcp_task_owners WHERE namespace=$1 AND owner_hash=queued.owner_hash),''::bytea)
		     AND live.status='running' AND live.expires_at>clock_timestamp() AND live.lease_expires_at>clock_timestamp())<$4
		ORDER BY queued.created_at,queued.task_id FOR UPDATE OF queued SKIP LOCKED LIMIT 1)
		UPDATE gregale_mcp_tasks AS task SET status='running',attempt_count=task.attempt_count+CASE WHEN task.resume_pending THEN 0 ELSE 1 END,
		resume_pending=false,next_attempt_at=NULL,lease_token=gen_random_uuid(),lease_expires_at=clock_timestamp()+interval '30 seconds',updated_at=clock_timestamp()
		FROM candidate WHERE task.namespace=candidate.namespace AND task.task_id=candidate.task_id AND task.cancel_requested_at IS NULL
		RETURNING task.task_id::text,task.tool_name,task.handler_version,task.status,task.created_at,task.updated_at,task.expires_at,task.attempt_count,task.lease_token::text,task.cancel_requested_at,task.arguments_encrypted,task.input_methods,task.result_encrypted,task.error_encrypted,task.input_state_encrypted,task.output_schema`, strings.Join(filters, " OR "))
	var task durableMCPTask
	var args, result, taskError, inputs []byte
	task.leaseDeadline = time.Now().Add(mcpTaskLeaseDuration)
	err = tx.QueryRow(ctx, query, params...).Scan(&task.ID, &task.ToolName, &task.HandlerVersion, &task.Status, &task.CreatedAt, &task.UpdatedAt, &task.ExpiresAt, &task.AttemptCount, &task.LeaseToken, &task.CancelAt, &args, &task.InputMethods, &result, &taskError, &inputs, &task.OutputSchema)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tx.Commit(ctx)
	}
	if err != nil {
		return nil, err
	}
	// Decryption failure must roll back the claim and its attempt increment.
	if err := runtime.decrypt(args, task.ID, "arguments", &task.Arguments); err != nil {
		return nil, err
	}
	for _, field := range []struct {
		name    string
		payload []byte
	}{{"result", result}, {"error", taskError}, {"input-state", inputs}} {
		if field.payload != nil {
			if _, err := runtime.decryptPayload(field.payload, task.ID, field.name); err != nil {
				return nil, err
			}
		}
	}
	if task.Arguments == nil {
		return nil, errors.New("MCP task arguments must be an object")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &task, nil
}

func (runtime *mcpTaskRuntime) heartbeatTask(ctx context.Context, task *durableMCPTask) (bool, error) {
	tx, err := runtime.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := runtime.lockExecution(ctx, tx); err != nil {
		return false, err
	}
	var cancelled bool
	err = tx.QueryRow(ctx, taskLeaseInstantSQL+`UPDATE gregale_mcp_tasks SET lease_expires_at=instant.now+interval '30 seconds'
		FROM instant
		WHERE namespace=$1 AND task_id=$2::uuid AND lease_token=$3::uuid AND status='running'
		AND lease_expires_at>instant.now AND expires_at>instant.now
		RETURNING cancel_requested_at IS NOT NULL`, runtime.namespace, task.ID, task.LeaseToken).Scan(&cancelled)
	if err != nil {
		return false, err
	}
	return cancelled, tx.Commit(ctx)
}

func (p mcpTaskExecutionPolicy) retryDelay(attempt int) time.Duration {
	ceiling := min(p.retryMaxDelayMS, p.retryBaseDelayMS*(1<<max(0, min(attempt-1, 10))))
	lower := (ceiling + 1) / 2
	return time.Duration(lower+rand.IntN(ceiling-lower+1)) * time.Millisecond
}

const taskLeaseInstantSQL = `WITH lease AS MATERIALIZED (
 SELECT namespace,task_id FROM gregale_mcp_tasks
 WHERE namespace=$1 AND task_id=$2::uuid AND lease_token=$3::uuid AND status='running' FOR UPDATE
), instant AS MATERIALIZED (SELECT clock_timestamp() AS now FROM lease)
`

func (runtime *mcpTaskRuntime) failTaskWithRetry(ctx context.Context, task *durableMCPTask, retryable bool) {
	encrypted, err := runtime.encrypt(map[string]any{"code": -32603, "message": "Task execution failed"}, task.ID, "error", 1024*1024)
	if err != nil {
		return
	}
	delayMS := runtime.execution.retryDelay(task.AttemptCount).Milliseconds()
	_, _ = runtime.pool.Exec(ctx, taskLeaseInstantSQL+`UPDATE gregale_mcp_tasks SET status=CASE WHEN cancel_requested_at IS NOT NULL THEN 'cancelled'
		WHEN $5 AND attempt_count<$6 AND instant.now+($7::bigint*interval '1 millisecond')<expires_at THEN 'queued' ELSE 'failed' END,
		next_attempt_at=CASE WHEN cancel_requested_at IS NULL AND $5 AND attempt_count<$6 AND instant.now+($7::bigint*interval '1 millisecond')<expires_at
		THEN instant.now+($7::bigint*interval '1 millisecond') ELSE NULL END,
		error_encrypted=CASE WHEN cancel_requested_at IS NULL THEN $4::bytea ELSE NULL END,result_encrypted=NULL,
		updated_at=instant.now,resume_pending=false,lease_token=NULL,lease_expires_at=NULL FROM instant
		WHERE namespace=$1 AND task_id=$2::uuid AND lease_token=$3::uuid AND status='running'
		AND lease_expires_at>instant.now AND expires_at>instant.now`, runtime.namespace, task.ID, task.LeaseToken, encrypted, retryable, runtime.execution.maxAttempts, delayMS)
}
