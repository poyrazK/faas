package insights_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/insights"
	"github.com/onebox-faas/faas/pkg/state"
)

func (f fixture) saved(t *testing.T, req api.SavedInsightRequest) state.SavedInsight {
	t.Helper()
	query, err := insights.NormalizeSaved(&req, api.Plan(api.PlanPro).AnalyticsRetentionDays())
	if err != nil {
		t.Fatalf("NormalizeSaved: %v", err)
	}
	return state.SavedInsight{EnvironmentID: f.scope.EnvironmentID, Key: req.Key, Name: req.Name, Kind: req.Kind,
		Query: query, RangeDays: req.RangeDays}
}

// ADR-20261122190000: a saved insight's value is an events total (or value
// sum), a funnel's overall conversion or the first-period retention rate.
func TestRunSavedValues(t *testing.T) {
	f := newFixture(t)
	a, b := f.customer(t, "a"), f.customer(t, "b")
	h := time.Hour
	order := func(c string, at time.Duration, value int64) state.FlagConversionEvent {
		e := f.ev(c, "order", at, nil)
		e.Value = value
		return e
	}
	f.track(t, f.ev(a, "signup", 50*h, nil), order(a, 49*h, 1200), f.ev(b, "signup", 30*h, nil),
		f.ev(a, "visit", 47*h, nil), f.ev(a, "visit", 20*h, nil), f.ev(b, "visit", 29*h, nil),
		// Outside a 1-day range.
		order(b, 100*h, 500))
	tests := []struct {
		name string
		req  api.SavedInsightRequest
		want float64 // -1: no data, no value
	}{
		{"events total", api.SavedInsightRequest{Key: "orders", Kind: "events", Events: &api.InsightEventsQuery{Event: "order"}}, 2},
		{"events value", api.SavedInsightRequest{Key: "revenue", Kind: "events", Events: &api.InsightEventsQuery{Event: "order", Measure: "value"}}, 1700},
		{"range days", api.SavedInsightRequest{Key: "today", Kind: "events", RangeDays: 3, Events: &api.InsightEventsQuery{Event: "order"}}, 1},
		{"funnel", api.SavedInsightRequest{Key: "conv", Kind: "funnel", Funnel: &api.InsightFunnelRequest{
			Steps: []api.InsightStepRequest{{Event: "signup"}, {Event: "order"}}}}, 0.5},
		{"retention", api.SavedInsightRequest{Key: "ret", Kind: "retention", Retention: &api.InsightRetentionRequest{
			Start: api.InsightStepRequest{Event: "signup"}, Return: api.InsightStepRequest{Event: "visit"}, Periods: 2}}, 1}, // b's day 1 has not ended, so only a counts.
		{"funnel without entrants", api.SavedInsightRequest{Key: "none", Kind: "funnel", Funnel: &api.InsightFunnelRequest{
			Steps: []api.InsightStepRequest{{Event: "never"}, {Event: "order"}}}}, -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := insights.RunSaved(f.ctx, f.store, f.saved(t, tc.req), api.Plan(api.PlanPro).AnalyticsRetentionDays(), f.now)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.want < 0 && res.Value != nil:
				t.Fatalf("value = %v, want none (%+v)", *res.Value, res)
			case tc.want >= 0 && (res.Value == nil || *res.Value != tc.want):
				t.Fatalf("value = %v, want %v (%+v)", res.Value, tc.want, res)
			}
			var csv strings.Builder
			if err := api.WriteSavedInsightCSV(&csv, res); err != nil || strings.Count(csv.String(), "\n") < 2 {
				t.Fatalf("csv = %q, %v", csv.String(), err)
			}
		})
	}
}

func TestNormalizeSavedRefuses(t *testing.T) {
	from := time.Now()
	ok := func() api.SavedInsightRequest {
		return api.SavedInsightRequest{Key: "orders", Kind: "events", Events: &api.InsightEventsQuery{Event: "order"}}
	}
	tests := []struct {
		name   string
		mutate func(*api.SavedInsightRequest)
	}{
		{"bad key", func(r *api.SavedInsightRequest) { r.Key = "Orders" }},
		{"no query", func(r *api.SavedInsightRequest) { r.Events = nil }},
		{"two queries", func(r *api.SavedInsightRequest) { r.Funnel = &api.InsightFunnelRequest{} }},
		{"kind mismatch", func(r *api.SavedInsightRequest) { r.Kind = "funnel" }},
		{"bad measure", func(r *api.SavedInsightRequest) { r.Events.Measure = "units" }},
		{"range past retention", func(r *api.SavedInsightRequest) { r.RangeDays = 400 }},
		{"hourly over a week", func(r *api.SavedInsightRequest) { r.Events.Interval, r.RangeDays = "hour", 8 }},
		{"bad digest", func(r *api.SavedInsightRequest) {
			r.Digest = "hourly"
			r.ChannelIDs = []string{"6f1c2c55-6a3f-4d7c-9a51-1d0c1f1b8e01"}
		}},
		{"bad comparison", func(r *api.SavedInsightRequest) {
			r.Alert = &api.SavedInsightAlert{Comparison: "eq"}
			r.ChannelIDs = []string{"6f1c2c55-6a3f-4d7c-9a51-1d0c1f1b8e01"}
		}},
		{"digest without channel", func(r *api.SavedInsightRequest) { r.Digest = "daily" }},
		{"channel without digest", func(r *api.SavedInsightRequest) { r.ChannelIDs = []string{"6f1c2c55-6a3f-4d7c-9a51-1d0c1f1b8e01"} }},
		{"funnel with from", func(r *api.SavedInsightRequest) {
			r.Kind, r.Events = "funnel", nil
			r.Funnel = &api.InsightFunnelRequest{From: &from, Steps: []api.InsightStepRequest{{Event: "a"}, {Event: "b"}}}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := ok()
			tc.mutate(&req)
			if _, err := insights.NormalizeSaved(&req, 90); !errors.Is(err, insights.ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
	req := ok()
	query, err := insights.NormalizeSaved(&req, 30)
	if err != nil || req.Name != "orders" || req.RangeDays != 7 {
		t.Fatalf("defaults: %+v, %v", req, err)
	}
	var back api.InsightEventsQuery
	if err := json.Unmarshal(query, &back); err != nil || back.Event != "order" {
		t.Fatalf("query = %s, %v", query, err)
	}
}

func TestSchedule(t *testing.T) {
	// Sunday 2026-11-22 10:00 UTC.
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		digest           string
		alert            bool
		wantDigest, want string
	}{
		{"", false, "", ""},
		{"daily", false, "2026-11-23T08:00:00Z", "2026-11-23T08:00:00Z"},
		{"weekly", false, "2026-11-23T08:00:00Z", "2026-11-23T08:00:00Z"},
		{"", true, "", "2026-11-22T10:15:00Z"},
		{"daily", true, "2026-11-23T08:00:00Z", "2026-11-22T10:15:00Z"},
	}
	str := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.Format(time.RFC3339)
	}
	for _, tc := range tests {
		d, c := insights.Schedule(tc.digest, tc.alert, now)
		if str(d) != tc.wantDigest || str(c) != tc.want {
			t.Errorf("Schedule(%q, %v) = %s, %s; want %s, %s", tc.digest, tc.alert, str(d), str(c), tc.wantDigest, tc.want)
		}
	}
	// A weekly digest on a Monday after 08:00 waits a week.
	d, _ := insights.Schedule("weekly", false, time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC))
	if str(d) != "2026-11-30T08:00:00Z" {
		t.Fatalf("weekly from Monday = %s", str(d))
	}
}
