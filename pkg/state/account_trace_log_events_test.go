package state_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// production-us hunt #7: `gregale trace` read each app's logs in its own
// round trip and ran past apid's request budget at 71 apps. The account-wide
// read must return exactly what the per-app reads did, newest first.
func TestMemStoreAccountTraceLogEvents(t *testing.T) {
	testAccountTraceLogEvents(t, state.NewMemStore(), context.Background())
}

func TestPgStoreAccountTraceLogEvents(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	testAccountTraceLogEvents(t, store, ctx)
}

func testAccountTraceLogEvents(t *testing.T, store state.LogEventStore, ctx context.Context) {
	t.Helper()
	accountID, appA, appB, appC := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	trace := "4bf92f3577b34da6a3ce929d0e0e4736"
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, e := range []struct {
		id      string
		age     time.Duration
		account string
		app     string
		source  state.LogEventSource
		trace   string
	}{
		{"a-newest", 1 * time.Second, accountID, appA, state.LogEventSourceHTTP, trace},
		{"b-middle", 2 * time.Second, accountID, appB, state.LogEventSourceHTTP, trace},
		{"a-oldest", 3 * time.Second, accountID, appA, state.LogEventSourceHTTP, trace},
		{"app-not-asked", time.Second, accountID, appC, state.LogEventSourceHTTP, trace},
		{"other-account", time.Second, uuid.NewString(), appA, state.LogEventSourceHTTP, trace},
		{"other-trace", time.Second, accountID, appA, state.LogEventSourceHTTP, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"runtime", time.Second, accountID, appB, state.LogEventSourceRuntime, trace},
		{"outside-window", 2 * time.Hour, accountID, appA, state.LogEventSourceHTTP, trace},
	} {
		if _, err := store.InsertLogEvent(ctx, state.LogEvent{
			OccurredAt: now.Add(-e.age), AccountID: e.account, AppID: e.app, Source: e.source,
			SourceEventID: e.id, TraceID: e.trace, Message: e.id,
		}); err != nil {
			t.Fatalf("InsertLogEvent %s: %v", e.id, err)
		}
	}
	ids := func(events []state.LogEvent) []string {
		out := make([]string, 0, len(events))
		for _, event := range events {
			out = append(out, event.SourceEventID)
		}
		return out
	}

	filter := state.AccountTraceLogFilter{
		AccountID: accountID, AppIDs: []string{appA, appB}, TraceID: trace,
		Source: state.LogEventSourceHTTP, Since: now.Add(-time.Hour), Until: now, Limit: 2,
	}
	page, hasMore, err := store.ListAccountTraceLogEvents(ctx, filter)
	if err != nil {
		t.Fatalf("ListAccountTraceLogEvents: %v", err)
	}
	if got, want := ids(page), []string{"a-newest", "b-middle"}; !reflect.DeepEqual(got, want) || !hasMore {
		t.Fatalf("limit 2 = %v hasMore=%v, want %v and more", got, hasMore, want)
	}
	filter.Limit = 10
	page, hasMore, err = store.ListAccountTraceLogEvents(ctx, filter)
	if err != nil {
		t.Fatalf("ListAccountTraceLogEvents: %v", err)
	}
	if got, want := ids(page), []string{"a-newest", "b-middle", "a-oldest"}; !reflect.DeepEqual(got, want) || hasMore {
		t.Fatalf("limit 10 = %v hasMore=%v, want %v and no more", got, hasMore, want)
	}
	filter.AppIDs = nil
	if page, _, err = store.ListAccountTraceLogEvents(ctx, filter); err != nil || len(page) != 0 {
		t.Fatalf("no apps = %v, %v; want nothing", ids(page), err)
	}
}
