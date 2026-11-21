package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR 20261117084505 §4: `gregale insight ...` drives the insights API and
// prints series, funnels, retention and requests by flag.
func TestInsightCommands(t *testing.T) {
	var funnel api.InsightFunnelRequest
	var retention api.InsightRetentionRequest
	var eventsQuery, requestsQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		base := "/v1/projects/shop/environments/production/insights"
		now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
		switch r.Method + " " + r.URL.Path {
		case "GET " + base + "/events":
			eventsQuery = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode(api.InsightEventSeries{Event: "order", Unit: "customer", Interval: "day", From: now.AddDate(0, 0, -7), Until: now,
				Points: []api.InsightEventPoint{{Start: now.AddDate(0, 0, -1), Breakdown: "pro", Events: 12, Units: 4, Value: 4800}}})
		case "POST " + base + "/funnel":
			_ = json.NewDecoder(r.Body).Decode(&funnel)
			_ = json.NewEncoder(w).Encode(api.InsightFunnel{Unit: "customer", Window: "24h0m0s", From: now, Until: now, Steps: []api.InsightFunnelStep{
				{Event: "signup", Units: 100, ConversionFromFirst: 1, ConversionFromPrev: 1}, {Event: "paid", Units: 25, ConversionFromFirst: 0.25, ConversionFromPrev: 0.25}},
				Breakdowns: []api.InsightFunnelBreakdown{{Value: "new", Steps: []api.InsightFunnelStep{{Event: "signup", Units: 50}, {Event: "paid", Units: 15}}}}})
		case "POST " + base + "/retention":
			_ = json.NewDecoder(r.Body).Decode(&retention)
			start := now.AddDate(0, 0, -7)
			_ = json.NewEncoder(w).Encode(api.InsightRetention{Unit: "customer", Period: "week", Periods: 2, From: now, Until: now,
				Overall: api.InsightRetentionRow{Units: 10, Eligible: []int64{10}, Returned: []int64{4}, Rates: []float64{0.4}},
				Cohorts: []api.InsightRetentionRow{{Start: &start, Units: 10, Eligible: []int64{10}, Returned: []int64{4}, Rates: []float64{0.4}}}})
		case "GET " + base + "/requests":
			requestsQuery = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode(api.InsightRequests{Flag: "checkout", From: now, Until: now, Variants: []api.InsightRequestVariant{
				{Value: "new", Requests: 1000, ServerErrors: 12, ServerErrorRate: 0.012, P95MS: 180}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	run := func(args ...string) (int, string, string) {
		main.ResetJSONOut(t)
		stdout, stderr, restore := main.SwapIO(t)
		code := main.Run(args)
		restore()
		return code, stdout.String(), stderr()
	}

	code, out, errOut := run("insight", "event", "list", "order", "--project", "shop", "--by", "property:plan", "--where", "plan:pro", "--from", "14d")
	if code != 0 || !strings.Contains(out, "4800") || !strings.Contains(eventsQuery, "where=plan%3Apro") || !strings.Contains(eventsQuery, "from=") {
		t.Fatalf("events = %d %q %q; query %s", code, out, errOut, eventsQuery)
	}
	code, out, _ = run("insight", "funnel", "get", "signup", "paid", "--project", "shop", "--window", "24h", "--by", "flag:checkout")
	if code != 0 || !strings.Contains(out, "25.0%") || !strings.Contains(out, "new") || len(funnel.Steps) != 2 || funnel.Window != "24h" || funnel.By != "flag:checkout" {
		t.Fatalf("funnel = %d %q; request %+v", code, out, funnel)
	}
	code, out, _ = run("insight", "retention", "get", "signup", "open", "--project", "shop", "--period", "week", "--periods", "2")
	if code != 0 || !strings.Contains(out, "40%") || !strings.Contains(out, "WEEK 2") || retention.Return.Event != "open" || retention.Periods != 2 {
		t.Fatalf("retention = %d %q; request %+v", code, out, retention)
	}
	code, out, _ = run("insight", "request", "list", "--flag", "checkout", "--project", "shop")
	if code != 0 || !strings.Contains(out, "1.20%") || !strings.Contains(out, "180 ms") || !strings.Contains(requestsQuery, "flag=checkout") {
		t.Fatalf("requests = %d %q", code, out)
	}
	for _, args := range [][]string{
		{"insight", "funnel", "get", "signup", "--project", "shop"},
		{"insight", "request", "list", "--project", "shop"},
		{"insight", "event", "list", "order", "--project", "shop", "--from", "yesterday"},
	} {
		if code, _, _ := run(args...); code != 1 {
			t.Errorf("%v = %d; want usage error", args, code)
		}
	}
}
