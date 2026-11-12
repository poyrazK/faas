package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1448 — amendment 1: `gregale release resolve` records a database
// check after a release command whose outcome is unknown.
func TestReleaseResolveRecordsTheCheck(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	var got api.RecordReleaseOutcomeCheckRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/deployments/"+id+"/release-outcome" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ReleaseOutcomeCheck{DeploymentID: id, TaskID: "t1", TaskStatus: "failed",
			CheckedBy: "ops@example.com", Note: got.Note, CheckedAt: time.Now()})
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	var output bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &output
	t.Cleanup(func() { *main.OsStdout = old })

	if code := main.CmdDeployment([]string{"resolve", id}); code == 0 {
		t.Fatal("resolve without --note succeeded")
	}
	if code := main.CmdDeployment([]string{"resolve", id, "--note", "migration 42 applied"}); code != 0 {
		t.Fatalf("resolve exit = %d", code)
	}
	if got.Note != "migration 42 applied" || !strings.Contains(output.String(), "Later release commands run again") {
		t.Fatalf("request note = %q, output = %q", got.Note, output.String())
	}
}
