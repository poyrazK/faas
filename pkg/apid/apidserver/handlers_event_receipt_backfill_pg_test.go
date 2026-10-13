//go:build !no_pg

package apidserver_test

// adr: 646

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/appevents"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEventReceiptBackfillRecoveryPostgresHTTP(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	s := (*e.Store()).(*state.PgStore)
	ctx := context.Background()
	app, err := s.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: (*e.Acct()).ID, Slug: "backfill-http", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	published := e.Do(t, http.MethodPost, "/v1/events:publish", api.PublishEventRequest{ID: "backfill-http", Source: "orders", Type: "created", Data: json.RawMessage(`{}`)}, nil)
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
	sub, _, err := s.UpsertEventSubscription(ctx, (*e.Acct()).ID, app.ID, "orders", "created", nil)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.CreateEventReplayBackfill(ctx, (*e.Acct()).ID, state.EventReplayBackfillQuery{AppID: app.ID, SubscriptionID: sub.ID, EventReplayBackfillRequest: api.EventReplayBackfillRequest{From: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if worked, err := s.ProcessNextEventReplayBackfill(ctx, time.Now()); err != nil || !worked {
			t.Fatalf("scan=%t %v", worked, err)
		}
	}
	work, err := s.ClaimDuePublishedEventRecipient(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishPublishedEventRecipient(ctx, work, state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientFailed, Attempts: work.TotalAttempts, UpdatedAt: time.Now(), FailureCode: state.EventFanoutFailureCodeInvocationEnqueueFailed, Retryable: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodGet, appevents.EventReceiptURL("orders", "backfill-http"), nil, nil)
	var receipt api.EventReceiptResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &receipt) != nil || receipt.RecipientCount != 0 || receipt.BackfillRecipientCount != 1 || len(receipt.Recipients) != 1 {
		t.Fatalf("receipt=%d %s", rec.Code, rec.Body)
	}
	entry := receipt.Recipients[0]
	if entry.Origin != "backfill" || entry.BackfillJobID != job.ID || entry.BackfillJobURL == "" || len(entry.RecoveryActions) != 1 || entry.RecoveryActions[0].Kind != "routing_replay" {
		t.Fatalf("backfill evidence=%+v", entry)
	}
	action := entry.RecoveryActions[0]
	replay := e.Do(t, action.Method, action.URL, action.Body, nil)
	if replay.Code != http.StatusAccepted {
		t.Fatalf("recovery=%d %s", replay.Code, replay.Body)
	}
	for _, link := range []string{entry.AttemptHistoryURL, entry.FanoutHistoryURL, entry.BackfillJobURL} {
		r := e.Do(t, http.MethodGet, link, nil, nil)
		if r.Code != http.StatusOK {
			t.Fatalf("inspection link=%s status=%d body=%s", link, r.Code, r.Body)
		}
	}
}
