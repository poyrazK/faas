package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdObsDispatch_IncidentsRequiresToken(t *testing.T) {
	t.Setenv("FAAS_ADMIN_TOKEN", "")
	if got := cmdObsDispatch([]string{"incidents"}); got != 2 {
		t.Fatalf("cmdObsDispatch(incidents) = %d, want 2", got)
	}
}

func TestCmdObsIncidents_JSONRoundTripAndFilters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/obs/incidents" {
			t.Errorf("path = %s, want incident inbox", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-admin-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		if got := r.URL.Query().Get("type"); got != "deployment" {
			t.Errorf("type = %q, want deployment", got)
		}
		if got := r.URL.Query().Get("severity"); got != "error" {
			t.Errorf("severity = %q, want error", got)
		}
		if got := r.URL.Query().Get("limit"); got != "3" {
			t.Errorf("limit = %q, want 3", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ObsIncidentListResponse{
			Items: []api.ObsIncident{{ID: "deployment/d1", Type: "deployment", Severity: "error", Status: "failed", Summary: "deployment failed"}},
			Limit: 3,
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "test-admin-token")

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()
	exit := cmdObsIncidents([]string{"--json", "--type", "deployment", "--severity", "error", "--limit", "3"})
	_ = w.Close()
	var out bytes.Buffer
	_, _ = out.ReadFrom(r)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; output=%s", exit, out.String())
	}
	var response api.ObsIncidentListResponse
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("decode JSON output: %v; output=%s", err, out.String())
	}
	if len(response.Items) != 1 || !strings.HasPrefix(response.Items[0].ID, "deployment/") {
		t.Fatalf("unexpected response: %+v", response)
	}
}
