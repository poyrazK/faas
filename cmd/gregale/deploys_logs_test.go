package main_test

import (
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/cmd/gregale"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// deploys logs prints a deployment's build log whatever its status; a live
// deployment's build log had no CLI command (H10-5).
func TestCmdDeploysLogsPrintsBuildLog(t *testing.T) {
	main.ResetJSONOut(t)
	const id = "0123456789abcdef0123456789abcdef"
	var limit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/deployments/"+id+"/logs" {
			http.NotFound(w, r)
			return
		}
		limit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range []string{"build started", "dependency cache restored"} {
			entry, _ := json.Marshal(map[string]string{"line": line})
			_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", entry)
		}
		_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	stdout, restore := main.CaptureStdout(t)
	code := main.CmdDeploys([]string{"logs", id, "--limit", "20"})
	restore()
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if out := stdout.String(); !strings.Contains(out, "build started\ndependency cache restored") {
		t.Fatalf("stdout = %q", out)
	}
	if limit != "20" {
		t.Fatalf("limit query = %q, want 20", limit)
	}
}
