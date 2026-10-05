package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const dispatchLogSecret = "request-secret-must-not-be-logged"

type dispatchLogEnqueueStore struct {
	*state.MemStore
	called bool
}

func (s *dispatchLogEnqueueStore) EnqueueInvocation(context.Context, state.Invocation) (state.Invocation, error) {
	s.called = true
	return state.Invocation{}, fmt.Errorf("Authorization: Bearer %s\r\nforged-entry: %w", dispatchLogSecret, state.ErrConflict)
}

func TestCronEnqueueFailureDoesNotLogRequestData(t *testing.T) {
	store := &dispatchLogEnqueueStore{MemStore: state.NewMemStore()}
	account, err := store.CreateAccount(t.Context(), "dispatch-log@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	newAppAndCron(t, store, account.ID, true)
	engine, _ := makeEngine(t, store, &fakeWakeVMM{})
	var output bytes.Buffer
	loop := NewLoop(nil, engine, slog.New(slog.NewJSONHandler(&output, nil))).
		WithClock(func() time.Time { return time.Date(2026, 7, 17, 12, 2, 0, 0, time.UTC) })
	loop.runCronTick(t.Context())
	if !store.called {
		t.Fatal("cron did not reach the failing enqueue")
	}
	assertDispatchLogRedacted(t, output.String(), map[string]string{"cron: enqueue invocation": "conflict"})
}

type dispatchLogEventStore struct {
	state.Store
	claimed  bool
	finished error
}

func (s *dispatchLogEventStore) ClaimDuePublishedEvent(context.Context, time.Time) (*state.PublishedEventWork, error) {
	if s.claimed {
		return nil, state.ErrNotFound
	}
	s.claimed = true
	return &state.PublishedEventWork{ID: 1, ClaimToken: "claim", Payload: []byte(`{"specversion":"1.0","id":"event-log","source":"orders","type":"created","time":"2026-10-05T00:00:00Z","datacontenttype":"application/json","accountid":"00000000-0000-4000-8000-000000000001","data":{}}`)}, nil
}

func (*dispatchLogEventStore) ListMatchingEventSubscriptionsForAccount(context.Context, string, string, string, state.EventSubscriptionCursor, int) ([]state.EventSubscription, error) {
	return nil, errors.New("Authorization: Bearer " + dispatchLogSecret + "\r\nforged-entry")
}

func (s *dispatchLogEventStore) FinishPublishedEvent(_ context.Context, _ int64, _ string, routeErr error) error {
	s.finished = routeErr
	return fmt.Errorf("%s\r\nforged-entry: %w", dispatchLogSecret, state.ErrConflict)
}

func TestEventFanoutFailureDoesNotLogRequestData(t *testing.T) {
	store := &dispatchLogEventStore{Store: state.NewMemStore()}
	var output bytes.Buffer
	loop := &Loop{engine: &Engine{store: store}, log: slog.New(slog.NewJSONHandler(&output, nil))}
	loop.runEventFanoutSweep(t.Context())
	if store.finished == nil || !strings.Contains(store.finished.Error(), dispatchLogSecret) {
		t.Fatal("fanout did not preserve the routing error for its retry decision")
	}
	assertDispatchLogRedacted(t, output.String(), map[string]string{
		"sched: event fanout failed":        "internal",
		"sched: finish event fanout failed": "conflict",
	})
}

func assertDispatchLogRedacted(t *testing.T, output string, expected map[string]string) {
	t.Helper()
	if strings.Contains(output, dispatchLogSecret) || strings.Contains(output, "forged-entry") || strings.Contains(output, "Authorization") {
		t.Fatal("dispatch logging exposed request-derived error text")
	}
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("invalid structured log: %v", err)
		}
		message, _ := entry["msg"].(string)
		if class, ok := expected[message]; ok {
			if entry["error_class"] != class || entry["err"] != nil {
				t.Fatalf("unsafe or unclassified dispatch error: %v", entry)
			}
			seen[message] = true
		}
	}
	if len(seen) != len(expected) {
		t.Fatalf("dispatch failure logs missing: saw %v", seen)
	}
}
