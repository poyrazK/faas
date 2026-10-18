package logsearch

// adr: 20261018083536 — log search filters, bounds and responses.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var testNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func TestParseFilter(t *testing.T) {
	keep := 7 * 24 * time.Hour
	tests := []struct {
		name     string
		query    string
		facets   bool
		wantCode string
		check    func(t *testing.T, f state.LogEventFilter)
	}{
		{name: "defaults to the last hour", check: func(t *testing.T, f state.LogEventFilter) {
			if !f.Since.Equal(testNow.Add(-time.Hour)) || !f.Until.Equal(testNow) || f.Limit != 100 || f.Timeout != api.LogSearchStatementTimeout {
				t.Fatalf("filter = %+v", f)
			}
		}},
		{name: "day lookback", query: "since=3d", check: func(t *testing.T, f state.LogEventFilter) {
			if !f.Since.Equal(testNow.Add(-72 * time.Hour)) {
				t.Fatalf("since = %s", f.Since)
			}
		}},
		{name: "since clamps to retention", query: "since=30d", check: func(t *testing.T, f state.LogEventFilter) {
			if !f.Since.Equal(testNow.Add(-keep)) {
				t.Fatalf("since = %s, want retention start", f.Since)
			}
		}},
		{name: "window before retention", query: "since=2026-09-01T00:00:00Z&until=2026-09-02T00:00:00Z", wantCode: api.CodeValidation},
		{name: "levels and status class", query: "level=ERROR,warn&status=5xx", check: func(t *testing.T, f state.LogEventFilter) {
			if len(f.Levels) != 2 || f.Levels[0] != "error" || f.StatusClass != 5 || f.Status != 0 {
				t.Fatalf("filter = %+v", f)
			}
		}},
		{name: "exact status", query: "status=502", check: func(t *testing.T, f state.LogEventFilter) {
			if f.Status != 502 || f.StatusClass != 0 {
				t.Fatalf("filter = %+v", f)
			}
		}},
		{name: "bad status class", query: "status=9xx", wantCode: api.CodeValidation},
		{name: "bad level", query: "level=loud", wantCode: api.CodeValidation},
		{name: "bad source", query: "source=kernel", wantCode: api.CodeValidation},
		{name: "short text", query: "q=a", wantCode: api.CodeValidation},
		{name: "text within a day", query: "q=timeout&since=24h", check: func(t *testing.T, f state.LogEventFilter) {
			if f.Query != "timeout" {
				t.Fatalf("query = %q", f.Query)
			}
		}},
		{name: "text over a day", query: "q=timeout&since=25h", wantCode: "log_search_window_too_large"},
		{name: "facets within a week", query: "since=7d", facets: true},
		{name: "facets over a week", query: "since=8d", facets: true, wantCode: "log_search_window_too_large"},
		{name: "limit bound", query: "limit=201", wantCode: api.CodeValidation},
		{name: "garbage cursor", query: "cursor=nope", wantCode: api.CodeValidation},
		{name: "since after until", query: "since=2026-10-18T11:00:00Z&until=2026-10-18T10:00:00Z", wantCode: api.CodeValidation},
		{name: "deployment must be a uuid", query: "deployment_id=v3", wantCode: api.CodeValidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := url.ParseQuery(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			keepFor := keep
			if tt.name == "facets over a week" {
				keepFor = 30 * 24 * time.Hour
			}
			f, prob := ParseFilter(q, testNow, keepFor, tt.facets)
			if tt.wantCode != "" {
				if prob == nil || prob.Code != tt.wantCode {
					t.Fatalf("problem = %+v, want code %s", prob, tt.wantCode)
				}
				return
			}
			if prob != nil {
				t.Fatalf("unexpected problem %+v", prob)
			}
			if tt.check != nil {
				tt.check(t, f)
			}
		})
	}
}

func TestWindowProblemCarriesLimitAndObserved(t *testing.T) {
	q, _ := url.ParseQuery("q=timeout&since=48h")
	_, prob := ParseFilter(q, testNow, 30*24*time.Hour, false)
	if prob == nil || prob.Limit == nil || *prob.Limit != 86400 || prob.Observed == nil || *prob.Observed != 172800 || prob.DocsURL == "" {
		t.Fatalf("problem = %+v, want limit 86400 observed 172800 and docs", prob)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 18, 11, 59, 1, 123456000, time.UTC)
	id := uuid.NewString()
	gotAt, gotID, err := DecodeCursor(EncodeCursor(at, id))
	if err != nil || !gotAt.Equal(at) || gotID != id {
		t.Fatalf("round trip = %s %s %v", gotAt, gotID, err)
	}
}

type fakeHost struct {
	store *state.MemStore
	app   state.App
}

func (h fakeHost) LoadApp(w http.ResponseWriter, _ *http.Request, _ state.Account, slug string) (state.App, bool) {
	if slug != h.app.Slug {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "not_found", "Not found", "no such app"))
		return state.App{}, false
	}
	return h.app, true
}
func (h fakeHost) LogEvents() state.LogEventStore { return h.store }
func (h fakeHost) Logger() *slog.Logger           { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newService(t *testing.T, enabled bool) (*Service, state.Account) {
	t.Helper()
	acct := state.Account{ID: uuid.NewString(), Plan: api.PlanHobby}
	app := state.App{ID: uuid.NewString(), Slug: "shop"}
	store := state.NewMemStore()
	messages := []struct {
		level, route, msg string
		status            int
	}{
		{"", "/checkout", "checkout failed", 502},
		{"", "/checkout", "checkout ok", 200},
		{"error", "", "db timeout", 0},
	}
	for i, m := range messages {
		source := state.LogEventSourceHTTP
		if m.level != "" {
			source = state.LogEventSourceRuntime
		}
		if _, err := store.InsertLogEvent(context.Background(), state.LogEvent{
			OccurredAt: testNow.Add(-time.Duration(i+1) * time.Minute), AccountID: acct.ID, AppID: app.ID,
			Source: source, SourceEventID: "e" + string(rune('a'+i)), Level: m.level, Route: m.route, Status: m.status, Message: m.msg,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return &Service{Host: fakeHost{store: store, app: app}, Enabled: enabled, Now: func() time.Time { return testNow }}, acct
}

func serve(t *testing.T, h func(http.ResponseWriter, *http.Request, state.Account), acct state.Account, target string, out any) int {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.SetPathValue("slug", "shop")
	w := httptest.NewRecorder()
	h(w, r, acct)
	if out != nil && w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("decode: %v body %s", err, w.Body.String())
		}
	}
	return w.Code
}

func TestSearchPagesNewestFirst(t *testing.T) {
	svc, acct := newService(t, true)
	var page api.LogEventsResponse
	if code := serve(t, svc.Search, acct, "/v1/apps/shop/log-events?limit=2", &page); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if len(page.Events) != 2 || page.Events[0].Message != "checkout failed" || page.Events[0].App != "shop" || page.NextCursor == "" {
		t.Fatalf("page = %+v", page)
	}
	var next api.LogEventsResponse
	if code := serve(t, svc.Search, acct, "/v1/apps/shop/log-events?limit=2&cursor="+page.NextCursor, &next); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if len(next.Events) != 1 || next.Events[0].Message != "db timeout" || next.NextCursor != "" {
		t.Fatalf("next page = %+v", next)
	}
}

func TestSearchFiltersByTextAndLevel(t *testing.T) {
	svc, acct := newService(t, true)
	var page api.LogEventsResponse
	serve(t, svc.Search, acct, "/v1/apps/shop/log-events?q=TIMEOUT&level=error", &page)
	if len(page.Events) != 1 || page.Events[0].Level != "error" || page.Events[0].Source != api.LogSource("runtime") {
		t.Fatalf("page = %+v", page)
	}
}

func TestFacetsCountTheWindow(t *testing.T) {
	svc, acct := newService(t, true)
	var facets api.LogEventFacetsResponse
	if code := serve(t, svc.Facets, acct, "/v1/apps/shop/log-events/facets?since=6h", &facets); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if facets.Total != 3 || len(facets.Routes) != 1 || facets.Routes[0].Count != 2 || len(facets.StatusClasses) != 2 {
		t.Fatalf("facets = %+v", facets)
	}
}

func TestGateAndUnknownApp(t *testing.T) {
	svc, acct := newService(t, false)
	if code := serve(t, svc.Search, acct, "/v1/apps/shop/log-events", nil); code != http.StatusServiceUnavailable {
		t.Fatalf("disabled status = %d, want 503", code)
	}
	svc.Enabled = true
	r := httptest.NewRequest(http.MethodGet, "/v1/apps/nope/log-events/facets", nil)
	r.SetPathValue("slug", "nope")
	w := httptest.NewRecorder()
	svc.Facets(w, r, acct)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown app status = %d, want 404", w.Code)
	}
}
