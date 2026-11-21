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

// ADR 20261117084505 §1, §3, §4: with product_analytics on, runtime events
// keep their subject, properties and flag decisions and answer series and
// funnels; invalid analytics fields drop the event; with the flag off the
// insights routes refuse and no raw event is kept.
func TestInsightsFromRuntimeEvents(t *testing.T) {
	e, token, customer := flagEventsRuntime(t, api.PlanPro)
	auth := map[string]string{"Authorization": "Bearer " + token}
	base := "/v1/projects/flags-events/environments/production/insights"
	ingest := func(events []api.FlagEvent) api.FlagEventsResponse {
		t.Helper()
		res := e.Do(t, http.MethodPost, "/v1/runtime/flags/events", api.FlagEventsRequest{Events: events}, auth)
		var out api.FlagEventsResponse
		if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil || res.Code != http.StatusOK {
			t.Fatalf("ingest = %d %s", res.Code, res.Body.String())
		}
		return out
	}
	now := time.Now().UTC()

	// Off: events still count for experiments, but insights refuse and keep nothing.
	ingest([]api.FlagEvent{{Name: "signup", CustomerID: customer, At: now.Add(-3 * time.Hour)}})
	if res := e.Do(t, http.MethodGet, base+"/events?event=signup", nil, nil); res.Code != http.StatusForbidden {
		t.Fatalf("insights with the flag off = %d %s; want 403", res.Code, res.Body.String())
	}

	(*e.S()).EnablePlatformFlagForAll(platformflags.ProductAnalytics)
	out := ingest([]api.FlagEvent{
		{Name: "signup", CustomerID: customer, At: now.Add(-2 * time.Hour), Subject: "user-1",
			Properties: map[string]string{"source": "email"}, Flags: map[string]string{"checkout": "new"}},
		{Name: "checkout", CustomerID: customer, At: now.Add(-time.Hour), Subject: "user-1"},
		{Name: "signup", CustomerID: customer, Properties: map[string]string{"Bad Key": "x"}},
		{Name: "signup", CustomerID: customer, Subject: strings.Repeat("s", api.AnalyticsSubjectMaxBytes+1)},
	})
	if out.Accepted != 2 || out.Dropped.Invalid != 2 {
		t.Fatalf("ingest = %+v; want 2 accepted and 2 invalid", out)
	}

	res := e.Do(t, http.MethodGet, base+"/events?event=signup&by=flag:checkout", nil, nil)
	var series api.InsightEventSeries
	if err := json.Unmarshal(res.Body.Bytes(), &series); res.Code != http.StatusOK || err != nil {
		t.Fatalf("series = %d %s", res.Code, res.Body.String())
	}
	var total int64
	for _, p := range series.Points {
		total += p.Events
		if p.Breakdown != "new" {
			t.Fatalf("series points = %+v; want only the event kept since the flag is on, broken down as new", series.Points)
		}
	}
	if total != 1 {
		t.Fatalf("series events = %d; want 1 (the event sent before analytics was on is not kept)", total)
	}

	res = e.Do(t, http.MethodPost, base+"/funnel", api.InsightFunnelRequest{Unit: "subject", By: "property:source",
		Steps: []api.InsightStepRequest{{Event: "signup"}, {Event: "checkout"}}}, nil)
	var funnel api.InsightFunnel
	if err := json.Unmarshal(res.Body.Bytes(), &funnel); res.Code != http.StatusOK || err != nil {
		t.Fatalf("funnel = %d %s", res.Code, res.Body.String())
	}
	if funnel.Steps[0].Units != 1 || funnel.Steps[1].Units != 1 || len(funnel.Breakdowns) != 1 || funnel.Breakdowns[0].Value != "email" {
		t.Fatalf("funnel = %+v", funnel)
	}

	res = e.Do(t, http.MethodPost, base+"/retention", api.InsightRetentionRequest{Start: api.InsightStepRequest{Event: "signup"},
		Return: api.InsightStepRequest{Event: "checkout"}, Period: "week"}, nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"period":"week"`) {
		t.Fatalf("retention = %d %s", res.Code, res.Body.String())
	}

	for _, tc := range []struct {
		method, path string
		body         any
		want         string
	}{
		{http.MethodGet, base + "/events?event=Bad%20Name", nil, "event"},
		{http.MethodGet, base + "/events?event=signup&by=country", nil, "by is"},
		{http.MethodPost, base + "/funnel", api.InsightFunnelRequest{Steps: []api.InsightStepRequest{{Event: "signup"}}}, "steps"},
		{http.MethodPost, base + "/funnel", api.InsightFunnelRequest{Window: "1m", Steps: []api.InsightStepRequest{{Event: "a"}, {Event: "b"}}}, "window"},
		{http.MethodPost, base + "/retention", api.InsightRetentionRequest{Start: api.InsightStepRequest{Event: "a"}, Return: api.InsightStepRequest{Event: "b"}, Periods: 99}, "periods"},
	} {
		res := e.Do(t, tc.method, tc.path, tc.body, nil)
		if res.Code != http.StatusUnprocessableEntity || !strings.Contains(res.Body.String(), tc.want) {
			t.Errorf("%s %s = %d %s; want 422 naming %q", tc.method, tc.path, res.Code, res.Body.String(), tc.want)
		}
	}
}

// ADR 20261117084505 §2: Free keeps no analytics events, so its queries are
// refused as invalid rather than answered empty.
func TestInsightsFreePlanHasNoRetention(t *testing.T) {
	e, _, _ := flagEventsRuntime(t, api.PlanFree)
	(*e.S()).EnablePlatformFlagForAll(platformflags.ProductAnalytics)
	res := e.Do(t, http.MethodGet, "/v1/projects/flags-events/environments/production/insights/events?event=signup", nil, nil)
	if res.Code != http.StatusUnprocessableEntity || !strings.Contains(res.Body.String(), "keeps no events") {
		t.Fatalf("free insights = %d %s; want 422", res.Code, res.Body.String())
	}
}
