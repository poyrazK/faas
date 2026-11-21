package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type invocationSendsTestStore = contractTestStore

func TestMemInvocationSendsContract(t *testing.T) {
	testInvocationSends(t, state.NewMemStore())
}

// ADR-20261112160000 §1 and §3: a sent invocation records its sender and
// completion app with the row; when it completes, the same transition
// enqueues a completion invocation of that app carrying the outcome, and the
// completion schedules none of its own. Invocations not sent have no record.
func testInvocationSends(t *testing.T, store invocationSendsTestStore) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "sends-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(slug string) state.App {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug + "-" + uuid.NewString()[:8], Type: state.AppTypeFunction,
			Status: state.AppActive, RAMMB: 256, MaxConcurrency: 2})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	caller, target := newApp("caller"), newApp("target")
	reader, ok := store.(state.InvocationSendReader)
	if !ok {
		t.Fatal("store does not read invocation sends")
	}

	plain, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: target.ID, AccountID: account.ID, Source: state.InvocationAsyncInvoke,
		Method: "POST", Path: "/", Payload: json.RawMessage(`{}`), DueAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.InvocationSend(ctx, plain.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("an invocation not sent has a send record: %v", err)
	}

	sent, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: target.ID, AccountID: account.ID, Source: state.InvocationAsyncInvoke,
		Method: "POST", Path: "/", Payload: json.RawMessage(`{"email":"a@b"}`), DueAt: time.Now().UTC(),
		SentByAppID: caller.ID, OnCompleteAppID: caller.ID})
	if err != nil {
		t.Fatal(err)
	}
	send, err := reader.InvocationSend(ctx, sent.ID)
	if err != nil || send.SenderAppID != caller.ID || send.OnCompleteAppID != caller.ID {
		t.Fatalf("send record = %+v, %v; want sender and completion %s", send, err, caller.ID)
	}

	if _, err := store.ClaimInvocation(ctx, sent.ID, "instance-1", 30); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteInvocation(ctx, sent.ID, json.RawMessage(`{"hello":"a@b"}`)); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueInvocations(ctx, time.Now().UTC().Add(time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	var completions []state.Invocation
	for _, inv := range due {
		var headers map[string]string
		_ = json.Unmarshal(inv.Headers, &headers)
		if inv.AppID == caller.ID && headers[state.InvocationCompletionHeader] == sent.ID {
			completions = append(completions, inv)
		}
	}
	if len(completions) != 1 {
		t.Fatalf("completions of %s = %d, want 1 (due: %d)", sent.ID, len(completions), len(due))
	}
	var body map[string]any
	if err := json.Unmarshal(completions[0].Payload, &body); err != nil {
		t.Fatal(err)
	}
	result, _ := body["result"].(map[string]any)
	if body["invocation_id"] != sent.ID || body["status"] != "succeeded" || result["hello"] != "a@b" {
		t.Fatalf("completion body = %v", body)
	}
	if _, err := reader.InvocationSend(ctx, completions[0].ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a completion was recorded as a send, so it would schedule another: %v", err)
	}
}
