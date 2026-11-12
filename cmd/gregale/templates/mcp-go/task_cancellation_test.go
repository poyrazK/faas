package main

import (
	"fmt"
	"testing"
)

func TestTaskCancellationSettlesExpiredLeaseBeforeRecovery(t *testing.T) {
	for _, budget := range []int{1, 3} {
		t.Run(fmt.Sprintf("budget_%d", budget), func(t *testing.T) {
			runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{MaxAttempts: budget})
			id := insertExecutionTestTask(t, runtime, ctx, string(runtime.ownerHashes(nil)[0]))
			original, err := runtime.claim(ctx)
			if err != nil || original == nil {
				t.Fatalf("claim: %v %v", original, err)
			}
			if err := runtime.cancelTask(ctx, id, nil); err != nil {
				t.Fatal(err)
			}
			if task, err := runtime.claim(ctx); err != nil || task != nil {
				t.Fatalf("live cancelled lease was reclaimed: %v %v", task, err)
			}
			var status, token string
			if err := runtime.pool.QueryRow(ctx, `SELECT status,lease_token::text FROM gregale_mcp_tasks WHERE task_id=$1`, id).Scan(&status, &token); err != nil || status != "running" || token != original.LeaseToken {
				t.Fatalf("live lease changed: status=%s token=%s err=%v", status, token, err)
			}
			if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE task_id=$1`, id); err != nil {
				t.Fatal(err)
			}
			// Another queued Task must still make progress while cancellation settles.
			nextID := insertExecutionTestTask(t, runtime, ctx, "another-owner")
			next, err := runtime.claim(ctx)
			if err != nil || next == nil || next.ID != nextID {
				t.Fatalf("claimed cancelled Task or blocked queue: %v %v", next, err)
			}
			var attempts int
			var cleared bool
			if err := runtime.pool.QueryRow(ctx, `SELECT status,attempt_count,lease_token IS NULL AND lease_expires_at IS NULL AND NOT resume_pending AND next_attempt_at IS NULL FROM gregale_mcp_tasks WHERE task_id=$1`, id).Scan(&status, &attempts, &cleared); err != nil || status != "cancelled" || attempts != 1 || !cleared {
				t.Fatalf("cancellation not settled: status=%s attempts=%d cleared=%v err=%v", status, attempts, cleared, err)
			}
		})
	}
}
