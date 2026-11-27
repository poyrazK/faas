package main

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"testing"
	"time"
)

func useExplicitRewrapKey(runtime *mcpTaskRuntime, secret string) {
	native := runtime.taskKeys[0]
	runtime.taskKeys = []mcpTaskKey{{id: native.id, ownerHashKey: native.ownerHashKey, payloadKey: deriveTaskKey([]byte(secret), "gregale-mcp-task-payload:v2:"+native.id), mainAAD: true}, native}
}

func TestTaskRewrapAuthenticatesSameIDNativeFields(t *testing.T) {
	for _, field := range []string{"arguments", "result", "error", "input-state"} {
		t.Run(field, func(t *testing.T) {
			runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
			runtime.payloadEncryptionVersion = 2
			id := insertExecutionTestTask(t, runtime, ctx, "alice")
			old, err := runtime.encrypt(map[string]any{"retained": field}, id, field, 1024)
			if err != nil {
				t.Fatal(err)
			}
			useExplicitRewrapKey(runtime, strings.Repeat("new-key-", 8))
			args, err := runtime.encrypt(map[string]any{"report": "test", "steps": 1}, id, "arguments", 1024)
			if err != nil {
				t.Fatal(err)
			}
			column := map[string]string{"arguments": "arguments_encrypted", "result": "result_encrypted", "error": "error_encrypted", "input-state": "input_state_encrypted"}[field]
			if _, err := runtime.pool.Exec(ctx, "UPDATE gregale_mcp_tasks SET arguments_encrypted=$2 WHERE task_id=$1", id, args); err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.pool.Exec(ctx, "UPDATE gregale_mcp_tasks SET "+column+"=$2 WHERE task_id=$1", id, old); err != nil {
				t.Fatal(err)
			}
			var before time.Time
			if err := runtime.pool.QueryRow(ctx, "SELECT updated_at FROM gregale_mcp_tasks WHERE task_id=$1", id).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if n, err := runtime.rewrapBatch(ctx, 64); err != nil || n != 1 {
				t.Fatalf("rewrapped=%d error=%v", n, err)
			}
			var payload []byte
			var after time.Time
			if err := runtime.pool.QueryRow(ctx, "SELECT "+column+",updated_at FROM gregale_mcp_tasks WHERE task_id=$1", id).Scan(&payload, &after); err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(payload, old) || !before.Equal(after) {
				t.Fatal("rewrap failed or changed protocol timestamps")
			}
			active := &mcpTaskRuntime{namespace: runtime.namespace, taskKeys: runtime.taskKeys[:1]}
			if _, err := active.decryptPayload(payload, id, field); err != nil {
				t.Fatalf("new active cipher cannot read payload: %v", err)
			}
			if n, err := runtime.rewrapBatch(ctx, 64); err != nil || n != 0 {
				t.Fatalf("current ciphertext rewritten: %d %v", n, err)
			}
			var unchanged []byte
			_ = runtime.pool.QueryRow(ctx, "SELECT "+column+" FROM gregale_mcp_tasks WHERE task_id=$1", id).Scan(&unchanged)
			if !bytes.Equal(payload, unchanged) {
				t.Fatal("current ciphertext changed on the next pass")
			}
		})
	}
}

func TestTaskRewrapScanProgressesPastCurrentAndLockedRows(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
	runtime.payloadEncryptionVersion = 2
	ids := []string{}
	for range 12 {
		ids = append(ids, insertExecutionTestTask(t, runtime, ctx, "alice"))
	}
	sort.Strings(ids)
	useExplicitRewrapKey(runtime, strings.Repeat("new-key-", 8))
	for _, id := range ids[:9] {
		payload, err := runtime.encrypt(map[string]any{"report": "current"}, id, "arguments", 1024)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.pool.Exec(ctx, "UPDATE gregale_mcp_tasks SET arguments_encrypted=$2 WHERE task_id=$1", id, payload); err != nil {
			t.Fatal(err)
		}
	}
	lock, err := runtime.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(ctx, "SELECT task_id FROM gregale_mcp_tasks WHERE task_id=$1 FOR UPDATE", ids[9]); err != nil {
		t.Fatal(err)
	}
	changed := 0
	for i := range 9 {
		if i == 3 {
			if err := lock.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
		n, err := runtime.rewrapBatch(ctx, 64)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && n != 0 {
			t.Fatal("first current-only batch should not rewrite data")
		}
		changed += n
	}
	if changed != 3 {
		t.Fatalf("rewrote %d rows, want three retained native rows", changed)
	}
	active := &mcpTaskRuntime{namespace: runtime.namespace, taskKeys: runtime.taskKeys[:1]}
	for _, id := range ids {
		var payload []byte
		if err := runtime.pool.QueryRow(ctx, "SELECT arguments_encrypted FROM gregale_mcp_tasks WHERE task_id=$1", id).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		if _, err := active.decryptPayload(payload, id, "arguments"); err != nil {
			t.Fatalf("retained row %s was starved: %v", id, err)
		}
	}
}

func TestTaskRewrapRollsBackCiphertextAndCursorOnFailure(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
	runtime.payloadEncryptionVersion = 2
	ids := []string{insertExecutionTestTask(t, runtime, ctx, "alice"), insertExecutionTestTask(t, runtime, ctx, "alice")}
	sort.Strings(ids)
	original := make([][]byte, 2)
	for i, id := range ids {
		if err := runtime.pool.QueryRow(ctx, "SELECT arguments_encrypted FROM gregale_mcp_tasks WHERE task_id=$1", id).Scan(&original[i]); err != nil {
			t.Fatal(err)
		}
	}
	corrupt := append([]byte(nil), original[1]...)
	corrupt[len(corrupt)-1] ^= 1
	if _, err := runtime.pool.Exec(ctx, "UPDATE gregale_mcp_tasks SET arguments_encrypted=$2 WHERE task_id=$1", ids[1], corrupt); err != nil {
		t.Fatal(err)
	}
	useExplicitRewrapKey(runtime, strings.Repeat("new-key-", 8))
	if _, err := runtime.rewrapBatch(ctx, 2); err == nil {
		t.Fatal("corrupted payload accepted")
	}
	var first []byte
	if err := runtime.pool.QueryRow(ctx, "SELECT arguments_encrypted FROM gregale_mcp_tasks WHERE task_id=$1", ids[0]).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, original[0]) || runtime.rewrapAfter != "" || runtime.rewrapThrough != "" {
		t.Fatal("failed batch changed ciphertext or scan state")
	}
	if _, err := runtime.pool.Exec(ctx, "UPDATE gregale_mcp_tasks SET arguments_encrypted=$2 WHERE task_id=$1", ids[1], original[1]); err != nil {
		t.Fatal(err)
	}
	if n, err := runtime.rewrapBatch(ctx, 2); err != nil || n != 2 {
		t.Fatalf("repaired batch did not recover: %d %v", n, err)
	}
}

func TestTaskRewrapDistinctIDsKeepFastPath(t *testing.T) {
	for _, version := range []int{1, 2} {
		runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
		runtime.payloadEncryptionVersion = version
		id := insertExecutionTestTask(t, runtime, ctx, "alice")
		useExplicitRewrapKey(runtime, strings.Repeat("new-key-", 8))
		runtime.taskKeys[0].id = "next"
		runtime.taskKeys[0].payloadKey = deriveTaskKey([]byte(strings.Repeat("new-key-", 8)), "gregale-mcp-task-payload:v2:next")
		runtime.payloadEncryptionVersion = 2
		if n, err := runtime.rewrapBatch(ctx, 2); err != nil || n != 1 {
			t.Fatalf("v%d fast path: %d %v", version, n, err)
		}
		var payload []byte
		if err := runtime.pool.QueryRow(ctx, "SELECT arguments_encrypted FROM gregale_mcp_tasks WHERE task_id=$1", id).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		active := &mcpTaskRuntime{namespace: runtime.namespace, taskKeys: runtime.taskKeys[:1]}
		if _, err := active.decryptPayload(payload, id, "arguments"); err != nil {
			t.Fatal(err)
		}
	}
}
