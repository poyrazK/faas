package main

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestTaskPayloadPreflightChecksEveryRetainedFieldAndVersion(t *testing.T) {
	for _, tc := range []struct {
		field, column string
		version       int
	}{
		{"arguments", "arguments_encrypted", 1}, {"arguments", "arguments_encrypted", 2},
		{"result", "result_encrypted", 2}, {"error", "error_encrypted", 2}, {"input-state", "input_state_encrypted", 2},
	} {
		t.Run(fmt.Sprintf("%s/v%d", tc.field, tc.version), func(t *testing.T) {
			runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
			insertExecutionTestTask(t, runtime, ctx, "alice") // A readable v1 sample must not hide other keys.
			id := insertExecutionTestTask(t, runtime, ctx, "alice")
			key := mcpTaskKey{id: "old", payloadKey: deriveTaskKey([]byte(strings.Repeat("old-", 8)), "gregale-mcp-task-payload:v1")}
			old := &mcpTaskRuntime{namespace: runtime.namespace, taskKeys: []mcpTaskKey{key}, payloadEncryptionVersion: tc.version}
			payload, err := old.encrypt(map[string]any{}, id, tc.field, 1024)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.pool.Exec(ctx, "UPDATE gregale_mcp_tasks SET "+tc.column+"=$2 WHERE task_id=$1", id, payload); err != nil {
				t.Fatal(err)
			}
			if err := runtime.checkTaskPayloadKeys(ctx); err == nil {
				t.Fatal("preflight accepted an unreadable retained payload")
			}
			runtime.taskKeys = append(runtime.taskKeys, key)
			if err := runtime.checkTaskPayloadKeys(ctx); err != nil {
				t.Fatalf("retained key was not accepted: %v", err)
			}
			runtime.taskKeys = runtime.taskKeys[:1]
			if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET namespace='other-namespace' WHERE task_id=$1`, id); err != nil {
				t.Fatal(err)
			}
			if err := runtime.checkTaskPayloadKeys(ctx); err != nil {
				t.Fatalf("foreign namespace affected startup: %v", err)
			}
			if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET namespace=$2,created_at=clock_timestamp()-interval '1 hour',expires_at=clock_timestamp()-interval '1 second' WHERE task_id=$1`, id, runtime.namespace); err != nil {
				t.Fatal(err)
			}
			if err := runtime.checkTaskPayloadKeys(ctx); err != nil {
				t.Fatalf("expired data required a retired key: %v", err)
			}
		})
	}
}

func TestTaskStartupDefaultsZeroSettings(t *testing.T) {
	fixture, _ := taskExecutionTestRuntime(t, mcpTaskConfig{})
	binding, err := url.Parse(fixture.pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	query := binding.Query()
	query.Set("search_path", fixture.pool.Config().ConnConfig.RuntimeParams["search_path"])
	binding.RawQuery = query.Encode()
	t.Setenv("DATABASE_URL", binding.String())
	t.Setenv("MCP_TASK_NAMESPACE", fixture.namespace)
	t.Setenv("MCP_TASK_OWNER_KEY", "01234567890123456789012345678901")
	config := mcpConfig{Tasks: []byte(`{"enabled":true,"owner_key_id":"initial","ttl_seconds":0,"poll_interval_ms":0,"worker_concurrency":0,"max_attempts":0,"retry_base_delay_ms":0,"retry_max_delay_ms":0}`)}
	runtime, err := startMCPTaskRuntime(config, "web")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.close)
	if runtime.ttl != 24*time.Hour || runtime.pollInterval != 2*time.Second || runtime.execution.maxAttempts != 3 || runtime.execution.retryBaseDelayMS != 1000 || runtime.execution.retryMaxDelayMS != 60000 {
		t.Fatalf("zero settings did not resolve to defaults: ttl=%s polling=%s execution=%+v", runtime.ttl, runtime.pollInterval, runtime.execution)
	}
}

func TestTaskStartupRejectsWrongSecretBeforeStartingWriters(t *testing.T) {
	fixture, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
	id := insertExecutionTestTask(t, fixture, ctx, "alice")
	binding, err := url.Parse(fixture.pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	query := binding.Query()
	query.Set("search_path", fixture.pool.Config().ConnConfig.RuntimeParams["search_path"])
	binding.RawQuery = query.Encode()
	t.Setenv("DATABASE_URL", binding.String())
	t.Setenv("MCP_TASK_NAMESPACE", fixture.namespace)
	config := mcpConfig{Tasks: []byte(`{"enabled":true,"owner_key_id":"initial"}`)}
	for _, secret := range []string{strings.Repeat("wrong", 8), "01234567890123456789012345678901"} {
		t.Setenv("MCP_TASK_OWNER_KEY", secret)
		runtime, err := startMCPTaskRuntime(config, "web")
		if runtime != nil {
			runtime.close()
		}
		if (err == nil) != (secret == "01234567890123456789012345678901") {
			t.Fatalf("unexpected startup result: %v", err)
		}
	}
	var attempts int
	if err := fixture.pool.QueryRow(ctx, `SELECT attempt_count FROM gregale_mcp_tasks WHERE task_id=$1`, id).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("startup consumed attempts: %d %v", attempts, err)
	}
}

func TestTaskClaimRollsBackUnreadablePayloadsWithoutConsumingAttempts(t *testing.T) {
	for _, column := range []string{"arguments_encrypted", "result_encrypted", "error_encrypted", "input_state_encrypted"} {
		t.Run(column, func(t *testing.T) {
			runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
			id := insertExecutionTestTask(t, runtime, ctx, "alice")
			if err := runtime.checkTaskPayloadKeys(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.pool.Exec(ctx, "UPDATE gregale_mcp_tasks SET "+column+"=$2 WHERE task_id=$1", id, []byte("corrupted payload")); err != nil {
				t.Fatal(err)
			}
			for range 3 {
				if task, err := runtime.claim(ctx); err == nil || task != nil {
					t.Fatal("claim accepted an unreadable payload")
				}
			}
			var status string
			var attempts int
			var noLease bool
			if err := runtime.pool.QueryRow(ctx, `SELECT status,attempt_count,lease_token IS NULL FROM gregale_mcp_tasks WHERE task_id=$1`, id).Scan(&status, &attempts, &noLease); err != nil || status != "queued" || attempts != 0 || !noLease {
				t.Fatalf("claim was committed: status=%s attempts=%d noLease=%v error=%v", status, attempts, noLease, err)
			}
		})
	}
}
