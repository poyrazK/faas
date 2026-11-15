package workflowcode_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/workflowcode"
)

// ADR-20261113120824 §2: a call is a new operation with a callee and JSON
// input; it waits up to the plan's maximum wait and is never re-reported. A
// pending call is a waiting journal entry that keeps a running run alive.
func TestValidateCallOperations(t *testing.T) {
	call := workflowcode.ReportedOp{Seq: 0, Name: "users", Kind: workflowcode.KindCall, Function: "users", Input: json.RawMessage(`{"email":"a@b"}`)}
	plan, err := workflowcode.Validate(workflowcode.Journal{Rows: map[string]workflowcode.JournalRow{}}, response("running", call), limits())
	if err != nil || len(plan.Ops) != 1 || !plan.Ops[0].New || plan.Ops[0].Descriptor.Function != "users" ||
		plan.Ops[0].Descriptor.Timeout() != 30*24*time.Hour {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	for name, op := range map[string]workflowcode.ReportedOp{
		"no function": {Seq: 0, Name: "users", Kind: workflowcode.KindCall},
		"bad input":   {Seq: 0, Name: "users", Kind: workflowcode.KindCall, Function: "users", Input: json.RawMessage(`{`)},
	} {
		if _, err := workflowcode.Validate(workflowcode.Journal{Rows: map[string]workflowcode.JournalRow{}}, response("running", op), limits()); !errors.Is(err, workflowcode.ErrProtocol) {
			t.Errorf("%s: err = %v, want a protocol error", name, err)
		}
	}

	pending := row("users", "pending", 0, workflowcode.KindCall, "")
	journal, err := workflowcode.BuildJournal([]workflowcode.Row{pending})
	if err != nil || journal.Entries[0].Status != workflowcode.EntryWaiting {
		t.Fatalf("journal = %+v, %v", journal.Entries, err)
	}
	if _, err := workflowcode.Validate(journal, response("running"), limits()); err != nil {
		t.Fatalf("a running run waiting on a call: %v", err)
	}
	if _, err := workflowcode.Validate(journal, response("running", call), limits()); !errors.Is(err, workflowcode.ErrNondeterministic) {
		t.Fatalf("re-reported call: %v", err)
	}
}
