package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestTaskExecutorPanicsAreRedactedAndTerminal(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"string", "TASK_PANIC_SECRET"},
		{"error", errors.New("TASK_PANIC_SECRET")},
		{"retryable error", ErrRetryableMCPTask},
		{"nil", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := invokeMCPTaskExecutor(func(context.Context, map[string]any, *MCPTaskExecutionContext) (map[string]any, error) {
				panic(tc.value)
			}, t.Context(), nil, nil)
			if result != nil || err == nil || err.Error() != "MCP task execution failed" || errors.Is(err, ErrRetryableMCPTask) {
				t.Fatal("executor panic was not converted to a generic terminal failure")
			}
		})
	}
}

func TestTaskExecutorReturnedResultsAndErrorsArePreserved(t *testing.T) {
	want := map[string]any{"content": []any{}}
	result, err := invokeMCPTaskExecutor(func(context.Context, map[string]any, *MCPTaskExecutionContext) (map[string]any, error) {
		return want, ErrRetryableMCPTask
	}, t.Context(), nil, nil)
	if !reflect.DeepEqual(result, want) || !errors.Is(err, ErrRetryableMCPTask) {
		t.Fatal("normal executor returns changed")
	}
}

func TestTaskExecutorPanicFailsTaskAndNextTaskCompletes(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
	runtime.taskExecutors[mcpTaskHandlerKey{"build_report", "2"}] = func(_ context.Context, args map[string]any, _ *MCPTaskExecutionContext) (map[string]any, error) {
		if args["report"] == "bad" {
			panic(map[string]any{"argument": "TASK_PANIC_SECRET"})
		}
		return map[string]any{"content": []any{}}, nil
	}
	for _, report := range []string{"bad", "good"} {
		accepted, err := runtime.create(ctx, "build_report", map[string]any{"report": report, "steps": float64(1)}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		task, err := runtime.claim(ctx)
		if err != nil || task == nil {
			t.Fatalf("claim: %v", err)
		}
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			runtime.runTask(ctx, task)
		}()
		select {
		case <-finished:
		case <-ctx.Done():
			t.Fatal("executor did not finish")
		}
		wire, stored, err := runtime.get(ctx, accepted["taskId"].(string), nil)
		if err != nil {
			t.Fatal(err)
		}
		if report == "bad" {
			encoded, err := json.Marshal(wire)
			if err != nil || strings.Contains(string(encoded), "TASK_PANIC_SECRET") {
				t.Fatal("panic value escaped the failure boundary")
			}
			if wire["status"] != "failed" || stored.Result != nil || stored.AttemptCount != 1 || stored.LeaseToken != "" {
				t.Fatalf("panicking task did not settle: %v", wire)
			}
			var nextAttempt bool
			if err := runtime.pool.QueryRow(ctx, `SELECT next_attempt_at IS NOT NULL FROM gregale_mcp_tasks WHERE task_id=$1`, task.ID).Scan(&nextAttempt); err != nil || nextAttempt {
				t.Fatal("panic scheduled another execution attempt")
			}
		} else if wire["status"] != "completed" {
			t.Fatalf("worker did not continue: %v", wire)
		}
	}
}

func TestTaskExecutorPanicPreservesCancellationAndLeaseFencing(t *testing.T) {
	for _, mode := range []string{"cancel", "replacement", "expired", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
			accepted, err := runtime.create(ctx, "build_report", map[string]any{"report": "review", "steps": float64(1)}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			task, err := runtime.claim(ctx)
			if err != nil || task == nil {
				t.Fatalf("claim: %v", err)
			}
			parent, cancel := context.WithCancel(ctx)
			defer cancel()
			var mutationErr error
			runtime.taskExecutors[mcpTaskHandlerKey{"build_report", "2"}] = func(context.Context, map[string]any, *MCPTaskExecutionContext) (map[string]any, error) {
				switch mode {
				case "cancel":
					mutationErr = runtime.cancelTask(ctx, task.ID, nil)
				case "replacement":
					_, mutationErr = runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET lease_token=gen_random_uuid() WHERE task_id=$1`, task.ID)
				case "expired":
					_, mutationErr = runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE task_id=$1`, task.ID)
				case "shutdown":
					cancel()
				}
				panic("TASK_PANIC_SECRET")
			}
			runtime.runTask(parent, task)
			if mutationErr != nil {
				t.Fatal(mutationErr)
			}
			wire, stored, err := runtime.get(ctx, accepted["taskId"].(string), nil)
			if err != nil {
				t.Fatal(err)
			}
			want, storedStatus := "working", "running"
			if mode == "cancel" {
				want = "cancelled"
				storedStatus = "cancelled"
			}
			if wire["status"] != want || stored.Status != storedStatus || stored.Result != nil || stored.Error != nil {
				t.Fatalf("panic overrode cancellation or lease ownership: %v", wire)
			}
		})
	}
}
