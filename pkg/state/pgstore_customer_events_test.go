package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPg_ListCustomerEventsScopesAnonymousRowsByOwnedApp(t *testing.T) {
	store, ctx := pgStore(t)
	accountA, appA, _, appB := seedTwoAppsPg(t, store, ctx,
		"audit-events-a@example.com", "audit-events-b@example.com",
		"audit-events-a", "audit-events-b")

	appendJSON := func(subject *string, kind string, data map[string]any) {
		t.Helper()
		payload, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(ctx, "test", kind, subject, payload); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	appendJSON(&accountA, "key.created", map[string]any{"key_id": "k"})
	appendJSON(nil, "wake.proxy_first_byte", map[string]any{"app_id": appA})
	appendJSON(nil, "wake.proxy_first_byte", map[string]any{"app_id": appB})
	appendJSON(nil, "wake.unattributed", map[string]any{"wake_id": "orphan"})

	rows, err := store.ListCustomerEvents(ctx, state.CustomerEventFilter{
		AccountID: accountA, IncludeAnonymous: true, KindPrefix: "wake.", Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListCustomerEvents: %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != "wake.proxy_first_byte" {
		t.Fatalf("customer rows = %+v, want one owned anonymous wake", rows)
	}
	var data map[string]any
	if err := json.Unmarshal(rows[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["app_id"] != appA {
		t.Fatalf("app_id = %v, want %s", data["app_id"], appA)
	}

	filtered, err := store.ListCustomerEvents(ctx, state.CustomerEventFilter{
		AccountID: accountA, IncludeAnonymous: true, AppID: appA,
		Since: time.Now().Add(-time.Minute), Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListCustomerEvents filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Subject != nil {
		t.Fatalf("filtered rows = %+v, want owned app's anonymous event", filtered)
	}
}

// spec: §12 — customer audit reads must return an empty page for an unknown
// app without turning a bounded lookup into a capacity error (issue #2688).
// TestPg_ListCustomerEventsUnknownAppReturnsEmpty exercises the app-scoped
// miss path from issue #2688. The filter is validated before the indexed
// candidate query, so an unknown app cannot force a scan of account history.
func TestPg_ListCustomerEventsUnknownAppReturnsEmpty(t *testing.T) {
	store, ctx := pgStore(t)
	accountID, _, _, _ := seedTwoAppsPg(t, store, ctx,
		"audit-events-miss@example.com", "audit-events-other@example.com",
		"audit-events-miss", "audit-events-other")

	rows, err := store.ListCustomerEvents(ctx, state.CustomerEventFilter{
		AccountID: accountID,
		AppID:     uuid.NewString(),
		Limit:     1,
	})
	if err != nil {
		t.Fatalf("ListCustomerEvents unknown app: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("unknown app rows = %+v, want empty", rows)
	}
}
