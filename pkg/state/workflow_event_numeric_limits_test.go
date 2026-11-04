package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowEventRejectsUnsupportedNumericPayload(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		app, _ := state.SeedWorkflowSchedule(t, store, "allow")
		run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "test", DefinitionSnapshot: json.RawMessage(`{"name":"test","steps":[]}`)}
		if err := store.CreateWorkflowRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
		for _, payload := range []string{`{"value":1e200000}`, `{"value":1e-20000}`} {
			event := &state.WorkflowEvent{RunID: run.ID, EventName: "ready", Payload: json.RawMessage(payload)}
			if err := store.InsertWorkflowEvent(context.Background(), event); !errors.Is(err, state.ErrWorkflowInvalidInput) {
				t.Fatalf("payload %s: got %v", payload, err)
			}
		}
		events, err := store.GetWorkflowEventsForRun(context.Background(), run.ID)
		if err != nil || len(events) != 0 {
			t.Fatalf("invalid events stored: %v, %v", events, err)
		}
	})
}

func TestWorkflowEventRetryChecksPayload(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		app, _ := state.SeedWorkflowSchedule(t, store, "allow")
		run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "test", DefinitionSnapshot: json.RawMessage(`{"name":"test","steps":[]}`)}
		if err := store.CreateWorkflowRun(t.Context(), run); err != nil {
			t.Fatal(err)
		}
		id := uuid.NewString()
		original := &state.WorkflowEvent{ID: id, RunID: run.ID, EventName: "ready", Payload: json.RawMessage(`{"value":1}`)}
		if err := store.InsertWorkflowEvent(t.Context(), original); err != nil {
			t.Fatal(err)
		}
		matching := &state.WorkflowEvent{ID: id, RunID: run.ID, EventName: "ready", Payload: json.RawMessage(`{"value":1.0}`)}
		if err := store.InsertWorkflowEvent(t.Context(), matching); err != nil {
			t.Fatalf("matching retry: %v", err)
		}
		changed := &state.WorkflowEvent{ID: id, RunID: run.ID, EventName: "ready", Payload: json.RawMessage(`{"value":2}`)}
		if err := store.InsertWorkflowEvent(t.Context(), changed); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("changed retry: %v", err)
		}
		events, err := store.GetWorkflowEventsForRun(t.Context(), run.ID)
		var stored map[string]int
		if err != nil || len(events) != 1 || json.Unmarshal(events[0].Payload, &stored) != nil || stored["value"] != 1 {
			t.Fatalf("stored event: %+v, %v", events, err)
		}
		if err := store.MarkWorkflowRunStatus(t.Context(), run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := store.InsertWorkflowEvent(t.Context(), matching); err != nil {
			t.Fatalf("retained retry after completion: %v", err)
		}
		if err := store.InsertWorkflowEvent(t.Context(), &state.WorkflowEvent{RunID: run.ID, EventName: "new", Payload: json.RawMessage(`{}`)}); !errors.Is(err, state.ErrWorkflowNotRunning) {
			t.Fatalf("new event after completion: %v", err)
		}
	})
}

func TestWorkflowEventLegacyOversizedRetryIsPreserved(t *testing.T) {
	store := state.NewMemStore()
	app, _ := state.SeedWorkflowSchedule(t, store, "allow")
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "test", DefinitionSnapshot: json.RawMessage(`{"name":"test","steps":[]}`)}
	if err := store.CreateWorkflowRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`[` + strings.Repeat(`1e10000,`, 105) + `1e10000]`)
	if api.WorkflowEventEnvelopeFits(payload) {
		t.Fatal("legacy payload must exceed the new expansion limit")
	}
	id := uuid.NewString()
	expanded := "1" + strings.Repeat("0", 10000)
	stored := json.RawMessage(`[` + strings.Repeat(expanded+`,`, 105) + expanded + `]`)
	store.SeedLegacyWorkflowEvent(state.WorkflowEvent{ID: id, RunID: run.ID, EventName: "ready", Payload: stored, ReceivedAt: time.Now().UTC()})
	if err := store.InsertWorkflowEvent(t.Context(), &state.WorkflowEvent{ID: id, RunID: run.ID, EventName: "ready", Payload: payload}); err != nil {
		t.Fatalf("exact retained retry: %v", err)
	}
	if err := store.InsertWorkflowEvent(t.Context(), &state.WorkflowEvent{ID: id, RunID: run.ID, EventName: "ready", Payload: json.RawMessage(`{}`)}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed retry: %v", err)
	}
	if err := store.InsertWorkflowEvent(t.Context(), &state.WorkflowEvent{ID: id, RunID: run.ID, EventName: "ready", Payload: json.RawMessage(`1e200000`)}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed numeric retry: %v", err)
	}
}

func TestWorkflowCallbackRejectsUnsupportedNumericPayload(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		app, _ := state.SeedWorkflowSchedule(t, store, "allow")
		run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "test", DefinitionSnapshot: json.RawMessage(`{"name":"test","steps":[]}`)}
		if err := store.CreateWorkflowRun(t.Context(), run); err != nil {
			t.Fatal(err)
		}
		_, err := store.CompleteWorkflowCallback(t.Context(), run.ID, "await", "ready", uuid.NewString(), time.Hour, json.RawMessage(`{"value":1e200000}`))
		if !errors.Is(err, state.ErrWorkflowInvalidInput) {
			t.Fatalf("invalid callback: %v", err)
		}
		events, err := store.GetWorkflowEventsForRun(t.Context(), run.ID)
		if err != nil || len(events) != 0 {
			t.Fatalf("invalid callback stored: %+v, %v", events, err)
		}
	})
}
