//go:build !no_pg

package apidserver_test

// adr: 20261028095548

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEventReplayBackfillCancelPostgresHTTP(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	s := (*e.Store()).(*state.PgStore)
	ctx := context.Background()
	account := (*e.Acct()).ID
	app, err := s.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account, Slug: "backfill-cancel", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	published := e.Do(t, http.MethodPost, "/v1/events:publish", api.PublishEventRequest{ID: "backfill-cancel", Source: "orders", Type: "created", Data: json.RawMessage(`{}`)}, nil)
	if published.Code != http.StatusAccepted {
		t.Fatalf("publish=%d %s", published.Code, published.Body)
	}
	parent, err := s.ClaimDuePublishedEvent(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishPublishedEvent(ctx, parent.ID, parent.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	sub, _, err := s.UpsertEventSubscription(ctx, account, app.ID, "orders", "created", nil)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.CreateEventReplayBackfill(ctx, account, state.EventReplayBackfillQuery{AppID: app.ID, SubscriptionID: sub.ID, EventReplayBackfillRequest: api.EventReplayBackfillRequest{From: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if worked, err := s.ProcessNextEventReplayBackfill(ctx, time.Now()); err != nil || !worked {
			t.Fatalf("scan=%t %v", worked, err)
		}
	}
	for attempt := range 2 {
		rec := e.Do(t, http.MethodPost, "/v1/event-replays/"+job.ID+"/cancel", nil, nil)
		var got api.EventReplayBackfillJobResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.State != "cancelled" || got.Progress.Cancelled != 1 || got.Progress.Pending != 0 {
			t.Fatalf("cancel %d = %d %s", attempt, rec.Code, rec.Body)
		}
	}
	if rec := e.Do(t, http.MethodPost, "/v1/event-replays/"+job.ID+"/retry-failed", map[string]int{"limit": 10}, nil); rec.Code != http.StatusConflict {
		t.Fatalf("retry of a cancelled job = %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodPost, "/v1/event-replays/"+uuid.NewString()+"/cancel", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("cancel of an unknown job = %d %s", rec.Code, rec.Body)
	}
}
