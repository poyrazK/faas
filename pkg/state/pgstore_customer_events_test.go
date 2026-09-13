package state_test

import (
	"encoding/json"
	"testing"
	"time"

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
