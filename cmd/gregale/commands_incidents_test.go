package main_test

// adr: 20261022193512 — `gregale incidents` lists with filters and shows an
// incident's notifications, log context and changes.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdIncidentsListAndShow(t *testing.T) {
	opened := time.Date(2026, 10, 22, 11, 40, 0, 0, time.UTC)
	resolved := opened.Add(15 * time.Minute)
	inc := api.Incident{ID: "alert-e1", Source: api.IncidentSourceAlert, Status: api.IncidentStatusResolved, Title: "checkout-5xx",
		App: "shop", OpenedAt: opened, ResolvedAt: &resolved, Metric: "error_rate_pct"}
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/incidents" {
			queries = append(queries, r.URL.RawQuery)
			_ = json.NewEncoder(w).Encode(api.IncidentsResponse{Since: opened.Add(-time.Hour), Incidents: []api.Incident{inc},
				UnavailableSources: []string{api.IncidentSourcePlatform}})
			return
		}
		_ = json.NewEncoder(w).Encode(api.IncidentDetail{Incident: inc,
			Fires:      []api.IncidentFire{{At: opened, Observed: 9.5, Status: "delivered"}},
			LogContext: json.RawMessage(`{"errors":4,"warnings":1,"http_5xx":3,"busiest_error_route":{"route":"/checkout","count":3},"search_path":"/dashboard/apps/shop/logs?level=error"}`),
			Changes:    []api.AppChangeEvent{{At: opened.Add(-5 * time.Minute), Source: api.ChangeSourceDeployment, Kind: "deploy.created", Summary: "Deployment created"}},
		})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &stdout
	t.Cleanup(func() { *main.OsStdout = old })

	if code := main.CmdIncidents([]string{"--app", "shop", "--open"}); code != 0 {
		t.Fatalf("list = %d", code)
	}
	if len(queries) != 1 || queries[0] != "app=shop&status=open" {
		t.Fatalf("queries = %q", queries)
	}
	for _, want := range []string{"resolved", "alert", "shop", "15m", "checkout-5xx", "alert-e1", "Could not read: platform."} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("list output missing %q:\n%s", want, stdout.String())
		}
	}
	if code := main.CmdIncidents([]string{"list", "--since", "3d"}); code != 0 || !strings.HasPrefix(queries[1], "since=") {
		t.Fatalf("list --since = %d, query %q", code, queries[1])
	}
	if code := main.CmdIncidents([]string{"list", "--since", "soon"}); code != 1 {
		t.Fatalf("list --since soon = %d, want 1", code)
	}

	stdout.Reset()
	if code := main.CmdIncidents([]string{"show", "alert-e1"}); code != 0 {
		t.Fatalf("show = %d", code)
	}
	for _, want := range []string{"checkout-5xx  (alert, resolved)", "after 15m", "observed 9.5  delivered",
		"4 errors, 1 warnings, 3 HTTP 5xx (most errors on /checkout: 3)", "search: /dashboard/apps/shop/logs?level=error", "Deployment created"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("show output missing %q:\n%s", want, stdout.String())
		}
	}
	if code := main.CmdIncidents([]string{"close"}); code != 1 {
		t.Fatalf("unknown subcommand = %d, want 1", code)
	}
}

func TestCmdIncidentsAckAndNote(t *testing.T) {
	at := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	type call struct{ method, path, body string }
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		calls = append(calls, call{r.Method, r.URL.Path, string(raw)})
		switch {
		case strings.HasSuffix(r.URL.Path, "/acknowledge"):
			_ = json.NewEncoder(w).Encode(api.IncidentNote{ID: "n1", Kind: api.IncidentNoteKindAcknowledged, Author: "ops@example.com", CreatedAt: at})
		case strings.HasSuffix(r.URL.Path, "/notes"):
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.IncidentNote{ID: "n2", Kind: api.IncidentNoteKindNote, Body: "rolled back to v41", Author: "ops@example.com", CreatedAt: at})
		default:
			opened := at.Add(-10 * time.Minute)
			_ = json.NewEncoder(w).Encode(api.IncidentDetail{
				Incident: api.Incident{ID: "alert-e1", Source: api.IncidentSourceAlert, Status: api.IncidentStatusOpen, Title: "checkout-5xx",
					OpenedAt: opened, AcknowledgedAt: &at, AcknowledgedBy: "ops@example.com"},
				Notes: []api.IncidentNote{{ID: "n1", Kind: api.IncidentNoteKindAcknowledged, Author: "ops@example.com", CreatedAt: at},
					{ID: "n2", Kind: api.IncidentNoteKindNote, Body: "rolled back to v41\nwatching", Author: "api_key:k-2", CreatedAt: at}},
			})
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &stdout
	t.Cleanup(func() { *main.OsStdout = old })

	if code := main.CmdIncidents([]string{"ack", "alert-e1"}); code != 0 || !strings.Contains(stdout.String(), "Acknowledged alert-e1 (by ops@example.com at 2026-10-23 09:30 UTC).") {
		t.Fatalf("ack = %d:\n%s", code, stdout.String())
	}
	if code := main.CmdIncidents([]string{"note", "alert-e1", "rolled", "back", "to", "v41"}); code != 0 || !strings.Contains(stdout.String(), "Added a note to alert-e1.") {
		t.Fatalf("note = %d:\n%s", code, stdout.String())
	}
	if len(calls) != 2 || calls[0] != (call{"POST", "/v1/incidents/alert-e1/acknowledge", ""}) ||
		calls[1].path != "/v1/incidents/alert-e1/notes" || !strings.Contains(calls[1].body, `"body":"rolled back to v41"`) {
		t.Fatalf("calls = %+v", calls)
	}
	for _, args := range [][]string{{"ack"}, {"ack", "a", "b"}, {"note", "alert-e1"}} {
		if code := main.CmdIncidents(args); code != 1 {
			t.Errorf("%v = %d, want 1", args, code)
		}
	}
	stdout.Reset()
	if code := main.CmdIncidents([]string{"show", "alert-e1"}); code != 0 {
		t.Fatalf("show = %d", code)
	}
	for _, want := range []string{"acked     2026-10-23 09:30:00 UTC by ops@example.com", "Notes", "(acknowledged)",
		"api_key:k-2\n                    rolled back to v41\n                    watching"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("show output missing %q:\n%s", want, stdout.String())
		}
	}
}
