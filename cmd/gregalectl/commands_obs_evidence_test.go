package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdObsEvidenceRoundTripAndHumanOutput(t *testing.T) {
	const ref = "deployment/11111111-1111-4111-8111-111111111111"
	response := api.ObsIncidentEvidenceResponse{
		Incident: api.ObsIncident{ID: ref, Status: "failed", RunbookURL: "/docs/runbooks/FaasDeployFailed.md"},
		History:  []api.ObsIncidentEvidenceEntry{{ID: "9007199254740993", Source: "event", Kind: "deployment.failed", TracePath: "/v1/admin/obs/traces/11111111111111111111111111111111"}},
		Limit:    2, Truncated: true, UnavailableSources: []string{"prometheus_alert_metadata"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/obs/incidents/"+ref+"/evidence" || r.URL.Query().Get("limit") != "2" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected request %s auth=%q", r.URL, r.Header.Get("Authorization"))
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
	if got := cmdObsDispatch([]string{subObsEvidence, "--incident", ref, "--limit", "2", "--json"}); got != 0 {
		t.Fatalf("exit=%d output=%s", got, out.String())
	}
	var got api.ObsIncidentEvidenceResponse
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || len(got.History) != 1 || got.History[0].ID != "9007199254740993" {
		t.Fatalf("JSON evidence lost precision: %s, %v", out.String(), err)
	}
	out.Reset()
	writeObsEvidenceHuman(&out, response)
	for _, expected := range []string{"current_status=failed", "truncated=true", "evidence snapshot is incomplete", "runbook_url=/docs/runbooks/FaasDeployFailed.md", response.History[0].TracePath} {
		if !strings.Contains(out.String(), expected) {
			t.Errorf("output missing %q: %s", expected, out.String())
		}
	}
}

func TestCmdObsEvidenceRejectsInvalidInputBeforeHTTP(t *testing.T) {
	t.Setenv("FAAS_ADMIN_TOKEN", "")
	for _, args := range [][]string{
		{}, {"--incident", "deployment/not-uuid"}, {"--incident", "deployment/../../health"},
		{"--incident", "unknown/11111111-1111-4111-8111-111111111111"},
		{"--incident", "deployment/11111111-1111-4111-8111-111111111111", "--limit", "101"},
		{"--incident", "deployment/11111111-1111-4111-8111-111111111111", "--timeout", "0"},
		{"--incident", "deployment/11111111-1111-4111-8111-111111111111"},
	} {
		if got := cmdObsEvidence(args); got != 2 {
			t.Errorf("args=%v exit=%d, want validation error", args, got)
		}
	}
}

func TestCmdObsEvidenceDoesNotRenderFailedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "source unavailable", 503) }))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "test-token")
	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	if got := cmdObsEvidence([]string{"--incident", "platform_alert/" + strings.Repeat("a", 64)}); got != 1 || out.Len() != 0 {
		t.Fatalf("failed evidence rendered as success: exit=%d output=%s", got, out.String())
	}
}
