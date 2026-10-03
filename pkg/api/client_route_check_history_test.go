package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouteCheckHistoryClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" || r.Method != "GET" {
			t.Error("history read lost authentication")
		}
		base := "/v1/apps/demo/route-requirements/checks/deployment/history"
		switch r.URL.Path {
		case base:
			if r.URL.Query().Get("limit") != "3" || r.URL.Query().Get("before") != "cursor" {
				t.Error("history pagination changed")
			}
			_ = json.NewEncoder(w).Encode(RouteCheckHistoryPage{AppID: "app", DeploymentID: "deployment", Entries: []RouteCheckHistorySummary{{ID: "check", Summary: RouteCheckChangeSummary{NewlyViolated: 1}}}, NextCursor: "next"})
		case base + "/check":
			// Full retained evidence can exceed the ordinary 4 MiB response cap.
			_ = json.NewEncoder(w).Encode(RouteCheckHistoryEntry{Version: 1, ID: "check", Check: RouteRequirementsCheck{Report: RouteRequirementsReport{Scope: strings.Repeat("x", 5<<20)}}, Changes: RouteCheckChanges{CheckID: "check", Summary: RouteCheckChangeSummary{NewlyViolated: 1}}})
		case base + "/oversized":
			_, _ = w.Write([]byte(strings.Repeat("x", (32<<20)+1)))
		default:
			t.Error("unexpected history path")
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	page, err := client.ListRouteCheckHistory(t.Context(), "demo", "deployment", 3, "cursor")
	if err != nil || len(page.Entries) != 1 || page.NextCursor != "next" || page.Entries[0].Summary.NewlyViolated != 1 {
		t.Fatalf("history pagination: %+v %v", page, err)
	}
	entry, err := client.GetRouteCheckHistoryEntry(t.Context(), "demo", "deployment", "check")
	if err != nil || entry.ID != "check" || entry.Changes.Summary.NewlyViolated != 1 || len(entry.Check.Report.Scope) != 5<<20 {
		t.Fatalf("large history entry: id=%s %v", entry.ID, err)
	}
	if _, err := client.GetRouteCheckHistoryEntry(t.Context(), "demo", "deployment", "oversized"); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("unbounded history entry: %v", err)
	}
}
