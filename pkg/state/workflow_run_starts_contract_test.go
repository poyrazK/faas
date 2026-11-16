package state_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemWorkflowRunStartsContract(t *testing.T) {
	testWorkflowRunStarts(t, state.NewMemStore())
}

// ADR-20261113120824 §3: a run a function started records the starter once;
// a retried record keeps the first; a run no function started has none.
func testWorkflowRunStarts(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "starts-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(slug string) state.App {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug + "-" + uuid.NewString()[:8], Type: state.AppTypeFunction, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 2})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	workflow, starter, other := newApp("orders"), newApp("web"), newApp("admin")
	run := &state.WorkflowRun{AppID: workflow.ID, WorkflowName: "orders", Input: json.RawMessage(`{}`), DefinitionSnapshot: json.RawMessage(`{"name":"orders","steps":[]}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WorkflowRunStarter(ctx, run.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("starter before a start = %v, want ErrNotFound", err)
	}
	if err := store.RecordWorkflowRunStart(ctx, run.ID, starter.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordWorkflowRunStart(ctx, run.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := store.WorkflowRunStarter(ctx, run.ID); err != nil || got != starter.ID {
		t.Fatalf("starter = %q, %v, want the first starter", got, err)
	}
}
