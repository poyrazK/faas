package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestTaskUUIDCasePreservesPayloadReadsAndInputUpdates(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
			runtime.payloadEncryptionVersion = version
			identity := &authContext{resource: "https://example.com/mcp", subject: "alice", clientID: "test-client"}
			id := insertExecutionTestTask(t, runtime, ctx, string(runtime.ownerHashes(identity)[0]))
			state := taskInputState{Requests: map[string]any{"approval": map[string]any{"method": "elicitation/create", "params": map[string]any{"mode": "form", "message": "Approve?"}}}, Responses: map[string]any{}}
			payloads := make([][]byte, 3)
			for index, field := range []struct {
				name string
				data any
			}{{"result", map[string]any{"content": []any{}}}, {"error", map[string]any{"code": -32603, "message": "Previous failure"}}, {"input-state", state}} {
				var err error
				payloads[index], err = runtime.encrypt(field.data, id, field.name, 1024)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET status='input_required',attempt_count=1,result_encrypted=$2,error_encrypted=$3,input_state_encrypted=$4 WHERE task_id=$1`, id, payloads[0], payloads[1], payloads[2]); err != nil {
				t.Fatal(err)
			}
			upper := strings.ToUpper(id)
			wire, task, err := runtime.get(ctx, upper, identity)
			if err != nil || task.ID != id || task.Result == nil || task.Error == nil || len(task.InputRequests) != 1 || wire["status"] != "input_required" {
				t.Fatalf("UUID alias cannot read retained fields: task=%+v error=%v", task, err)
			}
			if _, _, err := runtime.get(ctx, upper, &authContext{resource: identity.resource, subject: "bob", clientID: identity.clientID}); err == nil {
				t.Fatal("UUID normalization bypassed ownership")
			}
			if err := runtime.updateInputs(ctx, upper, identity, map[string]any{"approval": map[string]any{"action": "accept", "content": map[string]any{}}}); err != nil {
				t.Fatal(err)
			}
			_, task, err = runtime.get(ctx, id, identity)
			if err != nil || task.Status != "queued" || len(task.InputRequests) != 0 {
				t.Fatalf("uppercase update changed encryption identity: task=%+v error=%v", task, err)
			}
			if err := runtime.cancelTask(ctx, upper, identity); err != nil {
				t.Fatal(err)
			}
			wire, _, err = runtime.get(ctx, upper, identity)
			if err != nil || wire["status"] != "cancelled" {
				t.Fatalf("uppercase cancellation failed: wire=%v error=%v", wire, err)
			}
		})
	}
}
