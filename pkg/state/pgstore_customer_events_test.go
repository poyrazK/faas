package state_test

import (
	"encoding/json"
	"slices"
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

// TestPg_ListCustomerEventsKindPrefixIsExact pins the indexed kind-prefix
// query (H10-43): the byte-order range may only narrow the scan, never
// change which rows match. Kinds on either side of the range and a longer
// kind that shares the prefix all appear in the account's history.
func TestPg_ListCustomerEventsKindPrefixIsExact(t *testing.T) {
	store, ctx := pgStore(t)
	accountID, _, otherAccountID, _ := seedTwoAppsPg(t, store, ctx,
		"audit-events-prefix@example.com", "audit-events-prefix-other@example.com",
		"audit-events-prefix", "audit-events-prefix-other")

	kinds := []string{
		"app.auto_rollback", "app.auto_rollback_on_5xx", "app.auto_rollbackz",
		"app.auto_rollbac", "app.auto_rollbacl", "app.created", "cron.fired",
	}
	for _, kind := range kinds {
		if err := store.AppendEvent(ctx, "test", kind, &accountID, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("AppendEvent %s: %v", kind, err)
		}
	}
	if err := store.AppendEvent(ctx, "test", "app.auto_rollback_on_5xx", &otherAccountID, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("AppendEvent other account: %v", err)
	}

	for _, tc := range []struct {
		prefix string
		want   []string
	}{
		{"app.auto_rollback", []string{"app.auto_rollback", "app.auto_rollback_on_5xx", "app.auto_rollbackz"}},
		{"app.", []string{"app.auto_rollbac", "app.auto_rollback", "app.auto_rollback_on_5xx", "app.auto_rollbackz", "app.auto_rollbacl", "app.created"}},
		{"cron.fired", []string{"cron.fired"}},
		{"deploy.", nil},
		{"", kinds},
	} {
		rows, err := store.ListCustomerEvents(ctx, state.CustomerEventFilter{
			AccountID: accountID, KindPrefix: tc.prefix, Limit: 50,
		})
		if err != nil {
			t.Fatalf("ListCustomerEvents(%q): %v", tc.prefix, err)
		}
		got := make([]string, 0, len(rows))
		for _, row := range rows {
			got = append(got, row.Kind)
		}
		slices.Sort(got)
		want := slices.Clone(tc.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("ListCustomerEvents(%q) kinds = %v, want %v", tc.prefix, got, want)
		}
	}
}
