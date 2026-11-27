package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTaskOwnerIdentityMatchesOtherRuntimesAndRetainsGoLegacy(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	runtime := &mcpTaskRuntime{taskKeys: []mcpTaskKey{{ownerHashKey: deriveTaskKey(key, "gregale-mcp-task-owner:v1")}}}
	identity := &authContext{resource: "https://example.com/mcp", subject: "alice", clientID: "test-client"}
	got := runtime.ownerHashes(identity)
	// Fixed vectors shared with the Node and Python PostgreSQL tests.
	for i, want := range []string{
		"4fbc7a60b90569b9ac1b0d8b0c1f59ce770120370a5da54c662ce779b04edcab",
		"803002df4e65c74a4f74c141874d907b0d661cb39332bc05c3424af3145a2928",
	} {
		if hex.EncodeToString(got[i]) != want {
			t.Fatalf("owner identity %d = %x, want %s", i, got[i], want)
		}
	}
}

func TestTaskExecutionPolicyRejectsInvalidSettings(t *testing.T) {
	for _, settings := range []mcpTaskConfig{
		{MaxAttempts: -1}, {MaxAttempts: 11}, {MaxRunning: -1}, {MaxRunning: 2, MaxRunningPerOwner: 3},
		{RetryBaseDelayMS: 99}, {RetryBaseDelayMS: 200, RetryMaxDelayMS: 100}, {RetryMaxDelayMS: 86400001},
	} {
		if _, err := resolveTaskExecutionPolicy(settings); err == nil {
			t.Fatalf("accepted invalid execution settings %+v", settings)
		}
	}
}

func taskExecutionTestRuntime(t *testing.T, settings mcpTaskConfig) (*mcpTaskRuntime, context.Context) {
	t.Helper()
	databaseURL := os.Getenv("MCP_TASKS_TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("MCP_TASKS_REQUIRE_POSTGRES") == "1" {
			t.Fatal("MCP_TASKS_TEST_DATABASE_URL is required")
		}
		t.Skip("set MCP_TASKS_TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	id, err := newTaskUUID()
	if err != nil {
		t.Fatal(err)
	}
	schema := "mcp_execution_" + strings.ReplaceAll(id, "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",pg_catalog"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE")
		admin.Close()
	})
	if err := migrateTaskSchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	policy, err := resolveTaskExecutionPolicy(settings)
	if err != nil {
		t.Fatal(err)
	}
	key := []byte("01234567890123456789012345678901")
	handlers, executors := snapshotMCPTaskHandlers()
	return &mcpTaskRuntime{pool: pool, namespace: "execution-test", execution: policy, ttl: time.Hour,
		maxOutstanding: 1000, maxPerOwner: 100, payloadEncryptionVersion: 1,
		taskKeys:     []mcpTaskKey{{id: "initial", ownerHashKey: deriveTaskKey(key, "gregale-mcp-task-owner:v1"), payloadKey: deriveTaskKey(key, "gregale-mcp-task-payload:v1")}},
		taskHandlers: handlers, taskExecutors: executors}, ctx
}

func insertExecutionTestTask(t *testing.T, runtime *mcpTaskRuntime, ctx context.Context, owner string) string {
	t.Helper()
	id, err := newTaskUUID()
	if err != nil {
		t.Fatal(err)
	}
	args, err := runtime.encrypt(map[string]any{"report": "review", "steps": 1}, id, "arguments", 256*1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(owner) != 32 {
		if err := runtime.associateOwners(ctx, [][]byte{[]byte(owner)}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = runtime.pool.Exec(ctx, `INSERT INTO gregale_mcp_tasks(namespace,task_id,owner_hash,tool_name,handler_version,arguments_encrypted,status,expires_at)
		VALUES($1,$2,$3,'build_report','2',$4,'queued',clock_timestamp()+interval '1 hour')`, runtime.namespace, id, []byte(owner), args)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTaskExecutionCapacityAcrossConcurrentReplicas(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{MaxRunning: 3, MaxRunningPerOwner: 2})
	for range 10 {
		insertExecutionTestTask(t, runtime, ctx, "alice")
	}
	claimConcurrently := func(want int) {
		t.Helper()
		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make(chan *durableMCPTask, 10)
		errors := make(chan error, 10)
		for range 10 {
			wg.Go(func() {
				<-start
				task, err := runtime.claim(ctx)
				results <- task
				errors <- err
			})
		}
		close(start)
		wg.Wait()
		close(results)
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
		claimed := 0
		for task := range results {
			if task != nil {
				claimed++
			}
		}
		if claimed != want {
			t.Fatalf("claimed %d concurrent tasks, want %d", claimed, want)
		}
	}
	claimConcurrently(2)
	insertExecutionTestTask(t, runtime, ctx, "bob")
	insertExecutionTestTask(t, runtime, ctx, "bob")
	claimConcurrently(1)
}

func TestTaskExecutionHonorsRetryScheduleAndAttemptBudget(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{MaxAttempts: 5})
	id := insertExecutionTestTask(t, runtime, ctx, "alice")
	if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET attempt_count=3,next_attempt_at=clock_timestamp()+interval '1 day' WHERE task_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if task, err := runtime.claim(ctx); err != nil || task != nil {
		t.Fatalf("future retry claimed: task=%v error=%v", task, err)
	}
	if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE task_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	task, err := runtime.claim(ctx)
	if err != nil || task == nil || task.AttemptCount != 4 {
		t.Fatalf("configured fourth attempt rejected: task=%+v error=%v", task, err)
	}
	if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET status='queued',attempt_count=5,resume_pending=true,lease_token=NULL,lease_expires_at=NULL WHERE task_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	task, err = runtime.claim(ctx)
	if err != nil || task == nil || task.AttemptCount != 5 {
		t.Fatalf("final-attempt input resume rejected: task=%+v error=%v", task, err)
	}
	if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE task_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if next, err := runtime.claim(ctx); err != nil || next != nil {
		t.Fatalf("exhausted task reclaimed: task=%v error=%v", next, err)
	}
}

func TestTaskExecutionDoesNotReviveExpiredLeases(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{MaxRunning: 1, MaxRunningPerOwner: 1})
	id := insertExecutionTestTask(t, runtime, ctx, "alice")
	old, err := runtime.claim(ctx)
	if err != nil || old == nil {
		t.Fatalf("initial claim: %v %v", old, err)
	}
	if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE task_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.heartbeatTask(ctx, old); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired heartbeat revived lease: %v", err)
	}
	next, err := runtime.claim(ctx)
	if err != nil || next == nil || next.LeaseToken == old.LeaseToken {
		t.Fatalf("expired capacity did not recover: task=%+v error=%v", next, err)
	}
	runtime.failTaskWithRetry(ctx, old, true)
	var status string
	if err := runtime.pool.QueryRow(ctx, `SELECT status FROM gregale_mcp_tasks WHERE task_id=$1`, id).Scan(&status); err != nil || status != "running" {
		t.Fatalf("stale failure changed replacement lease: status=%s error=%v", status, err)
	}
}

func TestTaskExecutionLoweredBudgetSettlesQueuedRetries(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{MaxAttempts: 5})
	id := insertExecutionTestTask(t, runtime, ctx, "alice")
	for attempt := 1; attempt <= 3; attempt++ {
		task, err := runtime.claim(ctx)
		if err != nil || task == nil || task.AttemptCount != attempt {
			t.Fatalf("attempt %d: task=%+v error=%v", attempt, task, err)
		}
		runtime.failTaskWithRetry(ctx, task, true)
		if attempt < 3 {
			if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE task_id=$1`, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	runtime.execution.maxAttempts = 3
	if task, err := runtime.claim(ctx); err != nil || task != nil {
		t.Fatalf("exhausted retry claimed: %v %v", task, err)
	}
	var status string
	var pending bool
	if err := runtime.pool.QueryRow(ctx, `SELECT status,next_attempt_at IS NOT NULL OR resume_pending FROM gregale_mcp_tasks WHERE task_id=$1`, id).Scan(&status, &pending); err != nil || status != "failed" || pending {
		t.Fatalf("retry not settled: status=%s pending=%v error=%v", status, pending, err)
	}
	metrics, err := runtime.queueMetrics(ctx)
	if err != nil || metrics.outstandingTasks != 0 {
		t.Fatalf("terminal retry consumes capacity: metrics=%+v error=%v", metrics, err)
	}
}

func TestTaskExecutionLoweredBudgetPreservesActiveAttemptAndDrainResume(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{MaxAttempts: 5})
	id := insertExecutionTestTask(t, runtime, ctx, "alice")
	if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET attempt_count=3 WHERE task_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	active, err := runtime.claim(ctx)
	if err != nil || active == nil || active.AttemptCount != 4 {
		t.Fatalf("fourth attempt: %v %v", active, err)
	}
	runtime.execution.maxAttempts = 3
	if task, err := runtime.claim(ctx); err != nil || task != nil {
		t.Fatalf("live attempt reclaimed: %v %v", task, err)
	}
	runtime.releaseDrainedTask(ctx, active)
	metrics, err := runtime.queueMetrics(ctx)
	if err != nil || metrics.withoutCompatibleWorkerTasks != 1 {
		t.Fatalf("resumption missing from compatibility metrics: %+v %v", metrics, err)
	}
	resumed, err := runtime.claim(ctx)
	if err != nil || resumed == nil || resumed.ID != id || resumed.AttemptCount != 4 {
		t.Fatalf("existing attempt cannot resume: %v %v", resumed, err)
	}
	runtime.failTaskWithRetry(ctx, resumed, true)
	if next, err := runtime.claim(ctx); err != nil || next != nil {
		t.Fatalf("resumption granted another attempt: %v %v", next, err)
	}
}

func TestTaskExecutionCrashExhaustionReturnsObjectError(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
	identity := &authContext{resource: "https://example.com/mcp", subject: "alice", clientID: "test-client"}
	id := insertExecutionTestTask(t, runtime, ctx, string(runtime.ownerHashes(identity)[0]))
	for range runtime.execution.maxAttempts {
		if task, err := runtime.claim(ctx); err != nil || task == nil {
			t.Fatalf("crash recovery claim: %v %v", task, err)
		}
		if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE task_id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	if task, err := runtime.claim(ctx); err != nil || task != nil {
		t.Fatalf("exhausted attempt reclaimed: %v %v", task, err)
	}
	wire, _, err := runtime.get(ctx, id, identity)
	if err != nil || wire["status"] != "failed" {
		t.Fatalf("failed task unavailable: %v %v", wire, err)
	}
	taskError, ok := wire["error"].(map[string]any)
	if !ok || taskError["code"] != -32603 || taskError["message"] != "Task execution failed" {
		t.Fatalf("failed task must include its object error: %v", wire)
	}
}

func TestTaskExecutionPersistsOnlyOptedInRetries(t *testing.T) {
	for _, scenario := range []string{"retryable", "terminal", "cancelled", "ttl", "exhausted"} {
		t.Run(scenario, func(t *testing.T) {
			runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{RetryBaseDelayMS: 1000, RetryMaxDelayMS: 1000})
			id := insertExecutionTestTask(t, runtime, ctx, "alice")
			task, err := runtime.claim(ctx)
			if err != nil || task == nil {
				t.Fatalf("initial claim: %v %v", task, err)
			}
			patch := map[string]string{"cancelled": "cancel_requested_at=clock_timestamp()", "ttl": "expires_at=clock_timestamp()+interval '100 milliseconds'", "exhausted": "attempt_count=3"}[scenario]
			if patch != "" {
				if _, err := runtime.pool.Exec(ctx, "UPDATE gregale_mcp_tasks SET "+patch+" WHERE task_id=$1", id); err != nil {
					t.Fatal(err)
				}
			}
			runtime.taskExecutors[mcpTaskHandlerKey{"build_report", "2"}] = func(context.Context, map[string]any, *MCPTaskExecutionContext) (map[string]any, error) {
				if scenario == "terminal" {
					return nil, errors.New("private provider failure")
				}
				return nil, fmt.Errorf("private provider failure: %w", ErrRetryableMCPTask)
			}
			runtime.runTask(ctx, task)
			var status string
			var due *time.Time
			if err := runtime.pool.QueryRow(ctx, `SELECT status,next_attempt_at FROM gregale_mcp_tasks WHERE task_id=$1`, id).Scan(&status, &due); err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"retryable": "queued", "terminal": "failed", "cancelled": "cancelled", "ttl": "failed", "exhausted": "failed"}[scenario]
			if status != want || (due != nil) != (scenario == "retryable") {
				t.Fatalf("status=%s retry_due=%v, want %s", status, due, want)
			}
			if due != nil {
				if next, err := runtime.claim(ctx); err != nil || next != nil {
					t.Fatalf("persisted retry did not wait: %v %v", next, err)
				}
			}
		})
	}
}

func TestTaskOwnershipReadsLegacyRowsWithoutWideningAccess(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
	identity := &authContext{resource: "https://example.com/mcp", subject: "alice", clientID: "test-client"}
	id := insertExecutionTestTask(t, runtime, ctx, "placeholder")
	legacy, _ := hex.DecodeString("803002df4e65c74a4f74c141874d907b0d661cb39332bc05c3424af3145a2928")
	if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET owner_hash=$2 WHERE task_id=$1`, id, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.readTask(ctx, id, identity); err != nil {
		t.Fatalf("legacy Go task is inaccessible: %v", err)
	}
	foreign := *identity
	foreign.subject = "bob"
	if _, err := runtime.readTask(ctx, id, &foreign); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("legacy fallback widened caller access: %v", err)
	}
	runtime.maxPerOwner = 1
	if _, err := runtime.create(ctx, "build_report", map[string]any{"report": "new", "steps": float64(1)}, identity, nil); err == nil {
		t.Fatal("legacy task did not consume the owner's outstanding capacity")
	}
}

func TestTaskExecutionMigrationAddsRetryColumnAndPreservesTasks(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
	id := insertExecutionTestTask(t, runtime, ctx, "alice")
	if _, err := runtime.pool.Exec(ctx, `ALTER TABLE gregale_mcp_tasks DROP COLUMN next_attempt_at`); err != nil {
		t.Fatal(err)
	}
	if err := checkTaskSchema(ctx, runtime.pool); err == nil {
		t.Fatal("startup accepted a queue without retry scheduling state")
	}
	if err := migrateTaskSchema(ctx, runtime.pool); err != nil {
		t.Fatal(err)
	}
	if err := checkTaskSchema(ctx, runtime.pool); err != nil {
		t.Fatal(err)
	}
	task, err := runtime.claim(ctx)
	if err != nil || task == nil || task.ID != id {
		t.Fatalf("migration did not preserve existing task: task=%v error=%v", task, err)
	}
}

func TestTaskOwnerAliasesShareCapacityBeforeAndAfterAuthentication(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{MaxRunning: 3, MaxRunningPerOwner: 1})
	identity := &authContext{resource: "https://example.com/mcp", subject: "alice", clientID: "test-client"}
	owners := runtime.ownerHashes(identity)
	for _, owner := range owners {
		insertExecutionTestTask(t, runtime, ctx, string(owner))
	}
	first, err := runtime.claim(ctx)
	if err != nil || first == nil {
		t.Fatalf("initial claim: %v %v", first, err)
	}
	for _, registered := range []bool{false, true} {
		if registered {
			if err := runtime.associateOwners(ctx, owners); err != nil {
				t.Fatal(err)
			}
		}
		second, err := runtime.claim(ctx)
		if err != nil || second != nil {
			t.Fatalf("aliases bypassed per-owner cap (registered=%v): task=%v error=%v", registered, second, err)
		}
	}
	// A returning caller maps all its aliases, including ones without a task yet.
	bob := *identity
	bob.subject = "bob"
	if err := runtime.associateOwners(ctx, runtime.ownerHashes(&bob)); err != nil {
		t.Fatal(err)
	}
	id := insertExecutionTestTask(t, runtime, ctx, string(runtime.ownerHashes(&bob)[1]))
	next, err := runtime.claim(ctx)
	if err != nil || next == nil || next.ID != id {
		t.Fatalf("distinct caller blocked: %v %v", next, err)
	}
	// Overlapping key generations merge transitively without splitting groups.
	previous := []byte("previous-key-alias")
	if err := runtime.associateOwners(ctx, [][]byte{previous, owners[0]}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.associateOwners(ctx, [][]byte{[]byte("future-key-alias"), previous}); err != nil {
		t.Fatal(err)
	}
	var groups int
	if err := runtime.pool.QueryRow(ctx, `SELECT COUNT(DISTINCT owner_group) FROM gregale_mcp_task_owners WHERE namespace=$1 AND owner_hash=ANY($2::bytea[])`, runtime.namespace, append(owners, previous, []byte("future-key-alias"))).Scan(&groups); err != nil || groups != 1 {
		t.Fatalf("owner group split: groups=%d error=%v", groups, err)
	}
}

func TestTaskOwnerAliasCleanupRetainsWholeGroupUntilLastTaskExpires(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
	owners := runtime.ownerHashes(&authContext{subject: "alice"})
	if err := runtime.associateOwners(ctx, owners); err != nil {
		t.Fatal(err)
	}
	id := insertExecutionTestTask(t, runtime, ctx, string(owners[1]))
	if err := runtime.associateOwners(ctx, [][]byte{[]byte("unused-owner")}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cleanupExpiredTasks(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := runtime.pool.QueryRow(ctx, `SELECT COUNT(*) FROM gregale_mcp_task_owners WHERE namespace=$1`, runtime.namespace).Scan(&count); err != nil || count != 2 {
		t.Fatalf("lost retained aliases or kept unused owner: count=%d error=%v", count, err)
	}
	if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET created_at=clock_timestamp()-interval '1 hour',expires_at=clock_timestamp()-interval '1 second' WHERE task_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cleanupExpiredTasks(ctx); err != nil {
		t.Fatal(err)
	}
	if err := runtime.pool.QueryRow(ctx, `SELECT COUNT(*) FROM gregale_mcp_task_owners WHERE namespace=$1`, runtime.namespace).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unused aliases were retained: count=%d error=%v", count, err)
	}
}
