// adr: 20261027125625 — the events flag pauses publishing with a retryable
// 503 on every customer publish path, and leaves the live-update stream alone.
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

func eventsPaused(t *testing.T, r *httptest.ResponseRecorder, scope string) {
	t.Helper()
	var problem api.Problem
	if r.Code != http.StatusServiceUnavailable || json.Unmarshal(r.Body.Bytes(), &problem) != nil ||
		problem.Code != api.CodeEventsPaused || r.Header().Get("Retry-After") != "30" || !strings.Contains(problem.Detail, scope) {
		t.Fatalf("got %d %s (Retry-After %q), want 503 events_paused mentioning %q", r.Code, r.Body, r.Header().Get("Retry-After"), scope)
	}
}

func TestEventsFlagPausesPublishing(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv := *e.S()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	acct := (*e.Acct()).ID
	if r := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "paused-producer", RAMMB: 256}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", r.Code, r.Body)
	}
	publish := func(id string) *httptest.ResponseRecorder {
		return e.Do(t, http.MethodPost, "/v1/events:publish", api.PublishEventRequest{ID: id, Source: "orders", Type: "order.created", Data: json.RawMessage(`{}`)}, nil)
	}
	batch := func() *httptest.ResponseRecorder {
		return e.Do(t, http.MethodPost, "/v1/events:publish-batch", map[string]any{"events": []api.PublishEventRequest{{ID: "b-1", Source: "orders", Type: "order.created", Data: json.RawMessage(`{}`)}}}, nil)
	}
	appPublish := func() *httptest.ResponseRecorder {
		return e.Do(t, http.MethodPost, "/v1/apps/paused-producer/events:publish", api.AppPublishEventRequest{Key: "k-1", Type: "order.created", Data: json.RawMessage(`{}`)}, nil)
	}

	// events is preview: off for an account without an allow rule.
	eventsPaused(t, publish("account-off"), "for this account")
	eventsPaused(t, batch(), "for this account")
	eventsPaused(t, appPublish(), "for this account")

	key := capabilityFlag(t, "events")
	allow := true
	srv.SeedPlatformFlag(key, platformflags.Definition{Enabled: true,
		Rules: []platformflags.Rule{{ID: "partner", Accounts: []string{acct}, Value: &allow}}})
	if r := publish("released"); r.Code >= 300 {
		t.Fatalf("publish with events released = %d %s", r.Code, r.Body)
	}

	srv.SeedPlatformFlag(key, platformflags.Definition{Enabled: false})
	eventsPaused(t, publish("killed"), "platform-wide")
	eventsPaused(t, batch(), "platform-wide")
	eventsPaused(t, appPublish(), "platform-wide")
	// Other events routes stay hidden, as for any switched-off capability.
	if r := e.Do(t, http.MethodGet, "/v1/events/retention", nil, nil); !missingRoute(r) {
		t.Fatalf("GET /v1/events/retention while killed = %d %s, want the missing-route answer", r.Code, r.Body)
	}
	// The live-update stream belongs to the dashboard, not to events.
	anonymous := httptest.NewRecorder()
	srv.Handler().ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/v1/events", nil))
	if anonymous.Code == http.StatusNotFound {
		t.Fatalf("GET /v1/events while events is killed = 404; the live-update stream must stay up")
	}
}

// The platform-tenant publish route is owned by platform-tenants, so it asks
// the events flag itself.
func TestPlatformTenantPublishFollowsTheEventsFlag(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv := *e.S()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	acct := *e.Acct()
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "tenant-producer", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	if p := srv.EventsPausedProblem(acct, app); p == nil || p.Code != api.CodeEventsPaused || !strings.Contains(p.Detail, "for this account") {
		t.Fatalf("events off for the account: problem = %+v", p)
	}
	key := capabilityFlag(t, "events")
	allow := true
	srv.SeedPlatformFlag(key, platformflags.Definition{Enabled: true,
		Rules: []platformflags.Rule{{ID: "partner", Accounts: []string{acct.ID}, Value: &allow}}})
	if p := srv.EventsPausedProblem(acct, app); p != nil {
		t.Fatalf("events released: problem = %+v, want none", p)
	}
	srv.SeedPlatformFlag(key, platformflags.Definition{Enabled: false})
	if p := srv.EventsPausedProblem(acct, app); p == nil || !strings.Contains(p.Detail, "platform-wide") {
		t.Fatalf("events killed: problem = %+v", p)
	}
}

// triggers:batch_create applies the same event_triggers check a source
// deploy does, so it cannot create subscriptions while they are off.
func TestBatchCreateRefusesEventTriggersWhileSubscriptionsAreOff(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv := *e.S()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	acct := (*e.Acct()).ID
	allow := true
	srv.SeedPlatformFlag(capabilityFlag(t, "event-triggers"), platformflags.Definition{Enabled: true,
		Rules: []platformflags.Rule{{ID: "partner", Accounts: []string{acct}, Value: &allow}}})
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: acct, Slug: "batch-fn", Type: state.AppTypeFunction})
	if err != nil {
		t.Fatal(err)
	}
	r := e.Do(t, http.MethodPost, "/v1/triggers:batch_create", api.CreateTriggerBatchRequest{
		AppID: app.ID, ManifestYAML: "event_triggers:\n  - source: billing.*\n    type: invoice.paid\n",
	}, nil)
	var out apidserver.BatchCreateResponse
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &out) != nil || len(out.Errors) != 1 || !strings.HasPrefix(out.Errors[0].Message, "event_triggers: ") {
		t.Fatalf("batch_create with event subscriptions off = %d %s", r.Code, r.Body)
	}
	subs, err := (*e.Store()).ListEventSubscriptionsForApp(t.Context(), app.ID)
	if err != nil || len(subs) != 0 {
		t.Fatalf("subscriptions = %+v, %v; want none", subs, err)
	}
}
