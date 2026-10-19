package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/obsrulecatalog"
	"github.com/onebox-faas/faas/pkg/promql"
)

func recordingTestResponse(t *testing.T) api.ObsRecordingCoverage {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"groups":[]}}`))
	}))
	defer srv.Close()
	return operatorobs.RecordingCoverage(t.Context(), promql.NewClient(srv.URL, srv.Client()), obsrulecatalog.CoreSetID, time.Now().UTC())
}

func TestCmdObsRecordingCoverage(t *testing.T) {
	out, stderr := obsBundleTestStreams(t)
	response := operatorobs.RecordingCoverage(t.Context(), nil, "", time.Now().UTC())
	selected := ""
	calls := 0
	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != operatorobs.RecordingCoveragePath || r.URL.Query().Get("expected_set") != selected || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		if mode == "error" {
			http.Error(w, "private upstream body", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	if code := cmdObsDispatch([]string{subObsRecordingCoverage}); code != 0 || !strings.Contains(out.String(), "totals=unknown") || !strings.Contains(out.String(), "available_set=gregale-core-v1") {
		t.Fatalf("unselected: %d %s %s", code, out, stderr)
	}
	response = recordingTestResponse(t)
	selected = obsrulecatalog.CoreSetID
	out.Reset()
	if code := cmdObsRecordingCoverage([]string{"--expected-set", selected}); code != 0 || !strings.Contains(out.String(), "recording_coverage=degraded") || !strings.Contains(out.String(), "state=missing") || !strings.Contains(out.String(), "dependency_mapping=partial") || !strings.Contains(out.String(), "runtime_impact=unverified") {
		t.Fatalf("missing inventory: %d %s %s", code, out, stderr)
	}
	out.Reset()
	if code := cmdObsRecordingCoverage([]string{"--expected-set", selected, "--json"}); code != 0 || !strings.Contains(out.String(), `"selection_source": "explicit_request"`) || !strings.Contains(out.String(), `"changed_fields": []`) {
		t.Fatalf("JSON: %d %s %s", code, out, stderr)
	}
	before := calls
	for _, args := range [][]string{{"--expected-set", ""}, {"--expected-set", "unknown"}, {"--expected-set", selected, "--expected-set", selected}, {"--timeout", "0s"}, {"extra"}, {"--since", "now"}} {
		if code := cmdObsRecordingCoverage(args); code != 2 {
			t.Fatalf("invalid args: %v code=%d", args, code)
		}
	}
	t.Setenv("FAAS_ADMIN_TOKEN", "")
	if code := cmdObsRecordingCoverage(nil); code != 2 || calls != before {
		t.Fatal("bad flags or missing token made request")
	}
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	mode = "error"
	out.Reset()
	stderr.Reset()
	if code := cmdObsRecordingCoverage([]string{"--expected-set", selected}); code != 1 || out.Len() != 0 || strings.Contains(stderr.String(), "private") {
		t.Fatalf("source error redaction: %d %s %s", code, out, stderr)
	}
	mode = "ok"
	response.Items[0].Comparison.Reason = "private failure"
	out.Reset()
	stderr.Reset()
	if code := cmdObsRecordingCoverage([]string{"--expected-set", selected, "--json"}); code != 1 || out.Len() != 0 || strings.Contains(stderr.String(), "private") {
		t.Fatalf("invalid response leaked: %d %s %s", code, out, stderr)
	}
}
