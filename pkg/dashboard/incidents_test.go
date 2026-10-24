package dashboard

// adr: 20261022193512 — the incident pages list incidents open first with
// who acknowledged them, and show an incident's evidence, notes and the
// acknowledge and note forms.

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestIncidentPagesRender(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	opened, acked := now.Add(-95*time.Minute), now.Add(-90*time.Minute)
	resolved := now.Add(-2 * time.Hour)
	open := api.Incident{ID: "alert-e1", Source: api.IncidentSourceAlert, Status: api.IncidentStatusOpen, Title: "checkout-5xx", App: "shop",
		OpenedAt: opened, Metric: "error_rate_pct", AcknowledgedAt: &acked, AcknowledgedBy: "ops@example.com"}
	closed := api.Incident{ID: "route-r1", Source: api.IncidentSourceRouteMonitor, Status: api.IncidentStatusResolved,
		Title: "Route requirements violated after a deploy", App: "shop", OpenedAt: resolved.Add(-10 * time.Minute), ResolvedAt: &resolved}
	list := NewIncidentsData(api.IncidentsResponse{Since: now.Add(-7 * 24 * time.Hour), Incidents: []api.Incident{open, closed},
		UnavailableSources: []string{api.IncidentSourcePlatform}}, now)
	if list.Rows[0].Duration != "1h35m" || list.Rows[1].Duration != "10m" || list.Rows[1].Source != "route monitor" {
		t.Fatalf("rows = %+v", list.Rows)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := httptest.NewRecorder()
	if err := Render(w, log, "nonce", Page{Title: "Incidents", Body: "incidents", Data: list}); err != nil {
		t.Fatalf("render list: %v", err)
	}
	for _, s := range []string{`href="/dashboard/incidents/alert-e1"`, `class="open">open`, "ops@example.com", "1h35m", "Could not read: platform"} {
		if !strings.Contains(w.Body.String(), s) {
			t.Errorf("list page missing %q", s)
		}
	}

	detail := api.IncidentDetail{Incident: open,
		Fires:      []api.IncidentFire{{At: opened, Observed: 9.5, Status: "delivered"}},
		LogContext: json.RawMessage(`{"errors":4,"warnings":1,"http_5xx":3,"search_path":"/dashboard/apps/shop/logs?level=error"}`),
		Changes:    []api.AppChangeEvent{{At: opened.Add(-5 * time.Minute), Source: api.ChangeSourceDeployment, Summary: "Deployment created"}},
		Notes: []api.IncidentNote{{ID: "n1", Kind: api.IncidentNoteKindAcknowledged, Author: "ops@example.com", CreatedAt: acked},
			{ID: "n2", Kind: api.IncidentNoteKindNote, Body: "rolled back <v41>", Author: "api_key:k-2", CreatedAt: now}},
		UnavailableSources: []string{"changes.health"},
	}
	data := NewIncidentData(detail, now)
	data.CSRF = "tok"
	w = httptest.NewRecorder()
	if err := Render(w, log, "nonce", Page{Title: "Incident", Body: "incident", Data: data}); err != nil {
		t.Fatalf("render detail: %v", err)
	}
	body := w.Body.String()
	for _, s := range []string{"<h1>checkout-5xx</h1>", "observed 9.5", "4 errors, 1 warnings, 3 HTTP 5xx", `href="/dashboard/apps/shop/logs?level=error"`,
		"Deployment created", "rolled back &lt;v41&gt;", "Acknowledged", "by ops@example.com", `action="/dashboard/incidents/alert-e1/notes"`,
		`name="csrf_token" value="tok"`, "Could not read: changes.health."} {
		if !strings.Contains(body, s) {
			t.Errorf("detail page missing %q", s)
		}
	}
	if strings.Contains(body, `/acknowledge"`) {
		t.Error("an acknowledged incident must not offer the Acknowledge button")
	}

	open.AcknowledgedAt, open.AcknowledgedBy = nil, ""
	data = NewIncidentData(api.IncidentDetail{Incident: open}, now)
	data.CSRF = "tok"
	w = httptest.NewRecorder()
	if err := Render(w, log, "nonce", Page{Title: "Incident", Body: "incident", Data: data}); err != nil {
		t.Fatalf("render unacknowledged: %v", err)
	}
	if !strings.Contains(w.Body.String(), `action="/dashboard/incidents/alert-e1/acknowledge"`) {
		t.Error("an open, unacknowledged incident must offer the Acknowledge button")
	}
}
