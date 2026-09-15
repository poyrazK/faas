package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreExecutionEventsAreOrderedAndAccountScoped(t *testing.T) {
	store := NewMemStore()
	account := executionTestAccount(t, store, "events")
	other := executionTestAccount(t, store, "events-other")
	createdAt := time.Now().UTC().Add(time.Second)
	row, err := store.CreateExecution(context.Background(), executionTestParams(t, account.ID, createdAt, 0, "sealed"))
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if _, err := store.AppendExecutionEvent(context.Background(), account.ID, row.ID, ExecutionEventStdout, json.RawMessage(`{"chunk":"hello"}`), createdAt.Add(time.Millisecond)); err != nil {
		t.Fatalf("AppendExecutionEvent: %v", err)
	}
	events, err := store.ListExecutionEvents(context.Background(), account.ID, row.ID, 0, 10)
	if err != nil {
		t.Fatalf("ListExecutionEvents: %v", err)
	}
	if len(events) != 2 || events[0].Type != ExecutionEventStatus || events[1].Type != ExecutionEventStdout || events[0].Sequence >= events[1].Sequence {
		t.Fatalf("events = %+v, want ordered queued/status + stdout", events)
	}
	resumed, err := store.ListExecutionEvents(context.Background(), account.ID, row.ID, events[0].Sequence, 10)
	if err != nil {
		t.Fatalf("resumed ListExecutionEvents: %v", err)
	}
	if len(resumed) != 1 || resumed[0].Sequence != events[1].Sequence {
		t.Fatalf("resumed = %+v, want only second event", resumed)
	}
	if _, err := store.ListExecutionEvents(context.Background(), other.ID, row.ID, 0, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account event read = %v, want ErrNotFound", err)
	}
}

func TestMemStoreExecutionTerminalEventIncludesBoundedOutput(t *testing.T) {
	store := NewMemStore()
	account := executionTestAccount(t, store, "events-terminal")
	base := time.Now().UTC().Add(time.Second)
	created, err := store.CreateExecution(context.Background(), executionTestParams(t, account.ID, base, 0, "sealed"))
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	claim, err := store.ClaimExecution(context.Background(), "events-test", base.Add(time.Millisecond), time.Minute)
	if err != nil {
		t.Fatalf("ClaimExecution: %v", err)
	}
	if _, err := store.MarkExecutionRunning(context.Background(), created.ID, *claim.LeaseToken, base.Add(2*time.Millisecond)); err != nil {
		t.Fatalf("MarkExecutionRunning: %v", err)
	}
	if _, err := store.CompleteExecution(context.Background(), CompleteExecutionParams{
		ID: created.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded,
		Result: json.RawMessage(`{"ok":true}`), Stdout: "hello\n", FinishedAt: base.Add(3 * time.Millisecond),
	}); err != nil {
		t.Fatalf("CompleteExecution: %v", err)
	}
	events, err := store.ListExecutionEvents(context.Background(), account.ID, created.ID, 0, 20)
	if err != nil {
		t.Fatalf("ListExecutionEvents: %v", err)
	}
	if events[len(events)-1].Type != ExecutionEventTerminal {
		t.Fatalf("last event = %+v, want terminal", events[len(events)-1])
	}
	for _, event := range events {
		if len(event.Payload) > ExecutionEventMaxBytes || !json.Valid(event.Payload) {
			t.Fatalf("invalid event payload: %+v", event)
		}
	}
}
