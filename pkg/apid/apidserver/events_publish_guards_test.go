package apidserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// An app.<UUID> source is that app's word: the Auth app's workflow starts are
// trusted by it. The account routes no longer let anyone in the account
// publish there; the app's producer route still does.
//
// adr: 20261026140000
func TestAccountPublishRoutesRefuseAppSources(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	if r := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "auth-like", RAMMB: 256}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", r.Code, r.Body)
	}
	app, err := (*e.Store()).AppBySlug(t.Context(), "auth-like")
	if err != nil {
		t.Fatal(err)
	}
	forged := []string{"app." + app.ID, "app." + strings.ToUpper(app.ID)}
	for i, source := range forged {
		event := api.PublishEventRequest{ID: fmt.Sprintf("start-%d", i), Source: source, Type: "auth.workflow.start", Data: json.RawMessage(`{"workflow":"payout"}`)}
		if r := e.Do(t, http.MethodPost, "/v1/events:publish", event, nil); r.Code != http.StatusBadRequest && r.Code != http.StatusUnprocessableEntity {
			t.Fatalf("account publish from %s = %d %s", source, r.Code, r.Body)
		}
		r := e.Do(t, http.MethodPost, "/v1/events:publish-batch", map[string]any{"events": []api.PublishEventRequest{event}}, nil)
		var batch api.PublishEventBatchResponse
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &batch) != nil || len(batch.Results) != 1 || batch.Results[0].Status != "rejected" || batch.Results[0].Retryable {
			t.Fatalf("batch publish from %s = %d %s", source, r.Code, r.Body)
		}
	}
	r := e.Do(t, http.MethodPost, "/v1/apps/auth-like/events:publish", api.AppPublishEventRequest{Key: "start-1", Type: "auth.workflow.start", Data: json.RawMessage(`{"workflow":"payout"}`)}, nil)
	var published api.AppPublishEventResponse
	if r.Code != http.StatusAccepted || json.Unmarshal(r.Body.Bytes(), &published) != nil || published.Source != "app."+uuid.MustParse(app.ID).String() {
		t.Fatalf("app producer route = %d %s", r.Code, r.Body)
	}
}

// Every publication path counts each event against the account's publish
// rate; past it the routes answer 429 with Retry-After and accept nothing.
//
// adr: 20261026140000
func TestEventPublishRateLimit(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	limits := api.MustLimitsFor(api.PlanFree).EventPublish
	if r := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "busy-producer", RAMMB: 256}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", r.Code, r.Body)
	}
	batch := func(prefix string) map[string]any {
		events := make([]api.PublishEventRequest, limits.Burst)
		for i := range events {
			events[i] = api.PublishEventRequest{ID: fmt.Sprintf("%s-%d", prefix, i), Source: "orders", Type: "order.created", Data: json.RawMessage(`{}`)}
		}
		return map[string]any{"events": events}
	}
	if r := e.Do(t, http.MethodPost, "/v1/events:publish-batch", batch("first"), nil); r.Code != http.StatusOK {
		t.Fatalf("batch within the burst = %d %s", r.Code, r.Body)
	}
	limited := func(name string, r interface {
		Header() http.Header
	}, code int, body string) {
		t.Helper()
		var problem api.Problem
		if code != http.StatusTooManyRequests || json.Unmarshal([]byte(body), &problem) != nil || problem.Code != api.CodeEventPublishRateLimited || r.Header().Get("Retry-After") == "" {
			t.Fatalf("%s past the rate = %d %s", name, code, body)
		}
	}
	r := e.Do(t, http.MethodPost, "/v1/events:publish-batch", batch("second"), nil)
	limited("batch", r, r.Code, r.Body.String())
	r = e.Do(t, http.MethodPost, "/v1/events:publish", api.PublishEventRequest{ID: "one-more", Source: "orders", Type: "order.created", Data: json.RawMessage(`{}`)}, nil)
	limited("publish", r, r.Code, r.Body.String())
	r = e.Do(t, http.MethodPost, "/v1/apps/busy-producer/events:publish", api.AppPublishEventRequest{Key: "one-more", Type: "order.created", Data: json.RawMessage(`{}`)}, nil)
	limited("app publish", r, r.Code, r.Body.String())
}
