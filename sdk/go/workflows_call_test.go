package faas_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	faas "github.com/gregale-dev/gregale-go"
)

type signupUser struct {
	ID string `json:"id"`
}

var signupWorkflow = faas.DefineWorkflow("signup", func(_ context.Context, wf *faas.Workflow, input json.RawMessage) (any, error) {
	in, err := faas.WorkflowDecode[map[string]string](input)
	if err != nil {
		return nil, err
	}
	user, err := faas.WorkflowCall[signupUser](wf, "users", map[string]string{"email": in["email"]})
	if err != nil {
		return nil, err
	}
	if _, err := faas.WorkflowCall[any](wf, "billing", map[string]string{"user": user.ID}, faas.WithCallName("charge")); err != nil {
		var failed *faas.WorkflowCallError
		if errors.As(err, &failed) {
			return map[string]string{"user": user.ID, "billing": failed.Code}, nil
		}
		return nil, err
	}
	return map[string]string{"user": user.ID}, nil
})

// ADR-20261113120824 §2: WorkflowCall reports a call operation and suspends;
// a recorded call returns the callee's output or a *WorkflowCallError.
func TestWorkflowCallParksAndResumes(t *testing.T) {
	first := invoke(t, signupWorkflow, nil, map[string]string{"email": "a@b"})
	if len(first.Ops) != 1 || first.Ops[0].Kind != "call" || first.Ops[0].Function != "users" || string(first.Ops[0].Input) != `{"email":"a@b"}` || first.Result.Status != "running" {
		t.Fatalf("first = %+v", first)
	}
	done := invoke(t, signupWorkflow, []faas.WorkflowJournalEntry{
		{Seq: 0, Name: "users", Kind: "call", Status: "succeeded", Output: json.RawMessage(`{"id":"u1"}`)},
		{Seq: 1, Name: "charge", Kind: "call", Status: "failed", Error: "card declined"},
	}, map[string]string{"email": "a@b"})
	if done.Result.Status != "succeeded" || string(done.Result.Output) != `{"billing":"function_call_failed","user":"u1"}` {
		t.Fatalf("done = %+v %s", done.Result, done.Result.Output)
	}

	local, err := faas.RunWorkflowLocally(context.Background(), signupWorkflow, map[string]string{"email": "a@b"}, faas.WorkflowLocalRunOptions{
		Functions: map[string]func(json.RawMessage) (any, error){
			"users": func(json.RawMessage) (any, error) { return signupUser{ID: "u9"}, nil },
		},
	})
	if err != nil || local.Status != "succeeded" || string(local.Output) != `{"billing":"function_call_failed","user":"u9"}` {
		t.Fatalf("local = %+v %s %v", local, local.Output, err)
	}
}
