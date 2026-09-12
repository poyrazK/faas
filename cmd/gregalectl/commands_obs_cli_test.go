package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdObsDispatch_OverviewAndCapacityRequireToken(t *testing.T) {
	t.Setenv("FAAS_ADMIN_TOKEN", "")
	if got := cmdObsDispatch([]string{subObsOverview}); got != 2 {
		t.Fatalf("cmdObsDispatch(overview) = %d, want 2", got)
	}
	if got := cmdObsDispatch([]string{subObsCapacity}); got != 2 {
		t.Fatalf("cmdObsDispatch(capacity) = %d, want 2", got)
	}
}

func TestCmdObsOverview_JSONRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/obs/overview" {
			t.Errorf("path = %s, want overview", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-admin-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ObsOverviewResponse{
			Totals:     api.ObsOverviewTotals{AccountsActive: 4, NodesActive: 2},
			NodeHealth: []api.ObsOverviewNodeHealth{{Name: "node-a", Active: true}},
		})
	}))
	defer server.Close()
	t.Setenv("FAAS_APID_URL", server.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "test-admin-token")

	var out bytes.Buffer
	oldOut, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })

	if got := cmdObsOverview([]string{"--json"}); got != 0 {
		t.Fatalf("cmdObsOverview exit = %d; output=%s", got, out.String())
	}
	var response api.ObsOverviewResponse
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("decode JSON output: %v; output=%s", err, out.String())
	}
	if response.Totals.AccountsActive != 4 || len(response.NodeHealth) != 1 {
		t.Fatalf("unexpected overview response: %+v", response)
	}
}

func TestCmdObsCapacity_JSONRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/obs/capacity" {
			t.Errorf("path = %s, want capacity", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-admin-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ObsCapacityResponse{
			Summary: api.ObsCapacitySummary{TotalNodes: 1, AdmissionMarginMB: 512},
			Nodes:   []api.ObsCapacityNode{{Name: "node-a", Active: true, MemMB: 2048}},
		})
	}))
	defer server.Close()
	t.Setenv("FAAS_APID_URL", server.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "test-admin-token")

	var out bytes.Buffer
	oldOut, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })

	if got := cmdObsCapacity([]string{"--json"}); got != 0 {
		t.Fatalf("cmdObsCapacity exit = %d; output=%s", got, out.String())
	}
	var response api.ObsCapacityResponse
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("decode JSON output: %v; output=%s", err, out.String())
	}
	if response.Summary.TotalNodes != 1 || len(response.Nodes) != 1 {
		t.Fatalf("unexpected capacity response: %+v", response)
	}
}

func TestWriteObsCapacityHuman_IncludesHeadroom(t *testing.T) {
	var out bytes.Buffer
	writeObsCapacityHuman(&out, api.ObsCapacityResponse{
		GeneratedAt: time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC),
		Summary:     api.ObsCapacitySummary{AdmissionMarginMB: 512, UnplacedApps: 2},
		Nodes:       []api.ObsCapacityNode{{Name: "node-a", Active: true, AdmissionMarginMB: 512}},
	})
	if !strings.Contains(out.String(), "admission_margin_mb=512") {
		t.Fatalf("human output missing admission margin: %s", out.String())
	}
	if !strings.Contains(out.String(), "unplaced_apps=2") {
		t.Fatalf("human output missing unplaced apps: %s", out.String())
	}
}
