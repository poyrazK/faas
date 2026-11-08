package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ChronoLake finding 29(3): before the first publication nothing is live, so
// diff must not report automatic starts as enabled.
func TestAutomationDiffFirstPublicationIsNotLive(t *testing.T) {
	draft := automationDiffSpec(t, `{"name":"flow","trigger":{"type":"schedule","schedule":"0 9 * * *","enabled":false},"steps":[{"name":"send","path":"/send"}]}`)
	report, err := main.DiffAutomationDefinition(api.AutomationResponse{Name: "flow", Version: 18, Enabled: true, Draft: draft})
	if err != nil {
		t.Fatal(err)
	}
	if !report.FirstPublication || report.LiveEnabled {
		t.Fatalf("first publication reported live_enabled=%t", report.LiveEnabled)
	}
}

// ChronoLake finding 29(2): revision listings name the number that
// `revisions show --revision` and `restore --revision` accept.
func TestAutomationRevisionsListReportsRevisionKey(t *testing.T) {
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":1,"limit":50,"offset":0,"revisions":[{"version":19,"definition":{"name":"flow","steps":[]},"definition_hash":"abc","recorded_at":"2026-11-01T00:00:00Z"}]}`))
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "token")
	output := main.CaptureAutomationStdout(t)
	if code := main.CmdAutomations([]string{"revisions", "list", "--app", "billing", "--name", "flow"}); code != 0 {
		t.Fatalf("exit=%d output=%s", code, output)
	}
	var got struct {
		Revisions []map[string]json.RawMessage `json:"revisions"`
	}
	if err := json.Unmarshal(output.Bytes(), &got); err != nil || len(got.Revisions) != 1 {
		t.Fatalf("output=%s err=%v", output, err)
	}
	if string(got.Revisions[0]["revision"]) != "19" || string(got.Revisions[0]["version"]) != "19" {
		t.Fatalf("revision keys = %s", output)
	}
}
