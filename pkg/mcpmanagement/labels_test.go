package mcpmanagement

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// gregale_list_apps takes a label selector, and app views carry labels
// (ADR-20261031100025). The backend ignores ?selector=, as an apid without
// labels does, so the result shows the server's own match.
func TestListAppsSelector(t *testing.T) {
	var selected atomic.Value
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps":
			selected.Store(r.URL.Query().Get("selector"))
			_, _ = w.Write([]byte(`[
				{"id":"a","slug":"alpha","status":"active","labels":{"team":"billing","tier":"web"}},
				{"id":"b","slug":"bravo","status":"active","labels":{"team":"billing","tier":"api"}},
				{"id":"c","slug":"charlie","status":"active","labels":{"team":"search"}}]`))
		case "/v1/apps/alpha":
			_, _ = w.Write([]byte(`{"id":"a","slug":"alpha","status":"active","labels":{"team":"billing"}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer backend.Close()
	cs, ctx := connectTestServer(t, backend, 0, "2026-07-28")

	page := callTool(t, ctx, cs, "gregale_list_apps", map[string]any{"selector": "team=billing, tier notin (api)"}, false)
	items := string(page["items"])
	if !strings.Contains(items, "alpha") || strings.Contains(items, "bravo") || strings.Contains(items, "charlie") || string(page["total"]) != "1" {
		t.Fatalf("selected page=%s", page)
	}
	if !strings.Contains(items, `"labels":{"team":"billing","tier":"web"}`) {
		t.Fatalf("labels missing from the app view: %s", items)
	}
	if got := selected.Load(); got != "team=billing,tier notin (api)" {
		t.Fatalf("forwarded selector = %v, want the canonical form", got)
	}

	page = callTool(t, ctx, cs, "gregale_list_apps", map[string]any{}, false)
	if string(page["total"]) != "3" {
		t.Fatalf("unselected page=%s", page)
	}
	if got := selected.Load(); got != "" {
		t.Fatalf("a plain list sent selector %v", got)
	}

	app := callTool(t, ctx, cs, "gregale_get_app", map[string]any{"app": "alpha"}, false)
	if string(app["labels"]) != `{"team":"billing"}` {
		t.Fatalf("app=%s", app)
	}

	before := requests.Load()
	failure := callTool(t, ctx, cs, "gregale_list_apps", map[string]any{"selector": "tier in (web"}, true)
	if !strings.Contains(string(failure["code"]), "invalid_label_selector") {
		t.Fatalf("invalid selector result=%s", failure)
	}
	if requests.Load() != before {
		t.Fatal("an invalid selector reached the API")
	}
}
