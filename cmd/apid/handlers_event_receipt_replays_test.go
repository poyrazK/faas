package main

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 583
func TestEventReceiptReplayRecoveryAndIdempotency(t *testing.T) {
	e := setup(t, api.PlanPro)
	work := seedAPIReceipt(t, e)
	ctx := context.Background()
	sub := work.RecipientSnapshot[1]
	root := state.PublishedEventInvocationID(e.acct.ID, "orders", "receipt-1", sub.ID)
	for i, recipient := range work.RecipientSnapshot[:2] {
		id := state.PublishedEventInvocationID(e.acct.ID, "orders", "receipt-1", recipient.ID)
		if _, err := e.store.EnqueueInvocation(ctx, state.Invocation{ID: id, AppID: recipient.AppID, AccountID: e.acct.ID, Source: state.InvocationAsyncInvoke, DueAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		terminal := "completed"
		if i == 1 {
			terminal = "failed"
		}
		if err := forceInvocationState(t, e, id, terminal); err != nil {
			t.Fatal(err)
		}
	}
	readReceipt := func() api.EventReceiptResponse {
		t.Helper()
		rec := e.do(t, "GET", eventReceiptURL("orders", "receipt-1"), nil, nil)
		var result api.EventReceiptResponse
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil {
			t.Fatalf("receipt: %d %s", rec.Code, rec.Body)
		}
		return result
	}
	initial := readReceipt()
	if len(initial.Recipients[1].RecoveryActions) != 1 {
		t.Fatalf("initial: %+v", initial.Recipients[1])
	}
	action := initial.Recipients[1].RecoveryActions[0]
	var first api.AsyncInvokeResponse
	for _, key := range []string{"receipt-replay-one", "receipt-replay-other", ""} {
		rec := e.do(t, "POST", action.URL, nil, map[string]string{"Idempotency-Key": key})
		var replay api.AsyncInvokeResponse
		if rec.Code != 202 || json.Unmarshal(rec.Body.Bytes(), &replay) != nil {
			t.Fatalf("replay: %d %s", rec.Code, rec.Body)
		}
		if first.ID != "" && first.ID != replay.ID {
			t.Fatalf("idempotent replay created extra invocation: %s %s", first.ID, replay.ID)
		}
		first = replay
	}
	pending := readReceipt().Recipients[1]
	if pending.Execution.State != "failed" || pending.Recovery == nil || pending.Recovery.RetainedReplayCount != 1 || pending.Recovery.LatestReplay.State != "pending" || len(pending.RecoveryActions) != 0 {
		t.Fatalf("pending replay: %+v", pending)
	}
	if err := forceInvocationState(t, e, first.ID, "failed"); err != nil {
		t.Fatal(err)
	}
	failed := readReceipt().Recipients[1]
	if len(failed.RecoveryActions) != 1 || failed.RecoveryActions[0].URL != "/v1/invocations/"+first.ID+"/replay" {
		t.Fatalf("recovery did not target latest replay: %+v", failed)
	}
	rec := e.do(t, "POST", failed.RecoveryActions[0].URL, nil, nil)
	var second api.AsyncInvokeResponse
	if rec.Code != 202 || json.Unmarshal(rec.Body.Bytes(), &second) != nil {
		t.Fatalf("second replay: %s", rec.Body)
	}
	if err := forceInvocationState(t, e, second.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	recovered := readReceipt()
	entry := recovered.Recipients[1]
	if recovered.Recipients[0].Execution.State != "completed" || recovered.Recipients[0].Recovery != nil || entry.Execution.State != "failed" || entry.Recovery.LatestReplay.State != "completed" || entry.Recovery.LatestReplay.ReplayedFromInvocationID != first.ID || len(entry.RecoveryActions) != 0 {
		t.Fatalf("independent recovery: %+v", recovered)
	}
	historyPath := entry.Recovery.HistoryURL
	var page api.EventReceiptReplayHistoryResponse
	rec = e.do(t, "GET", historyPath+"&limit=1", nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil || page.OriginalInvocationID != root || len(page.Replays) != 1 || page.Replays[0].InvocationID != second.ID || page.NextAfter == "" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("history: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, "GET", historyPath+"&after="+url.QueryEscape(page.NextAfter), nil, nil)
	var older api.EventReceiptReplayHistoryResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &older) != nil || len(older.Replays) != 1 || older.Replays[0].InvocationID != first.ID || older.NextAfter != "" {
		t.Fatalf("older history: %s", rec.Body)
	}
	// Pruning both descendants leaves the original failure visible, without
	// suggesting another child from a parent whose replay was already accepted.
	if _, err := e.store.DeleteInvocationsByIDs(ctx, []string{first.ID, second.ID}); err != nil {
		t.Fatal(err)
	}
	pruned := readReceipt().Recipients[1]
	if pruned.Execution.State != "failed" || pruned.Recovery != nil || len(pruned.RecoveryActions) != 0 {
		t.Fatalf("pruning offered redundant recovery: %+v", pruned)
	}
	if rec := e.do(t, "POST", action.URL, nil, nil); rec.Code != 409 {
		t.Fatalf("pruned recovery executed again: %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{eventReceiptReplayURL("orders", "receipt-1", work.RecipientSnapshot[0].ID) + "&after=" + url.QueryEscape(page.NextAfter), historyPath + "&after=err1.garbage", historyPath + "&limit=201", historyPath + "&after=" + url.QueryEscape(strings.Repeat("x", 8193))} {
		if rec := e.do(t, "GET", path, nil, nil); rec.Code != 400 {
			t.Fatalf("invalid history: %d %s", rec.Code, rec.Body)
		}
	}
}

// adr: 583
func TestEventReceiptReplayReadScope(t *testing.T) {
	for _, test := range []struct {
		scope  string
		status int
	}{{api.ScopeAppsRead, 404}, {api.ScopeEventsPublish, 403}, {api.ScopeUsageRead, 403}} {
		t.Run(test.scope, func(t *testing.T) {
			e := setupWithScopes(t, []string{test.scope})
			if rec := e.do(t, "GET", eventReceiptReplayURL("orders", "missing", "sub"), nil, nil); rec.Code != test.status {
				t.Fatalf("read scope: %d %s", rec.Code, rec.Body)
			}
		})
	}
}

// adr: 583
func TestEventReceiptReplayCursorIdentityAndRetention(t *testing.T) {
	history := state.EventReceiptReplayHistory{OutboxID: 1, EventSource: strings.Repeat("&", 256), EventID: strings.Repeat("<", 256), SubscriptionID: strings.Repeat(">", 256), NextCursor: state.EventReceiptReplayCursor{OutboxID: 1, CreatedAt: time.Now(), InvocationID: "00000000-0000-0000-0000-000000000001"}}
	raw := eventReceiptReplayResponse("account", history).NextAfter
	cursor, err := decodeEventReceiptReplayCursor(raw, "account", history.EventSource, history.EventID, history.SubscriptionID)
	if err != nil || cursor.OutboxID != 1 || cursor.InvocationID != history.NextCursor.InvocationID || !cursor.CreatedAt.Equal(history.NextCursor.CreatedAt) {
		t.Fatalf("maximum identity cursor: %d %+v %v", len(raw), cursor, err)
	}
	if _, err := decodeEventReceiptReplayCursor(raw, "foreign", history.EventSource, history.EventID, history.SubscriptionID); err == nil {
		t.Fatal("cross-account cursor accepted")
	}
}
