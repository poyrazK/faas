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

func TestCmdObsContextRoundTripAndHumanOutput(t *testing.T) {
	ref := "compute_node/11111111-1111-4111-8111-111111111111"
	response := api.ObsIncidentContextResponse{IncidentID: ref, Scope: api.ObsIncidentContextScope{State: "known", Basis: "node"}, Counts: &api.ObsIncidentContextCounts{Apps: 3, Tenants: 2, FailedResources: 1}, AsOf: time.Now().UTC(), NextCursor: "opaque+/=", TimelineScope: "incident_scope_and_returned_resources", TimelineTruncated: true, Timeline: []api.ObsIncidentRelatedEntry{{ResourceKind: "deployment", ResourceID: "id", Match: "subject", Entry: api.ObsIncidentEvidenceEntry{ID: "9007199254740993", TracePath: "/v1/admin/obs/traces/11111111111111111111111111111111"}}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/obs/incidents/"+ref+"/context" || r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("history_limit") != "3" || r.URL.Query().Get("cursor") != "opaque+/=" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("wrong request: %s", r.URL)
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "test-token")
	var out bytes.Buffer
	oldOut, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	if code := cmdObsDispatch([]string{subObsContext, "--incident", ref, "--limit", "2", "--history-limit", "3", "--cursor", "opaque+/=", "--json"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	var got api.ObsIncidentContextResponse
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Timeline[0].Entry.ID != "9007199254740993" {
		t.Fatalf("JSON lost precision: %s err=%v", out.String(), err)
	}
	out.Reset()
	writeObsContextHuman(&out, response)
	for _, part := range []string{"current_relationships_as_of=", "related_apps=3", "explicitly_failed_resources=1", "timeline_truncated=true", "next_cursor=opaque+/=", response.Timeline[0].Entry.TracePath} {
		if !strings.Contains(out.String(), part) {
			t.Errorf("missing %q: %s", part, out.String())
		}
	}
	out.Reset()
	response.Counts = nil
	response.Scope.State = "unknown"
	writeObsContextHuman(&out, response)
	if !strings.Contains(out.String(), "counts=unknown") || strings.Contains(out.String(), "related_apps=0") {
		t.Fatalf("unknown became zero: %s", out.String())
	}
}

func TestCmdObsContextRejectsInvalidInputAndFailedHTTP(t *testing.T) {
	t.Setenv("FAAS_ADMIN_TOKEN", "")
	ref := "deployment/11111111-1111-4111-8111-111111111111"
	for _, args := range [][]string{{}, {"--incident", "deployment/not-uuid"}, {"--incident", ref, "--limit", "101"}, {"--incident", ref, "--history-limit", "0"}, {"--incident", ref, "--timeout", "0"}, {"--incident", ref}} {
		if code := cmdObsContext(args); code != 2 {
			t.Errorf("bad args exit=%d", code)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "unavailable", 503) }))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "test-token")
	var out bytes.Buffer
	old := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = old })
	if code := cmdObsContext([]string{"--incident", ref}); code != 1 || out.Len() != 0 {
		t.Fatalf("failed response rendered: code=%d body=%s", code, out.String())
	}
}
