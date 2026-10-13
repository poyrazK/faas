package apidserver_test

// adr: 1152

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func promVector(rows ...string) string {
	return `{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(rows, ",") + `]}}`
}

func promRow(labels map[string]string, value float64) string {
	raw, _ := json.Marshal(labels)
	return fmt.Sprintf(`{"metric":%s,"value":[1728000000,"%g"]}`, raw, value)
}

// fakeRouteEvidence answers the four ADR-1152 queries for one caller app.
func fakeRouteEvidence(t *testing.T, target, foreign string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(q, "gateway_service_wake_ahead_edge_share"):
			_, _ = fmt.Fprint(w, promVector(
				promRow(map[string]string{"target_app": target, "kind": "call"}, 0.9),
				promRow(map[string]string{"target_app": target, "kind": "cold"}, 0.8),
				promRow(map[string]string{"target_app": foreign, "kind": "call"}, 0.9),
			))
		case strings.Contains(q, "gateway_service_wake_ahead_caller_wakes"):
			_, _ = fmt.Fprint(w, promVector(promRow(map[string]string{}, 40)))
		case strings.Contains(q, "gateway_service_wake_ahead_edge_total"):
			_, _ = fmt.Fprint(w, promVector(
				promRow(map[string]string{"target_app": target, "outcome": "started"}, 12),
				promRow(map[string]string{"target_app": target, "outcome": "used"}, 10.4),
			))
		case strings.Contains(q, "gateway_route_priority_queue_total"):
			_, _ = fmt.Fprint(w, promVector(
				promRow(map[string]string{"class": "normal", "outcome": "rejected"}, 9),
				promRow(map[string]string{"class": "normal", "outcome": "displaced"}, 6),
				promRow(map[string]string{"class": "critical", "outcome": "queued"}, 20),
			))
		default:
			t.Errorf("unexpected query %q", q)
			_, _ = fmt.Fprint(w, promVector())
		}
	}))
}

func seedForeignApp(t *testing.T, e apidserver.TestEnv) string {
	t.Helper()
	store := *e.Store()
	other, err := store.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: other.ID, Slug: "foreign-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	return app.ID
}

func TestRouteFeatureEvidence(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "api")
	auth := apidserver.MustSeedApp(t, e, "auth")
	prom := fakeRouteEvidence(t, auth, seedForeignApp(t, e))
	defer prom.Close()
	(*(*e.S()).PromqlClient()) = promql.NewClient(prom.URL, prom.Client())

	res := e.Do(t, "GET", "/v1/apps/api/service-wake-ahead", nil, nil)
	var wake api.ServiceWakeAheadResponse
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &wake) != nil || wake.Learned == nil {
		t.Fatalf("%d: %s", res.Code, res.Body.String())
	}
	l := wake.Learned
	if l.Source != api.RouteEvidenceSourceLive || l.CallerWakes != 40 || len(l.Targets) != 1 {
		t.Fatalf("learned = %+v; the foreign target must be hidden", l)
	}
	if got := l.Targets[0]; got.Slug != "auth" || got.CallShare != 0.9 || got.ColdShare != 0.8 || !got.Qualifies || got.Started != 12 || got.Used != 10 {
		t.Fatalf("target = %+v", got)
	}

	res = e.Do(t, "GET", "/v1/apps/api/route-priorities", nil, nil)
	var prio api.RoutePrioritiesResponse
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &prio) != nil || prio.Queue == nil {
		t.Fatalf("%d: %s", res.Code, res.Body.String())
	}
	if q := prio.Queue; q.Source != api.RouteEvidenceSourceLive || len(q.Classes) != 3 || q.Classes[0].Queued != 20 || q.Classes[1].Rejected != 9 || q.Classes[1].Displaced != 6 {
		t.Fatalf("queue = %+v", prio.Queue)
	}
	res = e.Do(t, "PUT", "/v1/apps/api/route-priorities", api.SetRoutePrioritiesRequest{Routes: []api.RoutePriorityRule{}}, nil)
	if !strings.Contains(res.Body.String(), `"source":"configured"`) || strings.Contains(res.Body.String(), `"queue"`) {
		t.Fatalf("writes answer without queue evidence: %s", res.Body.String())
	}
}

func TestRouteFeatureEvidenceUnavailableWithoutPrometheus(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "api")
	res := e.Do(t, "GET", "/v1/apps/api/service-wake-ahead", nil, nil)
	var wake api.ServiceWakeAheadResponse
	if json.Unmarshal(res.Body.Bytes(), &wake) != nil || wake.Learned == nil || wake.Learned.Source != api.RouteEvidenceSourceUnavailable || wake.Learned.Targets == nil {
		t.Fatalf("learned = %s", res.Body.String())
	}
	res = e.Do(t, "GET", "/v1/apps/api/route-priorities", nil, nil)
	var prio api.RoutePrioritiesResponse
	if json.Unmarshal(res.Body.Bytes(), &prio) != nil || prio.Queue == nil || prio.Queue.Source != api.RouteEvidenceSourceUnavailable {
		t.Fatalf("queue = %s", res.Body.String())
	}
}

func TestRouteAdviceIncludesSettingSuggestions(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "shop")
	auth := apidserver.MustSeedApp(t, e, "auth")
	prom := fakeRouteEvidence(t, auth, uuid.NewString())
	defer prom.Close()
	store := routeAdviceFixture(e)
	store.statRows = append(store.statRows, sqlc.RouteAdviceRouteStatsRow{Route: "POST /checkout", Method: "POST", Requests: 500})
	(*(*e.S()).Store()) = statetest.NewMem(store)
	(*(*e.S()).PromqlClient()) = promql.NewClient(prom.URL, prom.Client())
	out := getRouteAdviceResponse(t, e, "/v1/apps/shop/routes/advice")
	var kinds []string
	var prio *api.RouteAdviceSuggestion
	for i, s := range out.Suggestions {
		kinds = append(kinds, s.Kind)
		if s.Kind == api.RouteAdviceKindPriority {
			prio = &out.Suggestions[i]
		}
	}
	if !strings.Contains(strings.Join(kinds, ","), api.RouteAdviceKindWakeAhead) || prio == nil {
		t.Fatalf("kinds = %v, want wake_ahead and priority suggestions", kinds)
	}
	if rules := prio.Setting.RoutePriorities; len(rules) != 1 || rules[0].Path != "/checkout" || rules[0].Class != api.RoutePriorityCritical {
		t.Fatalf("priority setting = %+v", prio.Setting)
	}
}
