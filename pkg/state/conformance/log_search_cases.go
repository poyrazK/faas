package conformance

// adr: 20261018083536 — ledger search filters and facets behave the same in
// PostgreSQL and in memory.

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func testLogEventSearchAndFacets(t *testing.T, fx *Fixture) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	events := []state.LogEvent{
		{Source: state.LogEventSourceHTTP, Route: "/checkout", Method: "POST", Status: 502, Message: "upstream TIMEOUT on checkout", Occurrences: 3},
		{Source: state.LogEventSourceHTTP, Route: "/checkout", Method: "POST", Status: 200, Message: "checkout ok"},
		{Source: state.LogEventSourceHTTP, Route: "/health", Method: "GET", Status: 200, Message: "health ok"},
		{Source: state.LogEventSourceHTTP, Route: "/cart", Method: "GET", Status: 404, Message: "cart 100%_off missing"},
		{Source: state.LogEventSourceRuntime, Level: "error", Message: "db timeout after 5s"},
		{Source: state.LogEventSourceRuntime, Level: "warn", Message: "slow query"},
		{Source: state.LogEventSourceRuntime, Level: "info", Message: "listening"},
	}
	for i, event := range events {
		event.OccurredAt = now.Add(-time.Duration(i+1) * time.Minute)
		event.AccountID, event.AppID = fx.Account.ID, fx.App.ID
		event.SourceEventID = "search:" + string(rune('a'+i))
		if _, err := fx.Store.InsertLogEvent(fx.Ctx, event); err != nil {
			t.Fatalf("InsertLogEvent %d: %v", i, err)
		}
	}
	base := state.LogEventFilter{
		AccountID: fx.Account.ID, AppID: fx.App.ID,
		Since: now.Add(-time.Hour), Until: now.Add(time.Second), Limit: 50, Timeout: 5 * time.Second,
	}
	messages := func(t *testing.T, f state.LogEventFilter) []string {
		t.Helper()
		page, _, err := fx.Store.ListLogEvents(fx.Ctx, f)
		if err != nil {
			t.Fatalf("ListLogEvents(%+v): %v", f, err)
		}
		out := make([]string, 0, len(page))
		for _, e := range page {
			out = append(out, e.Message)
		}
		return out
	}

	tests := []struct {
		name   string
		mutate func(*state.LogEventFilter)
		want   []string
	}{
		{"levels match any listed severity", func(f *state.LogEventFilter) { f.Levels = []string{"error", "WARN"} }, []string{"db timeout after 5s", "slow query"}},
		{"status class matches the hundred", func(f *state.LogEventFilter) { f.StatusClass = 5 }, []string{"upstream TIMEOUT on checkout"}},
		{"text is a case-insensitive substring", func(f *state.LogEventFilter) { f.Query = "timeout" }, []string{"upstream TIMEOUT on checkout", "db timeout after 5s"}},
		{"text treats LIKE wildcards literally", func(f *state.LogEventFilter) { f.Query = "100%_off" }, []string{"cart 100%_off missing"}},
		{"wildcards never match other text", func(f *state.LogEventFilter) { f.Query = "%_" }, []string{"cart 100%_off missing"}},
		{"filters combine", func(f *state.LogEventFilter) { f.Route = "/checkout"; f.StatusClass = 2 }, []string{"checkout ok"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := base
			tt.mutate(&f)
			got := messages(t, f)
			if len(got) != len(tt.want) {
				t.Fatalf("messages = %q, want %q", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("messages = %q, want %q", got, tt.want)
				}
			}
		})
	}

	facets, err := fx.Store.FacetLogEvents(fx.Ctx, base, 2)
	if err != nil {
		t.Fatalf("FacetLogEvents: %v", err)
	}
	if facets.Total != 9 {
		t.Fatalf("total = %d, want 9 (occurrences summed)", facets.Total)
	}
	wantSources := []state.LogEventFacetCount{{Value: "http", Count: 6}, {Value: "runtime", Count: 3}}
	if !equalFacets(facets.Sources, wantSources) {
		t.Fatalf("sources = %+v, want %+v", facets.Sources, wantSources)
	}
	wantClasses := []state.LogEventFacetCount{{Value: "2xx", Count: 2}, {Value: "4xx", Count: 1}, {Value: "5xx", Count: 3}}
	if !equalFacets(facets.StatusClasses, wantClasses) {
		t.Fatalf("status classes = %+v, want %+v", facets.StatusClasses, wantClasses)
	}
	wantRoutes := []state.LogEventFacetCount{{Value: "/checkout", Count: 4}, {Value: "/cart", Count: 1}}
	if !equalFacets(facets.Routes, wantRoutes) || !facets.RoutesTruncated {
		t.Fatalf("routes = %+v truncated=%v, want %+v truncated", facets.Routes, facets.RoutesTruncated, wantRoutes)
	}
	if len(facets.Levels) != 3 {
		t.Fatalf("levels = %+v, want error, info and warn", facets.Levels)
	}

	filtered := base
	filtered.Levels = []string{"error"}
	only, err := fx.Store.FacetLogEvents(fx.Ctx, filtered, 10)
	if err != nil {
		t.Fatalf("FacetLogEvents filtered: %v", err)
	}
	if only.Total != 1 || len(only.Routes) != 0 || only.RoutesTruncated {
		t.Fatalf("filtered facets = %+v, want one runtime error and no routes", only)
	}

	bad := base
	bad.Query = "x"
	if _, _, err := fx.Store.ListLogEvents(fx.Ctx, bad); err == nil {
		t.Fatal("one-character text query was accepted")
	}
	bad = base
	bad.Status, bad.StatusClass = 500, 5
	if _, err := fx.Store.FacetLogEvents(fx.Ctx, bad, 10); err == nil {
		t.Fatal("status and status class together were accepted")
	}
}

func equalFacets(got, want []state.LogEventFacetCount) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
