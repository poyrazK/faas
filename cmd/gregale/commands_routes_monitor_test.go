package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

func cliProductionIncident(t *testing.T) api.RouteMonitorIncident {
	t.Helper()
	body, err := os.ReadFile("../../tests/fixtures/production-route-incident.json")
	if err != nil {
		t.Fatal(err)
	}
	var i api.RouteMonitorIncident
	if err := json.Unmarshal(body, &i); err != nil {
		t.Fatal(err)
	}
	if err := routemonitor.ValidateIncident(i, "demo"); err != nil {
		t.Fatal(err)
	}
	return i
}

// ADR-464: wire scopes, reports, bounded saved diagnostics and create-new export.
func TestProductionRouteMonitorCLIReportsAndIncidentEvidence(t *testing.T) {
	for _, scenario := range []string{"human", "json", "export", "overwrite", "symlink", "wrong_id", "wrong_weight", "wrong_link", "wrong_verdict", "report", "report_gate", "incidents"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			incident := cliProductionIncident(t)
			switch scenario {
			case "wrong_id":
				incident.ID = "00000000-0000-4000-8000-000000000003"
			case "wrong_weight":
				incident.Evidence[0].Windows[0].Requests.MatchingRequests++
			case "wrong_link":
				incident.Evidence[0].Windows[0].Requests.Examples[0].EvidencePath = "/v1/apps/other/debug/requests/row/evidence"
			case "wrong_verdict":
				incident.OpeningReport.Status = "healthy"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("unexpected write")
				}
				switch r.URL.Path {
				case "/v1/apps/demo/route-monitor/report":
					writeJSONTest(w, incident.OpeningReport)
				case "/v1/apps/demo/route-monitor/incidents":
					if r.URL.Query().Get("limit") != "5" {
						t.Error("page limit lost")
					}
					writeJSONTest(w, api.RouteMonitorIncidentPage{AppID: incident.AppID, Incidents: []api.RouteMonitorIncident{incident}})
				default:
					if r.URL.Path != "/v1/apps/demo/route-monitor/incidents/"+cliProductionIncident(t).ID {
						t.Error("incident scope lost")
					}
					writeJSONTest(w, incident)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var output bytes.Buffer
			old := osStdout
			osStdout = &output
			t.Cleanup(func() { osStdout = old })
			args := []string{"routes", "monitor", "explain", "demo", "--incident", cliProductionIncident(t).ID}
			if scenario == "report" || scenario == "report_gate" {
				args = []string{"routes", "monitor", "report", "demo"}
				if scenario == "report_gate" {
					args = append(args, "--fail-on-unhealthy")
				}
			}
			if scenario == "incidents" {
				args = []string{"routes", "monitor", "incidents", "demo"}
			}
			if scenario == "json" {
				args = append(args, "--json")
			}
			file := filepath.Join(t.TempDir(), "incident.json")
			if scenario == "export" || scenario == "overwrite" || scenario == "symlink" {
				args = append(args, "--out", file)
			}
			if scenario == "overwrite" {
				if err := os.WriteFile(file, []byte("preserved"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "symlink" {
				if err := os.Symlink("missing-target", file); err != nil {
					t.Fatal(err)
				}
			}
			want := 0
			if strings.HasPrefix(scenario, "wrong_") || scenario == "overwrite" || scenario == "symlink" || scenario == "report_gate" {
				want = 1
			}
			if code := run(args); code != want {
				t.Fatalf("exit %d want %d: %s", code, want, output.String())
			}
			if scenario == "human" && (!strings.Contains(output.String(), "gregale debug requests inspect demo") || !strings.Contains(output.String(), "managed_binding/postgres")) {
				t.Fatal("actionable diagnostics missing")
			}
			if scenario == "export" {
				info, err := os.Stat(file)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatal("export permissions")
				}
			}
			if scenario == "overwrite" {
				body, _ := os.ReadFile(file)
				if string(body) != "preserved" {
					t.Fatal("existing file changed")
				}
			}
		})
	}
}
func TestProductionRouteMonitorCLIIntentAndEarlyValidation(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	i := cliProductionIncident(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "PUT" || r.URL.Path != "/v1/apps/demo/route-monitor" {
			t.Error("intent path")
		}
		var req api.SetRouteMonitorRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil || !req.Enabled || req.ExpectedRevision == nil || *req.ExpectedRevision != 0 || req.Routes[0].Max5xxRateBPS == nil || *req.Routes[0].Max5xxRateBPS != 0 {
			t.Error("zero budget lost")
		}
		writeJSONTest(w, api.RouteMonitorConfig{AppID: i.AppID, Enabled: true, Revision: 1, UpdatedAt: i.OpeningReport.ObservationAnchor, Routes: req.Routes})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	path := filepath.Join(t.TempDir(), "routes.json")
	if err := os.WriteFile(path, []byte(`[{"method":"POST","path":"/checkout","max_5xx_rate_bps":0}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"routes", "monitor", "set", "demo", "--mode", "enabled", "--routes", path, "--expected-revision", "0", "--json"}); code != 0 {
		t.Fatal("zero budget rejected")
	}
	for _, args := range [][]string{{"explain", "demo"}, {"incidents", "demo", "--limit", "11"}, {"set", "demo", "--mode", "bad", "--routes", path, "--expected-revision", "0"}, {"explain", "demo", "--incident", "bad"}} {
		before := calls
		if code := cmdRoutesMonitor(args); code != 1 || calls != before {
			t.Fatal("invalid input reached server")
		}
	}
	if err := os.WriteFile(path, []byte(`[{"method":"POST","path":"/checkout","max_p95_ms":100,"typo":1}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRouteMonitorRoutes(path); err == nil {
		t.Fatal("unknown budget field accepted")
	}
}
