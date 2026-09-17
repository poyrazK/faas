package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestUnifiedDeadLetter_QueueListInspectAndReplay(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "dlq-app")
	invocationID := seedDeadLetterRow(t, e, appID, "poisoned")

	list := e.do(t, http.MethodGet, "/v1/apps/dlq-app/dlq", nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", list.Code, list.Body.String())
	}
	var page api.DeadLetterEventsResponse
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(page.Events))
	}
	event := page.Events[0]
	if event.Source != "invocation" || event.SourceID != invocationID {
		t.Fatalf("event source = %q/%q, want invocation/%q", event.Source, event.SourceID, invocationID)
	}
	if string(event.Payload) != `{"label":"poisoned"}` {
		t.Errorf("payload = %s, want original queue payload", event.Payload)
	}

	inspect := e.do(t, http.MethodGet, "/v1/apps/dlq-app/dlq/"+event.ID, nil, nil)
	if inspect.Code != http.StatusOK {
		t.Fatalf("inspect status = %d; body=%s", inspect.Code, inspect.Body.String())
	}

	replay := e.do(t, http.MethodPost, "/v1/apps/dlq-app/dlq/"+event.ID+"/replay", nil,
		map[string]string{"Idempotency-Key": "unified-dlq-replay-1"})
	if replay.Code != http.StatusAccepted {
		t.Fatalf("replay status = %d; body=%s", replay.Code, replay.Body.String())
	}
	var replayed api.DeadLetterEvent
	if err := json.Unmarshal(replay.Body.Bytes(), &replayed); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if replayed.ReplayedAt == nil {
		t.Fatal("replayed_at = nil, want replay timestamp")
	}

	inv, err := e.store.InvocationByID(t.Context(), invocationID)
	if err != nil {
		t.Fatalf("InvocationByID: %v", err)
	}
	if inv.State != state.InvocationPending || inv.Attempts != 0 {
		t.Fatalf("source after replay = state %q attempts %d, want pending/0", inv.State, inv.Attempts)
	}

	listAfterReplay := e.do(t, http.MethodGet, "/v1/apps/dlq-app/dlq", nil, nil)
	if listAfterReplay.Code != http.StatusOK {
		t.Fatalf("list after replay status = %d; body=%s", listAfterReplay.Code, listAfterReplay.Body.String())
	}
	var after api.DeadLetterEventsResponse
	if err := json.Unmarshal(listAfterReplay.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode list after replay: %v", err)
	}
	if len(after.Events) != 1 || after.Events[0].ReplayedAt == nil {
		t.Fatalf("list after replay = %+v, want one replayed event", after.Events)
	}
}

func TestUnifiedDeadLetter_AppScopePreventsCrossAppRead(t *testing.T) {
	e := setup(t, api.PlanPro)
	ownerAppID := mustSeedApp(t, e, "dlq-owner")
	mustSeedApp(t, e, "dlq-other")
	seedDeadLetterRow(t, e, ownerAppID, "owner-only")

	ownerPage := e.do(t, http.MethodGet, "/v1/apps/dlq-owner/dlq", nil, nil)
	if ownerPage.Code != http.StatusOK {
		t.Fatalf("owner list status = %d; body=%s", ownerPage.Code, ownerPage.Body.String())
	}
	var page api.DeadLetterEventsResponse
	if err := json.Unmarshal(ownerPage.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode owner list: %v", err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("owner events = %d, want 1", len(page.Events))
	}

	crossApp := e.do(t, http.MethodGet, "/v1/apps/dlq-other/dlq/"+page.Events[0].ID, nil, nil)
	if crossApp.Code != http.StatusNotFound {
		t.Fatalf("cross-app inspect status = %d, want 404; body=%s", crossApp.Code, crossApp.Body.String())
	}
}

func TestUnifiedDeadLetter_ReplayAllAndPurge(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "dlq-operator")
	first := seedDeadLetterRow(t, e, appID, "first")
	second := seedDeadLetterRow(t, e, appID, "second")

	replay := e.do(t, http.MethodPost, "/v1/apps/dlq-operator/dlq:replay_all?limit=1", nil,
		map[string]string{"Idempotency-Key": "unified-dlq-replay-all-1"})
	if replay.Code != http.StatusAccepted {
		t.Fatalf("replay-all status = %d; body=%s", replay.Code, replay.Body.String())
	}
	var batch api.DeadLetterReplayAllResponse
	if err := json.Unmarshal(replay.Body.Bytes(), &batch); err != nil {
		t.Fatalf("decode replay-all: %v", err)
	}
	if batch.Replayed != 1 {
		t.Fatalf("replayed = %d, want 1", batch.Replayed)
	}

	page := e.do(t, http.MethodGet, "/v1/apps/dlq-operator/dlq", nil, nil)
	var events api.DeadLetterEventsResponse
	if err := json.Unmarshal(page.Body.Bytes(), &events); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if len(events.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(events.Events))
	}

	// Purging the un-replayed source removes only the ledger projection.
	purgeID := ""
	purgeSourceID := ""
	for _, event := range events.Events {
		if event.ReplayedAt == nil {
			purgeID = event.ID
			purgeSourceID = event.SourceID
			break
		}
	}
	if purgeID == "" {
		t.Fatal("replay-all unexpectedly replayed both sources")
	}
	purge := e.do(t, http.MethodDelete, "/v1/apps/dlq-operator/dlq/"+purgeID, nil,
		map[string]string{"Idempotency-Key": "unified-dlq-purge-1"})
	if purge.Code != http.StatusNoContent {
		t.Fatalf("purge status = %d; body=%s", purge.Code, purge.Body.String())
	}
	inspect := e.do(t, http.MethodGet, "/v1/apps/dlq-operator/dlq/"+purgeID, nil, nil)
	if inspect.Code != http.StatusNotFound {
		t.Fatalf("purged inspect status = %d, want 404; body=%s", inspect.Code, inspect.Body.String())
	}
	inv, err := e.store.InvocationByID(t.Context(), purgeSourceID)
	if err != nil {
		t.Fatalf("source after purge: %v", err)
	}
	if inv.State != state.InvocationDeadLetter {
		t.Fatalf("source after purge = %q, want dead_letter", inv.State)
	}
	bulk := e.do(t, http.MethodDelete, "/v1/apps/dlq-operator/dlq?limit=10", nil,
		map[string]string{"Idempotency-Key": "unified-dlq-purge-all-1"})
	if bulk.Code != http.StatusOK {
		t.Fatalf("bulk purge status = %d; body=%s", bulk.Code, bulk.Body.String())
	}
	var purged api.DeadLetterPurgeResponse
	if err := json.Unmarshal(bulk.Body.Bytes(), &purged); err != nil {
		t.Fatalf("decode bulk purge: %v", err)
	}
	if purged.Purged != 1 {
		t.Fatalf("bulk purged = %d, want 1", purged.Purged)
	}
	_ = first
	_ = second // retain named sources to make the two-row setup explicit.
}
