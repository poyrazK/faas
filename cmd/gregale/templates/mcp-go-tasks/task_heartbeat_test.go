package main

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestTaskHeartbeatRechecksExpiryAfterRowLock(t *testing.T) {
	for _, field := range []string{"lease_expires_at", "expires_at"} {
		t.Run(field, func(t *testing.T) {
			runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
			id := insertExecutionTestTask(t, runtime, ctx, "alice")
			task, err := runtime.claim(ctx)
			if err != nil || task == nil {
				t.Fatalf("claim: %v %v", task, err)
			}
			if _, err := runtime.pool.Exec(ctx, "UPDATE gregale_mcp_tasks SET "+field+"=clock_timestamp()+interval '1 second' WHERE task_id=$1", id); err != nil {
				t.Fatal(err)
			}
			lock, err := runtime.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lock.Rollback(ctx) }()
			if _, err := lock.Exec(ctx, "SELECT task_id FROM gregale_mcp_tasks WHERE task_id=$1 FOR UPDATE", id); err != nil {
				t.Fatal(err)
			}
			renewal := make(chan error, 1)
			go func() { _, err := runtime.heartbeatTask(ctx, task); renewal <- err }()
			for {
				var waiting bool
				if err := runtime.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%UPDATE gregale_mcp_tasks%lease_expires_at%')").Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			for {
				var expired bool
				if err := runtime.pool.QueryRow(ctx, "SELECT "+field+"<=clock_timestamp() FROM gregale_mcp_tasks WHERE task_id=$1", id).Scan(&expired); err != nil {
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
			if err := <-renewal; !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("renewed expired ownership: %v", err)
			}
			if field == "lease_expires_at" {
				replacement, err := runtime.claim(ctx)
				if err != nil || replacement == nil || replacement.LeaseToken == task.LeaseToken {
					t.Fatalf("recovery blocked: %v %v", replacement, err)
				}
			}
		})
	}
}

// The CLI's release gate maps instances to registry IDs and drain budgets
// through this receipt (ADR-20261123160000).
func TestTaskWorkerReceiptNamesRegistryIDAndDrainBudget(t *testing.T) {
	got := mcpTaskWorkerReceipt("11111111-1111-4111-8111-111111111111", 45*time.Second)
	want := `{"event":"mcp_task_worker_started","workerID":"11111111-1111-4111-8111-111111111111","claimFence":false,"shutdownTimeoutMs":45000}`
	if got != want {
		t.Fatalf("receipt = %s, want %s", got, want)
	}
}
