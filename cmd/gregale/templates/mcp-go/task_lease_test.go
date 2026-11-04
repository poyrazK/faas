package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTaskWorkerWritesRequireLiveLeaseAndTTL(t *testing.T) {
	for _, operation := range []string{"complete", "cancel", "input", "retry"} {
		for _, condition := range []string{"live", "lease-expired", "ttl-expired", "replaced"} {
			t.Run(operation+"/"+condition, func(t *testing.T) {
				runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
				id := insertExecutionTestTask(t, runtime, ctx, "alice")
				if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET input_methods=ARRAY['elicitation/create:form'] WHERE task_id=$1`, id); err != nil {
					t.Fatal(err)
				}
				task, err := runtime.claim(ctx)
				if err != nil || task == nil {
					t.Fatalf("claim: %v", err)
				}
				patch := map[string]string{"lease-expired": "lease_expires_at=clock_timestamp()-interval '1 second'", "ttl-expired": "created_at=clock_timestamp()-interval '1 hour',expires_at=clock_timestamp()-interval '1 second'", "replaced": "lease_token=gen_random_uuid()"}[condition]
				if patch != "" {
					if _, err := runtime.pool.Exec(ctx, "UPDATE gregale_mcp_tasks SET "+patch+" WHERE task_id=$1", id); err != nil {
						t.Fatal(err)
					}
				}
				switch operation {
				case "complete":
					runtime.taskExecutors[mcpTaskHandlerKey{"build_report", "2"}] = func(context.Context, map[string]any, *MCPTaskExecutionContext) (map[string]any, error) {
						return map[string]any{"content": []any{}}, nil
					}
					runtime.runTask(ctx, task)
				case "cancel":
					runtime.finishCancelled(ctx, task)
				case "retry":
					runtime.failTaskWithRetry(ctx, task, true)
				case "input":
					_, _, err := runtime.requestTaskInputs(ctx, task, map[string]any{"approve": map[string]any{"method": "elicitation/create", "params": map[string]any{"mode": "form", "message": "Approve?"}}})
					if condition == "live" && err != nil {
						t.Fatal(err)
					}
					if condition != "live" && !errors.Is(err, errTaskInputLeaseLost) {
						t.Fatalf("expired input lease accepted: %v", err)
					}
				}
				var status string
				var resultEmpty, inputsEmpty bool
				if err := runtime.pool.QueryRow(ctx, `SELECT status,result_encrypted IS NULL,input_state_encrypted IS NULL FROM gregale_mcp_tasks WHERE task_id=$1`, id).Scan(&status, &resultEmpty, &inputsEmpty); err != nil {
					t.Fatal(err)
				}
				want := "running"
				if condition == "live" {
					want = map[string]string{"complete": "completed", "cancel": "cancelled", "input": "input_required", "retry": "queued"}[operation]
				}
				if status != want || condition != "live" && (!resultEmpty || !inputsEmpty) {
					t.Fatalf("write after lost lease: status=%s want=%s resultEmpty=%v inputsEmpty=%v", status, want, resultEmpty, inputsEmpty)
				}
			})
		}
	}
}

func TestTaskCompletionRechecksLeaseAfterWaitingForRowLock(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
	id := insertExecutionTestTask(t, runtime, ctx, "alice")
	task, err := runtime.claim(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET lease_expires_at=clock_timestamp()+interval '1 second' WHERE task_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	lock, err := runtime.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(ctx) }()
	if _, err := lock.Exec(ctx, `SELECT 1 FROM gregale_mcp_tasks WHERE task_id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	runtime.taskExecutors[mcpTaskHandlerKey{"build_report", "2"}] = func(context.Context, map[string]any, *MCPTaskExecutionContext) (map[string]any, error) {
		return map[string]any{"content": []any{}}, nil
	}
	done := make(chan struct{})
	go func() { runtime.runTask(ctx, task); close(done) }()
	for {
		var waiting bool
		if err := runtime.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'WITH lease AS MATERIALIZED%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(5 * time.Millisecond)
	}
	for {
		var expired bool
		if err := runtime.pool.QueryRow(ctx, `SELECT lease_expires_at<=clock_timestamp() FROM gregale_mcp_tasks WHERE task_id=$1`, id).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var status string
	if err := runtime.pool.QueryRow(ctx, `SELECT status FROM gregale_mcp_tasks WHERE task_id=$1`, id).Scan(&status); err != nil || status != "running" {
		t.Fatalf("expired while blocked but completion committed: %s %v", status, err)
	}
}
