package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func taskOutputTestSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"amount"}, "additionalProperties": false,
		"$defs":      map[string]any{"amount": map[string]any{"type": "integer", "minimum": 1}},
		"properties": map[string]any{"amount": map[string]any{"$ref": "#/$defs/amount"}}}
}

func TestTaskWorkersValidatePersistedOutputContracts(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
	key := mcpTaskHandlerKey{toolName: "build_report", version: "2"}
	for _, tc := range []struct {
		name   string
		result map[string]any
		status string
	}{
		{"wrong type", map[string]any{"content": []any{}, "structuredContent": map[string]any{"amount": "secret-output"}}, "failed"},
		{"missing structured output", map[string]any{"content": []any{}}, "failed"},
		{"missing required field", map[string]any{"content": []any{}, "structuredContent": map[string]any{}}, "failed"},
		{"constraint", map[string]any{"content": []any{}, "structuredContent": map[string]any{"amount": 0}}, "failed"},
		{"malformed result", map[string]any{"structuredContent": map[string]any{"amount": 2}}, "failed"},
		{"intermediate result", map[string]any{"content": []any{}, "resultType": "input_required"}, "failed"},
		{"valid", map[string]any{"content": []any{}, "structuredContent": map[string]any{"amount": 2}}, "completed"},
		{"tool error", map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": "business rejection"}}}, "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			contract := taskOutputTestSchema()
			accepted, err := runtime.create(ctx, "build_report", map[string]any{"report": "review", "steps": float64(1)}, nil, nil, contract)
			if err != nil {
				t.Fatal(err)
			}
			// A later application/catalog mutation must not weaken a queued promise.
			contract["required"] = []string{}
			task, err := runtime.claim(ctx)
			if err != nil || task == nil {
				t.Fatalf("claim %v", err)
			}
			if len(task.OutputSchema) == 0 {
				t.Fatal("task lost output schema")
			}
			runtime.taskExecutors[key] = func(context.Context, map[string]any, *MCPTaskExecutionContext) (map[string]any, error) {
				return tc.result, nil
			}
			runtime.runTask(ctx, task)
			wire, stored, err := runtime.get(ctx, accepted["taskId"].(string), nil)
			if err != nil {
				t.Fatal(err)
			}
			if wire["status"] != tc.status {
				t.Fatalf("status=%v want=%s", wire["status"], tc.status)
			}
			encoded, _ := json.Marshal(wire)
			if strings.Contains(string(encoded), "secret-output") {
				t.Fatal("invalid task output leaked")
			}
			if tc.status == "failed" && stored.Result != nil {
				t.Fatal("failed task retained invalid output")
			}
		})
	}
}

func TestTaskOutputContractSurvivesRetryInputAndWorkerReplacement(t *testing.T) {
	for _, pause := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry", true: "input"}[pause], func(t *testing.T) {
			runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{RetryBaseDelayMS: 100, RetryMaxDelayMS: 100})
			key := mcpTaskHandlerKey{toolName: "build_report", version: "2"}
			contract := taskOutputTestSchema()
			capabilities := map[string]any{"elicitation": map[string]any{"form": map[string]any{}}}
			accepted, err := runtime.create(ctx, "build_report", map[string]any{"report": "review", "steps": float64(1)}, nil, capabilities, contract)
			if err != nil {
				t.Fatal(err)
			}
			id := accepted["taskId"].(string)
			request := map[string]any{"method": "elicitation/create", "params": map[string]any{"message": "Approve?"}}
			runtime.taskExecutors[key] = func(_ context.Context, _ map[string]any, execution *MCPTaskExecutionContext) (map[string]any, error) {
				if pause {
					_, err := execution.RequestInput("approval", request)
					return nil, err
				}
				return nil, ErrRetryableMCPTask
			}
			task, err := runtime.claim(ctx)
			if err != nil || task == nil {
				t.Fatalf("claim %v", err)
			}
			runtime.runTask(ctx, task)
			if pause {
				if err := runtime.updateInputs(ctx, id, nil, map[string]any{"approval": map[string]any{"action": "accept", "content": map[string]any{}}}); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET next_attempt_at=clock_timestamp() WHERE task_id=$1`, id); err != nil {
					t.Fatal(err)
				}
			}
			contract["$defs"].(map[string]any)["amount"].(map[string]any)["type"] = "string"
			// A new worker has no dependency on the old process-local tool catalog.
			replacement := &mcpTaskRuntime{pool: runtime.pool, namespace: runtime.namespace, execution: runtime.execution,
				taskKeys: runtime.taskKeys, payloadEncryptionVersion: runtime.payloadEncryptionVersion,
				taskExecutors: map[mcpTaskHandlerKey]MCPTaskExecutor{key: func(_ context.Context, _ map[string]any, execution *MCPTaskExecutionContext) (map[string]any, error) {
					if pause {
						if _, err := execution.RequestInput("approval", request); err != nil {
							return nil, err
						}
					}
					return map[string]any{"content": []any{}, "structuredContent": map[string]any{"amount": 2}}, nil
				}}}
			task, err = replacement.claim(ctx)
			if err != nil || task == nil {
				t.Fatalf("replacement claim %v", err)
			}
			replacement.runTask(ctx, task)
			wire, _, err := runtime.get(ctx, id, nil)
			if err != nil {
				t.Fatal(err)
			}
			if wire["status"] != "completed" {
				t.Fatalf("lost persisted contract: %v", wire)
			}
		})
	}
}

func TestTaskOutputSchemaMigrationAndInvalidAdmission(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
	args := map[string]any{"report": "review", "steps": float64(1)}
	for _, schema := range []map[string]any{{"type": 42}, {"$ref": "https://provider.example/schema"}} {
		if _, err := runtime.create(ctx, "build_report", args, nil, nil, schema); err == nil {
			t.Fatal("invalid output schema was admitted")
		}
	}
	var count int
	if err := runtime.pool.QueryRow(ctx, `SELECT count(*) FROM gregale_mcp_tasks`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid schema left tasks: %d %v", count, err)
	}
	if _, err := runtime.pool.Exec(ctx, `ALTER TABLE gregale_mcp_tasks DROP COLUMN output_schema`); err != nil {
		t.Fatal(err)
	}
	if checkTaskSchema(ctx, runtime.pool) == nil {
		t.Fatal("old schema passed startup")
	}
	if err := migrateTaskSchema(ctx, runtime.pool); err != nil {
		t.Fatal(err)
	}
	if err := checkTaskSchema(ctx, runtime.pool); err != nil {
		t.Fatal(err)
	}
	if _, err := compileMCPTaskOutput(json.RawMessage(`{"$ref":"https://provider.example/schema"}`)); err == nil {
		t.Fatal("external schema resolved")
	}
	validator, err := compileMCPTaskOutput(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := validator([]byte(`{"content":[]}`)); err != nil {
		t.Fatal(err)
	}
}
