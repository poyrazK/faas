package dashboard

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dataapi"
)

func TestDataAPIRenderEscapesCatalogAndShowsOwnerActions(t *testing.T) {
	attack := `<script>alert("catalog")</script>`
	data := DataAPIData{Slug: "orders", Enabled: true, CSRFToken: "sealed-token", TaskID: "task-id", TaskStatus: "succeeded", Fingerprint: strings.Repeat("a", 64), Schema: &dataapi.Snapshot{
		Schemas: []string{"api"}, Tables: []dataapi.Table{{Schema: "api", Name: attack, Columns: []dataapi.Column{{Name: "id", Type: attack}}}},
		Composites: []dataapi.Composite{{Schema: "api", Name: "location", Fields: []dataapi.CompositeField{{Name: attack, Type: attack, Nullable: true}}}},
	}}
	w := httptest.NewRecorder()
	if err := Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-nonce", Page{Title: "Data API", Body: "data_api", Data: data}); err != nil {
		t.Fatal(err)
	}
	body := w.Body.String()
	if strings.Contains(body, attack) || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("catalog text did not stay escaped")
	}
	if !strings.Contains(body, "Composite api.location") {
		t.Fatal("missing composite schema inspection")
	}
	for _, action := range []string{"schema", "types", "refresh"} {
		if !strings.Contains(body, "/dashboard/apps/orders/data-api/"+action) {
			t.Fatalf("missing owner action %q", action)
		}
	}
	if strings.Contains(body, `http-equiv="refresh"`) || strings.Contains(body, "Download database.types.ts") {
		t.Fatal("completed schema observation polled or offered ungenerated types")
	}
}

func TestDataAPIUsageViewIsNewestFirstWithScaledMeters(t *testing.T) {
	start := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	view := NewDataAPIUsageView(api.DataAPIUsage{
		Totals:      api.DataAPIUsageCounts{UserRequests: 5, AnonymousRequests: 4, ServerRequests: 1, CappedRequests: 2, RowsReturned: 30},
		MonthToDate: api.DataAPIUsageMonthToDate{Month: "2026-10", Requests: 120, AnonymousRequests: 40},
		Buckets: []api.DataAPIUsageBucket{
			{Start: start, DataAPIUsageCounts: api.DataAPIUsageCounts{UserRequests: 5, RowsReturned: 30}},
			{Start: start.Add(24 * time.Hour), DataAPIUsageCounts: api.DataAPIUsageCounts{AnonymousRequests: 4, ServerRequests: 1, CappedRequests: 2}},
		},
	})
	if view.Month != "2026-10" || view.MonthRequests != 120 || view.Totals.Requests != 10 || view.Max != 5 || len(view.Days) != 2 || view.Days[0].Label != "2026-10-19" || view.Days[0].Capped != 2 {
		t.Fatalf("view = %+v", view)
	}
	if empty := NewDataAPIUsageView(api.DataAPIUsage{}); empty.Max != 1 {
		t.Fatal("meters need a positive maximum")
	}
}

func TestDataAPIAccessSettingsReportPresenceOnly(t *testing.T) {
	settings, slots := DataAPIAccessSettings("orders", map[string]bool{"DATA_API_PUBLIC_READ": true, "DATA_API_MONTHLY_CAPS": true, "DATA_API_SERVER_KEY_A_SHA256": true, "DATA_API_SERVER_KEY_A_PERMISSIONS": true, "DATA_API_SERVER_KEY_B_PERMISSIONS": true})
	states := map[string]string{}
	for _, s := range settings {
		states[s.Name] = s.State
		if !strings.HasPrefix(s.Command, "gregale data-api configure orders ") {
			t.Fatalf("command = %q", s.Command)
		}
	}
	if states["Public reads"] != "On" || states["Public RPCs"] != "Off" || states["Monthly caps"] != "Set" || states["Usage reporting"] != "Off" {
		t.Fatalf("states = %v", states)
	}
	if !slots[0].InUse || !slots[0].Restricted || slots[1].InUse || slots[1].Restricted {
		t.Fatalf("slots = %+v (permissions without a key restrict nothing)", slots)
	}
}

func TestDataAPIRenderShowsUsageAndAccess(t *testing.T) {
	settings, slots := DataAPIAccessSettings("orders", map[string]bool{"DATA_API_USAGE_URL": true, "DATA_API_SERVER_KEY_B_SHA256": true})
	data := DataAPIData{Slug: "orders", Access: settings, ServerKeys: slots, Usage: NewDataAPIUsageView(api.DataAPIUsage{
		MonthToDate: api.DataAPIUsageMonthToDate{Month: "2026-10", Requests: 7},
		Buckets:     []api.DataAPIUsageBucket{{Start: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC), DataAPIUsageCounts: api.DataAPIUsageCounts{UserRequests: 7, CappedRequests: 3}}},
		Totals:      api.DataAPIUsageCounts{UserRequests: 7, CappedRequests: 3},
	})}
	render := func(data DataAPIData) string {
		w := httptest.NewRecorder()
		if err := Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-nonce", Page{Title: "Data API", Body: "data_api", Data: data}); err != nil {
			t.Fatal(err)
		}
		return w.Body.String()
	}
	body := render(data)
	for _, want := range []string{"This month (2026-10): 7 requests", "3 refused by monthly caps", `<meter min="0" max="7" value="7">`, "gregale data-api usage orders --since 30d", "Usage reporting</td><td>On", "gregale data-api configure orders --monthly-caps", "<td>b</td><td>In use</td>", "<td>a</td><td>Empty</td>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
	unavailable := render(DataAPIData{Slug: "orders", UsageMessage: "Usage is not available on this host."})
	if !strings.Contains(unavailable, "Usage is not available on this host.") || strings.Contains(unavailable, "<h2>Access</h2>") {
		t.Fatal("unavailable usage or missing settings rendered wrongly")
	}
}
