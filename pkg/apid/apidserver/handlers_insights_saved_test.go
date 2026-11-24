package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// ADR-20261122190000 §1–§3: saved insights follow their platform flag, run
// over their relative range and export the same result as CSV.
func TestSavedInsightsLifecycle(t *testing.T) {
	e, token, customer := flagEventsRuntime(t, api.PlanPro)
	base := "/v1/projects/flags-events/environments/production/insights/saved"
	orders := api.SavedInsightRequest{Key: "orders", Kind: "events", Events: &api.InsightEventsQuery{Event: "order", Measure: "value"}}

	(*e.S()).EnablePlatformFlagForAll(platformflags.ProductAnalytics)
	if res := e.Do(t, http.MethodPost, base, orders, nil); res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "Saved insights") {
		t.Fatalf("create with saved_insights off = %d %s; want 403", res.Code, res.Body.String())
	}
	(*e.S()).EnablePlatformFlagForAll(platformflags.SavedInsights)

	now := time.Now().UTC()
	auth := map[string]string{"Authorization": "Bearer " + token}
	if res := e.Do(t, http.MethodPost, "/v1/runtime/flags/events", api.FlagEventsRequest{Events: []api.FlagEvent{
		{Name: "order", CustomerID: customer, Value: 1200, At: now.Add(-time.Hour)},
		{Name: "order", CustomerID: customer, Value: 300, At: now.Add(-30 * time.Minute)},
	}}, auth); res.Code != http.StatusOK {
		t.Fatalf("ingest = %d %s", res.Code, res.Body.String())
	}

	res := e.Do(t, http.MethodPost, base, orders, nil)
	var created api.SavedInsight
	if err := json.Unmarshal(res.Body.Bytes(), &created); res.Code != http.StatusCreated || err != nil {
		t.Fatalf("create = %d %s", res.Code, res.Body.String())
	}
	if created.Name != "orders" || created.RangeDays != 7 || created.AlertState != "ok" {
		t.Fatalf("created = %+v; want defaults name=key, 7 days, ok", created)
	}
	if res := e.Do(t, http.MethodPost, base, orders, nil); res.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d %s; want 409", res.Code, res.Body.String())
	}

	res = e.Do(t, http.MethodGet, base+"/orders/run", nil, nil)
	var run api.SavedInsightResult
	if err := json.Unmarshal(res.Body.Bytes(), &run); res.Code != http.StatusOK || err != nil {
		t.Fatalf("run = %d %s", res.Code, res.Body.String())
	}
	if run.Value == nil || *run.Value != 1500 || run.Events == nil {
		t.Fatalf("run = %+v; want value 1500 with the series", run)
	}
	res = e.Do(t, http.MethodGet, base+"/orders/run?format=csv", nil, nil)
	if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), "text/csv") ||
		!strings.HasPrefix(res.Body.String(), "start,breakdown,events,units,value\n") || !strings.Contains(res.Header().Get("Content-Disposition"), "orders.csv") {
		t.Fatalf("csv = %d %v %q", res.Code, res.Header(), res.Body.String())
	}

	// Replace: a 1-day funnel; the path key wins over an empty body key.
	funnel := api.SavedInsightRequest{Kind: "funnel", RangeDays: 1, Funnel: &api.InsightFunnelRequest{
		Steps: []api.InsightStepRequest{{Event: "signup"}, {Event: "order"}}}}
	if res := e.Do(t, http.MethodPut, base+"/orders", funnel, nil); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"kind":"funnel"`) {
		t.Fatalf("replace = %d %s", res.Code, res.Body.String())
	}

	for _, tc := range []struct {
		name string
		req  api.SavedInsightRequest
		want string
	}{
		{"foreign channel", api.SavedInsightRequest{Key: "x", Kind: "events", Events: &api.InsightEventsQuery{Event: "order"}, Digest: "daily",
			ChannelIDs: []string{"6f1c2c55-6a3f-4d7c-9a51-1d0c1f1b8e01"}}, "notification channels"},
		{"from on a funnel", api.SavedInsightRequest{Key: "x", Kind: "funnel", Funnel: &api.InsightFunnelRequest{From: &now,
			Steps: []api.InsightStepRequest{{Event: "a"}, {Event: "b"}}}}, "range_days"},
		{"no query", api.SavedInsightRequest{Key: "x", Kind: "events"}, "exactly one"},
	} {
		if res := e.Do(t, http.MethodPost, base, tc.req, nil); res.Code != http.StatusUnprocessableEntity || !strings.Contains(res.Body.String(), tc.want) {
			t.Errorf("%s = %d %s; want 422 naming %q", tc.name, res.Code, res.Body.String(), tc.want)
		}
	}

	res = e.Do(t, http.MethodGet, base, nil, nil)
	var list api.SavedInsightList
	if err := json.Unmarshal(res.Body.Bytes(), &list); res.Code != http.StatusOK || err != nil || len(list.Insights) != 1 {
		t.Fatalf("list = %d %s", res.Code, res.Body.String())
	}
	if res := e.Do(t, http.MethodDelete, base+"/orders", nil, nil); res.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", res.Code, res.Body.String())
	}
	if res := e.Do(t, http.MethodGet, base+"/orders", nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d %s; want 404", res.Code, res.Body.String())
	}
}
