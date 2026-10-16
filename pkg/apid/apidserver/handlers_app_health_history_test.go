package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apphealth"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppHealthHistoryAPI(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "history-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/history-app/health/history"
	read := func() api.AppHealthHistoryPage {
		t.Helper()
		rec := e.Do(t, http.MethodGet, path, nil, nil)
		var page api.AppHealthHistoryPage
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		return page
	}
	if page := read(); len(page.Entries) != 0 || page.CollectorFresh {
		t.Fatal("read created history")
	}
	collector := apphealth.Collector{Store: (*e.Store())}
	if n, err := collector.Sweep(t.Context()); err != nil || n != 1 {
		t.Fatalf("collect %d %v", n, err)
	}
	page := read()
	if page.AppID != app.ID || len(page.Entries) != 1 || page.Entries[0].Kind != "baseline" || !page.CollectorFresh {
		t.Fatalf("%+v", page)
	}
	for _, query := range []string{"?limit=0", "?limit=101", "?before=invalid"} {
		if rec := e.Do(t, http.MethodGet, path+query, nil, nil); rec.Code != 400 {
			t.Fatalf("%s: %d %s", query, rec.Code, rec.Body)
		}
	}
	if rec := e.Do(t, http.MethodGet, path+"?before=12345678-1234-1234-1234-123456789abc", nil, nil); rec.Code != 404 {
		t.Fatal("missing cursor", rec.Code)
	}
	other, err := (*e.Store()).CreateAccount(t.Context(), "foreign-history@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: other.ID, Slug: "foreign-history"}); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/apps/foreign-history/health/history", nil, nil); rec.Code != 404 {
		t.Fatal("foreign app exposed", rec.Code)
	}
	if again := read(); len(again.Entries) != 1 || again.Latest.EvaluatedAt != page.Latest.EvaluatedAt {
		t.Fatal("history read refreshed evidence")
	}
}
