package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeAsyncRouteInvocationStore struct {
	app         state.App
	invocations map[string]state.Invocation
	nextID      int
}

func (s *fakeAsyncRouteInvocationStore) AppByID(_ context.Context, id string) (state.App, error) {
	if id != s.app.ID {
		return state.App{}, state.ErrNotFound
	}
	return s.app, nil
}

func (s *fakeAsyncRouteInvocationStore) EnqueueInvocation(_ context.Context, invocation state.Invocation) (state.Invocation, error) {
	if s.invocations == nil {
		s.invocations = make(map[string]state.Invocation)
	}
	if invocation.ID == "" {
		s.nextID++
		invocation.ID = fmt.Sprintf("generated-%d", s.nextID)
	}
	if _, exists := s.invocations[invocation.ID]; exists {
		return state.Invocation{}, state.ErrConflict
	}
	s.invocations[invocation.ID] = invocation
	return invocation, nil
}

func (s *fakeAsyncRouteInvocationStore) InvocationByID(_ context.Context, id string) (state.Invocation, error) {
	invocation, ok := s.invocations[id]
	if !ok {
		return state.Invocation{}, state.ErrNotFound
	}
	return invocation, nil
}

func TestAsyncRouteEnqueuerPersistsInvocationEnvelope(t *testing.T) {
	store := &fakeAsyncRouteInvocationStore{app: state.App{
		ID:              "app_1",
		AccountID:       "acct_1",
		RetryPolicyJSON: json.RawMessage(`{"max_attempts":4}`),
	}}
	enqueuer := &asyncRouteEnqueuer{store: store}
	accepted, err := enqueuer.EnqueueAsyncRoute(t.Context(), gateway.AsyncRouteRequest{
		AppID: "app_1", AccountID: "acct_1", Method: "POST", Path: "/reports?format=pdf",
		Payload: json.RawMessage(`{"month":"2026-09"}`),
		Headers: map[string]string{"Content-Type": "application/json"},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	invocation := store.invocations[accepted.ID]
	if invocation.Source != state.InvocationAsyncInvoke || invocation.Method != "POST" || invocation.Path != "/reports?format=pdf" {
		t.Fatalf("invocation envelope = %+v", invocation)
	}
	if string(invocation.Payload) != `{"month":"2026-09"}` || string(invocation.RetryPolicyJSON) != `{"max_attempts":4}` {
		t.Errorf("payload/retry policy = %s / %s", invocation.Payload, invocation.RetryPolicyJSON)
	}
	var headers map[string]string
	if err := json.Unmarshal(invocation.Headers, &headers); err != nil {
		t.Fatalf("decode headers: %v", err)
	}
	if headers["Content-Type"] != "application/json" {
		t.Errorf("headers = %#v", headers)
	}
}

func TestAsyncRouteEnqueuerIdempotencyKeyReturnsExistingInvocation(t *testing.T) {
	store := &fakeAsyncRouteInvocationStore{app: state.App{ID: "app_1", AccountID: "acct_1", RetryPolicyJSON: json.RawMessage(`{}`)}}
	enqueuer := &asyncRouteEnqueuer{store: store}
	request := gateway.AsyncRouteRequest{
		AppID: "app_1", AccountID: "acct_1", Method: "POST", Path: "/reports",
		Payload: json.RawMessage(`{}`), IdempotencyKey: "monthly-report",
	}
	first, err := enqueuer.EnqueueAsyncRoute(t.Context(), request)
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	second, err := enqueuer.EnqueueAsyncRoute(t.Context(), request)
	if err != nil {
		t.Fatalf("second enqueue: %v", err)
	}
	if first.ID == "" || second.ID != first.ID {
		t.Fatalf("ids = %q and %q, want same non-empty id", first.ID, second.ID)
	}
	if len(store.invocations) != 1 {
		t.Fatalf("persisted invocations = %d, want 1", len(store.invocations))
	}
	if got := store.invocations[first.ID].RetryPolicyJSON; len(got) != 0 {
		t.Errorf("empty retry policy persisted as %s, want nil", got)
	}
}
