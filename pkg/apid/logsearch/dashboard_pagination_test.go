package logsearch

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Pagination must retain the same search window while the user reads a page.
func TestDashboardPaginationPreservesOriginalWindow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	acct := state.Account{ID: uuid.NewString(), Plan: api.PlanHobby}
	app := state.App{ID: uuid.NewString(), Slug: "shop"}
	store := state.NewMemStore()
	for i := 0; i < 101; i++ {
		at := now.Add(-time.Duration(i+1) * time.Second)
		if i == 100 {
			at = now.Add(-59*time.Minute - 30*time.Second)
		}
		_, err := store.InsertLogEvent(ctx, state.LogEvent{OccurredAt: at, AccountID: acct.ID, AppID: app.ID, Source: state.LogEventSourceRuntime, SourceEventID: fmt.Sprintf("page-event-%d", i), Message: fmt.Sprintf("event %d", i)})
		if err != nil {
			t.Fatal(err)
		}
	}
	svc := &Service{Host: fakeHost{store: store, app: app}, Enabled: true, Now: func() time.Time { return now }}
	first := svc.DashboardData(ctx, acct, app, url.Values{"since": {"1h"}})
	if first.Error != "" || first.Results == nil || len(first.Results.Events) != 100 || first.NextURL == "" {
		t.Fatalf("unexpected first page: %+v", first)
	}
	next, err := url.Parse(first.NextURL)
	if err != nil {
		t.Fatal(err)
	}
	control := svc.DashboardData(ctx, acct, app, next.Query())
	if control.Error != "" || control.Results == nil || len(control.Results.Events) != 1 {
		t.Fatalf("same-clock control: %+v", control)
	}
	now = now.Add(time.Minute)
	later := svc.DashboardData(ctx, acct, app, next.Query())
	if later.Error != "" || later.Results == nil || len(later.Results.Events) != 1 {
		t.Fatalf("next page lost an original event after clock advances: %+v", later)
	}
}
