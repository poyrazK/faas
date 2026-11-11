package main

import (
	"encoding/json"
	"testing"
)

func TestTaskElicitationAcceptsOmittedFormModeAndRejectsInvalidExplicitModes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params string
		valid  bool
	}{
		{"default form", `{"message":"Approve?","requestedSchema":{"type":"object","properties":{}}}`, true},
		{"explicit form", `{"mode":"form","message":"Approve?"}`, true},
		{"explicit url", `{"mode":"url","message":"Approve?"}`, true},
		{"null", `{"mode":null}`, false},
		{"empty", `{"mode":""}`, false},
		{"unknown", `{"mode":"invalid"}`, false},
		{"number", `{"mode":1}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var params map[string]any
			if err := json.Unmarshal([]byte(tc.params), &params); err != nil {
				t.Fatal(err)
			}
			requests := map[string]any{"approval": map[string]any{"method": "elicitation/create", "params": params}}
			if err := validateTaskInputRequests(requests); (err == nil) != tc.valid {
				t.Fatalf("validateTaskInputRequests() = %v, valid=%v", err, tc.valid)
			}
			encoded, err := json.Marshal(params)
			if err != nil {
				t.Fatal(err)
			}
			var original map[string]any
			if err := json.Unmarshal([]byte(tc.params), &original); err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(original)
			if string(encoded) != string(want) {
				t.Fatalf("validation mutated persisted request: %s, want %s", encoded, want)
			}
		})
	}
}

func TestTaskDefaultFormModeParksAndReplaysDurably(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
	identity := &authContext{resource: "https://example.com/mcp", subject: "alice", clientID: "test-client"}
	id := insertExecutionTestTask(t, runtime, ctx, string(runtime.ownerHashes(identity)[0]))
	if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET input_methods=ARRAY['elicitation/create:form'] WHERE task_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	task, err := runtime.claim(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim: %v", err)
	}
	request := map[string]any{"method": "elicitation/create", "params": map[string]any{"message": "Approve?"}}
	requests := map[string]any{"approval": request}
	if _, ready, err := runtime.requestTaskInputs(ctx, task, requests); err != nil || ready {
		t.Fatalf("default form did not park: ready=%v err=%v", ready, err)
	}
	wire, _, err := runtime.get(ctx, id, identity)
	if err != nil || wire["status"] != "input_required" {
		t.Fatalf("waiting task: wire=%v err=%v", wire, err)
	}
	response := map[string]any{"action": "accept", "content": map[string]any{}}
	if err := runtime.updateInputs(ctx, id, identity, map[string]any{"approval": response}); err != nil {
		t.Fatal(err)
	}
	task, err = runtime.claim(ctx)
	if err != nil || task == nil {
		t.Fatalf("resume claim: %v", err)
	}
	responses, ready, err := runtime.requestTaskInputs(ctx, task, requests)
	if err != nil || !ready {
		t.Fatalf("default form did not replay: ready=%v err=%v", ready, err)
	}
	if value, ok := responses["approval"].(map[string]any); !ok || value["action"] != "accept" {
		t.Fatalf("replayed response = %v", responses)
	}
}
