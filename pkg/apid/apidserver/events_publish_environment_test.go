package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Every publish route stamps the environment it names, so only that
// environment's subscriptions receive the event; an environment of another
// account or project is refused.
//
// adr: 20261104120000
func TestPublishRoutesStampTheEventEnvironment(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, acct, ctx := *e.Store(), *e.Acct(), t.Context()
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	staging, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-api", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	production, _, err := store.UpsertEventSubscription(ctx, acct.ID, app.ID, "*", "*", nil)
	if err != nil {
		t.Fatal(err)
	}
	stage, _, err := store.UpsertEventSubscriptionInScope(ctx, acct.ID, app.ID, "staging", "*", "*", nil)
	if err != nil {
		t.Fatal(err)
	}
	reached := func(name string) string {
		t.Helper()
		work, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil || len(work.RecipientSnapshot) != 1 {
			t.Fatalf("%s: recipients %+v, %v", name, work.RecipientSnapshot, err)
		}
		return work.RecipientSnapshot[0].ID
	}
	event := func(environmentID string) api.PublishEventRequest {
		return api.PublishEventRequest{ID: uuid.NewString(), Source: "orders", Type: "order.created", Data: json.RawMessage(`{}`), EnvironmentID: environmentID}
	}

	if r := e.Do(t, http.MethodPost, "/v1/events:publish", event(staging.ID), nil); r.Code != http.StatusAccepted && r.Code != http.StatusCreated {
		t.Fatalf("stage publish = %d %s", r.Code, r.Body)
	}
	if got := reached("stage publish"); got != stage.ID {
		t.Fatalf("stage publish reached %s, want %s", got, stage.ID)
	}
	if r := e.Do(t, http.MethodPost, "/v1/events:publish", event(""), nil); r.Code != http.StatusAccepted && r.Code != http.StatusCreated {
		t.Fatalf("production publish = %d %s", r.Code, r.Body)
	}
	if got := reached("production publish"); got != production.ID {
		t.Fatalf("production publish reached %s, want %s", got, production.ID)
	}
	if r := e.Do(t, http.MethodPost, "/v1/events:publish", event(uuid.NewString()), nil); r.Code != http.StatusBadRequest && r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown environment publish = %d %s", r.Code, r.Body)
	}

	r := e.Do(t, http.MethodPost, "/v1/events:publish-batch", map[string]any{"events": []api.PublishEventRequest{event(staging.ID), event(uuid.NewString())}}, nil)
	var batch api.PublishEventBatchResponse
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &batch) != nil || len(batch.Results) != 2 ||
		batch.Results[0].Status != "accepted" || batch.Results[1].Status != "rejected" || batch.Results[1].Retryable {
		t.Fatalf("batch publish = %d %s", r.Code, r.Body)
	}
	if got := reached("stage batch publish"); got != stage.ID {
		t.Fatalf("stage batch publish reached %s, want %s", got, stage.ID)
	}

	appEvent := api.AppPublishEventRequest{Key: "k-1", Type: "order.created", Data: json.RawMessage(`{}`), Environment: "staging"}
	if r := e.Do(t, http.MethodPost, "/v1/apps/shop-api/events:publish", appEvent, nil); r.Code != http.StatusAccepted {
		t.Fatalf("stage app publish = %d %s", r.Code, r.Body)
	}
	if got := reached("stage app publish"); got != stage.ID {
		t.Fatalf("stage app publish reached %s, want %s", got, stage.ID)
	}
	appEvent.Key, appEvent.Environment = "k-2", "nope"
	if r := e.Do(t, http.MethodPost, "/v1/apps/shop-api/events:publish", appEvent, nil); r.Code != http.StatusBadRequest && r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown app environment publish = %d %s", r.Code, r.Body)
	}
}
