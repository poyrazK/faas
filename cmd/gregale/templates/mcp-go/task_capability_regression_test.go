package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// This probe uses the starter's existing PostgreSQL test helper and its own
// isolated schema. No production loops, clocks or ticker intervals are replaced.
func TestMCPRegressionBusyWorkerKeepsCapabilitiesFresh(t *testing.T) {
	runtime, setupCtx := taskExecutionTestRuntime(t, mcpTaskConfig{})
	var executed atomic.Int64
	runtime.taskExecutors = map[mcpTaskHandlerKey]MCPTaskExecutor{
		{toolName: "build_report", version: "2"}: func(context.Context, map[string]any, *MCPTaskExecutionContext) (map[string]any, error) {
			executed.Add(1)
			return map[string]any{"content": []any{}}, nil
		},
	}
	// Keep claims slower than the small handlers, a normal situation for a
	// database-backed queue. This fixture trigger introduces 10 ms of latency.
	_, err := runtime.pool.Exec(setupCtx, `
		CREATE FUNCTION investigation_slow_claim() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
		  IF NEW.status='running' AND OLD.status='queued' THEN PERFORM pg_sleep(0.01); END IF;
		  RETURN NEW;
		END $$;
		CREATE TRIGGER investigation_slow_claim BEFORE UPDATE ON gregale_mcp_tasks
		FOR EACH ROW EXECUTE FUNCTION investigation_slow_claim()`)
	if err != nil {
		t.Fatal(err)
	}
	owner := []byte("01234567890123456789012345678901")
	if err := runtime.associateOwners(setupCtx, [][]byte{owner}); err != nil {
		t.Fatal(err)
	}
	rows := make([][]any, 0, 6000)
	for range 6000 {
		id, err := newTaskUUID()
		if err != nil {
			t.Fatal(err)
		}
		args, err := runtime.encrypt(map[string]any{"report": "probe", "steps": 1}, id, "arguments", 256*1024)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, []any{runtime.namespace, id, owner, "build_report", "2", args, "queued", time.Now().Add(time.Hour)})
	}
	_, err = runtime.pool.CopyFrom(setupCtx, pgx.Identifier{"gregale_mcp_tasks"},
		[]string{"namespace", "task_id", "owner_hash", "tool_name", "handler_version", "arguments_encrypted", "status", "expires_at"}, pgx.CopyFromRows(rows))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	runtime.executionCtx = ctx
	runtime.pollInterval = 500 * time.Millisecond
	runtime.concurrency = 16
	runtime.workerID, err = newTaskUUID()
	if err != nil {
		t.Fatal(err)
	}
	runtime.done = make(chan struct{})
	runtime.activeTasks = make(map[string]*durableMCPTask)
	go runtime.workerLoop(ctx)
	defer func() { cancel(); <-runtime.done }()
	select {
	case <-time.After(47 * time.Second):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var age float64
	var queued int
	if err := runtime.pool.QueryRow(ctx, `SELECT EXTRACT(EPOCH FROM clock_timestamp()-MAX(heartbeat_at))::double precision
		FROM gregale_mcp_task_worker_handlers WHERE namespace=$1 AND worker_id=$2::uuid`, runtime.namespace, runtime.workerID).Scan(&age); err != nil {
		t.Fatal(err)
	}
	if err := runtime.pool.QueryRow(ctx, `SELECT count(*) FROM gregale_mcp_tasks WHERE namespace=$1 AND status='queued'`, runtime.namespace).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	t.Logf("completed executions=%d; queued=%d; capability heartbeat age=%.3fs (freshness budget=45s)", executed.Load(), queued, age)
	if executed.Load() < 100 || queued == 0 {
		t.Fatal("probe did not sustain a busy queue")
	}
	if age >= 45 {
		t.Fatalf("live busy worker expired from the capability inventory: heartbeat age %.3fs", age)
	}
}
