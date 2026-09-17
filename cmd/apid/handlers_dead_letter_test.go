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
