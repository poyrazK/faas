package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedEventRouteDeadLetter(store *state.MemStore, accountID, appID string, failedAt time.Time) string {
	id := uuid.NewString()
	store.SeedDeadLetterEventForTest(state.DeadLetterEvent{
		ID: id, AccountID: accountID, AppID: appID, Source: state.DeadLetterSourceEventRoute,
		SourceID: uuid.NewString(), Origin: "event_subscription",
		Payload:     json.RawMessage(`{"id":"evt-1","source":"orders","type":"order.created","data":{}}`),
		Headers:     json.RawMessage(`{}`),
		ErrorKind:   "target_unavailable",
		ErrorDetail: json.RawMessage(`{"subscription_id":"sub","last_error":"app is deleted"}`),
		RetryCount:  12, FirstFailedAt: failedAt, LastFailedAt: failedAt, CreatedAt: failedAt,
	})
	return id
}

// A routing failure whose receipt was pruned (ADR-20261026163443) is listed
// and can be discarded, but replay answers 409 and replay-all passes it by.
//
// adr: 20261026163443
func TestEventRouteDeadLetterRefusesReplay(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	if r := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "route-target", RAMMB: 256}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", r.Code, r.Body)
	}
	store := *e.Store()
	app, err := store.AppBySlug(t.Context(), "route-target")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	inv, err := store.EnqueueInvocation(t.Context(), state.Invocation{
		AccountID: e.Acct().ID, AppID: app.ID, Source: state.InvocationQueue, State: state.InvocationDeadLetter,
		DueAt: now, Attempts: 3, LastError: "worker failed", Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	seedEventRouteDeadLetter(store, e.Acct().ID, app.ID, now.Add(time.Minute))
	seedEventRouteDeadLetter(store, e.Acct().ID, "", now.Add(2*time.Minute))

	r := e.Do(t, http.MethodGet, "/v1/apps/route-target/dlq", nil, nil)
	var page api.DeadLetterEventsResponse
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &page) != nil || len(page.Events) != 2 || page.Events[0].Source != state.DeadLetterSourceEventRoute {
		t.Fatalf("app dlq list = %d %s", r.Code, r.Body)
	}
	appRoute := page.Events[0].ID
	r = e.Do(t, http.MethodGet, "/v1/account/dlq", nil, nil)
	var account api.AccountDeadLetterEventsResponse
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &account) != nil || len(account.Events) != 3 || account.Events[0].Source != state.DeadLetterSourceEventRoute || account.Events[0].AppID != "" {
		t.Fatalf("account dlq list = %d %s", r.Code, r.Body)
	}
	accountRoute := account.Events[0].ID

	for _, path := range []string{"/v1/apps/route-target/dlq/" + appRoute + "/replay", "/v1/account/dlq/" + appRoute + "/replay", "/v1/account/dlq/" + accountRoute + "/replay"} {
		r := e.Do(t, http.MethodPost, path, nil, nil)
		var problem api.Problem
		if r.Code != http.StatusConflict || json.Unmarshal(r.Body.Bytes(), &problem) != nil || problem.Code != api.CodeDeadLetterNotReplayable {
			t.Fatalf("POST %s = %d %s", path, r.Code, r.Body)
		}
	}
	r = e.Do(t, http.MethodPost, "/v1/apps/route-target/dlq:replay_all?limit=1", nil, nil)
	var replayed api.DeadLetterReplayAllResponse
	if r.Code != http.StatusAccepted || json.Unmarshal(r.Body.Bytes(), &replayed) != nil || replayed.Replayed != 1 {
		t.Fatalf("app replay-all past the event_route row = %d %s", r.Code, r.Body)
	}
	if after, err := store.InvocationByID(t.Context(), inv.ID); err != nil || after.State != state.InvocationPending {
		t.Fatalf("invocation after replay-all = %+v, %v", after, err)
	}
	r = e.Do(t, http.MethodPost, "/v1/account/dlq:replay_all", nil, nil)
	var accountReplayed api.AccountDeadLetterReplayAllResponse
	if r.Code != http.StatusAccepted || json.Unmarshal(r.Body.Bytes(), &accountReplayed) != nil || accountReplayed.Replayed != 0 {
		t.Fatalf("account replay-all with only event_route rows open = %d %s", r.Code, r.Body)
	}
	for _, path := range []string{"/v1/apps/route-target/dlq/" + appRoute, "/v1/account/dlq/" + accountRoute} {
		if r := e.Do(t, http.MethodDelete, path, nil, nil); r.Code >= 300 {
			t.Fatalf("DELETE %s = %d %s", path, r.Code, r.Body)
		}
	}
	metrics := apidserver.ScrapeOpsMetrics(t, *e.Ops())
	if strings.Contains(metrics, `faas_dlq_replayed_total{app="route-target",status="error"}`) {
		t.Fatalf("refused event_route replay counted as an error:\n%s", metrics)
	}
}

// The dashboard offers only Discard for an event_route entry, and a selected
// replay that includes one changes nothing.
//
// adr: 20261026163443
func TestDashboardFailedEvents_EventRouteEntriesOnlyDiscard(t *testing.T) {
	env := newDashboardFailedEventsTestEnv(t)
	app, err := env.store.CreateApp(t.Context(), state.App{
		AccountID: env.account.ID, Slug: "dashboard-route", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	inv, invID := seedDashboardFailedInvocation(t, env.store, env.account.ID, app.ID)
	routeID := seedEventRouteDeadLetter(env.store, env.account.ID, app.ID, time.Now().UTC().Add(time.Minute))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/failed-events?app="+app.Slug, nil)
	req.AddCookie(env.session)
	env.h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "/dashboard/failed-events/dashboard-route/"+routeID+"/discard") ||
		strings.Contains(body, "/dashboard/failed-events/dashboard-route/"+routeID+"/replay") ||
		!strings.Contains(body, "/dashboard/failed-events/dashboard-route/"+invID+"/replay") || !strings.Contains(body, "Re-publish to retry") {
		t.Fatalf("failed events page = %d; want discard-only controls for the event_route row:\n%s", rec.Code, body)
	}

	csrf := dashboardFailedEventsCSRF(t, env)
	replayed := dashboardPOSTForm(t, env.h, env.session, "/dashboard/failed-events/replay-selected", url.Values{
		middleware.FormFieldName: []string{csrf.Value},
		"app":                    []string{app.Slug},
		"event_id":               []string{invID, routeID},
	}, csrf)
	if loc := replayed.Header().Get("Location"); replayed.Code != http.StatusSeeOther || !strings.Contains(loc, "action=error") {
		t.Fatalf("selected replay with an event_route row = %d %q", replayed.Code, loc)
	}
	if after, err := env.store.InvocationByID(t.Context(), inv.ID); err != nil || after.State != state.InvocationDeadLetter {
		t.Fatalf("invocation after refused selected replay = %+v, %v", after, err)
	}
}
