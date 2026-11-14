package main

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestTaskLeaseDeadlineBoundsFailedOrBlockedRenewals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var expirations atomic.Int32
		lease := newMCPTaskLeaseDeadline(t.Context(), time.Now().Add(mcpTaskLeaseDuration), func() { expirations.Add(1) })
		defer lease.close()
		time.Sleep(29 * time.Second)
		synctest.Wait()
		if expirations.Load() != 0 {
			t.Fatal("a still-proven lease expired early")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if expirations.Load() != 1 {
			t.Fatal("an unrenewed lease did not expire")
		}
		lease.renew(time.Now())
		time.Sleep(time.Minute)
		synctest.Wait()
		if expirations.Load() != 1 {
			t.Fatal("a late renewal revived expired execution")
		}
	})
}

func TestTaskLeaseRenewalExcludesResponseLatencyAndCloseStopsTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		var expirations atomic.Int32
		lease := newMCPTaskLeaseDeadline(t.Context(), started.Add(mcpTaskLeaseDuration), func() { expirations.Add(1) })
		time.Sleep(20 * time.Second)
		lease.renew(started.Add(5 * time.Second))
		time.Sleep(14 * time.Second)
		synctest.Wait()
		if expirations.Load() != 0 {
			t.Fatal("a recovered renewal did not extend the budget")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if expirations.Load() != 1 {
			t.Fatal("database response time extended lease ownership")
		}
		lease.close()
		other := newMCPTaskLeaseDeadline(t.Context(), time.Time{}, func() { expirations.Add(1) })
		other.close()
		time.Sleep(time.Minute)
		synctest.Wait()
		if expirations.Load() != 1 {
			t.Fatal("a finished execution retained a live lease timer")
		}
	})
}

func TestTaskLeaseExpiryCancelsHandlerAndSuppressesCompletion(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
	insertExecutionTestTask(t, runtime, ctx, "alice")
	task, err := runtime.claim(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim: %v", err)
	}
	task.leaseDeadline = time.Now().Add(100 * time.Millisecond)
	runtime.taskExecutors[mcpTaskHandlerKey{"build_report", "2"}] = func(ctx context.Context, _ map[string]any, _ *MCPTaskExecutionContext) (map[string]any, error) {
		<-ctx.Done()
		return map[string]any{"content": []any{}}, nil
	}
	parent, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	runtime.runTask(parent, task)
	if parent.Err() != nil {
		t.Fatal("handler waited for its caller instead of lease expiry")
	}
	var status string
	var empty bool
	if err := runtime.pool.QueryRow(ctx, `SELECT status,result_encrypted IS NULL FROM gregale_mcp_tasks WHERE task_id=$1`, task.ID).Scan(&status, &empty); err != nil {
		t.Fatal(err)
	}
	if status != "running" || !empty {
		t.Fatal("expired execution published a terminal result")
	}
}
