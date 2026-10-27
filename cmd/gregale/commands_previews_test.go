package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261021080640 — self-serve previews from the CLI.

func TestCmdPreviewsRoutesAccountAndOrganizationRequests(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(api.PreviewList{Items: []api.Preview{
				{Key: "change_timeline", Label: "Change timeline", Eligible: true},
				{Key: "service_map", Label: "Service map", Joined: true, Enabled: true},
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(api.Preview{Key: "change_timeline", Label: "Change timeline", Joined: r.Method == http.MethodPut, Enabled: r.Method == http.MethodPut})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_previews")
	stdout, restore := main.CaptureStdout(t)
	defer restore()
	stderr, restoreErr := main.CaptureStderr(t)
	defer restoreErr()

	for _, args := range [][]string{
		{"previews", "list"},
		{"previews", "join", "change_timeline"},
		{"previews", "join", "change_timeline", "--org", "acme"},
		{"previews", "leave", "--org", "acme", "change_timeline"},
		{"previews", "ls", "--org", "acme"},
	} {
		if code := main.Run(args); code != 0 {
			t.Fatalf("%v exit=%d stderr=%s", args, code, stderr)
		}
	}
	want := []string{
		"GET /v1/previews",
		"PUT /v1/previews/change_timeline",
		"PUT /v1/orgs/acme/previews/change_timeline",
		"DELETE /v1/orgs/acme/previews/change_timeline",
		"GET /v1/orgs/acme/previews",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests =\n%s\nwant\n%s", strings.Join(requests, "\n"), strings.Join(want, "\n"))
	}
	out := stdout.String()
	for _, fragment := range []string{"change_timeline", "open", "service_map", "joined", "Organization acme joined the Change timeline preview (on)"} {
		if !strings.Contains(out, fragment) {
			t.Errorf("output missing %q:\n%s", fragment, out)
		}
	}
}

func TestCmdPreviewsRequiresAKey(t *testing.T) {
	_, restoreErr := main.CaptureStderr(t)
	defer restoreErr()
	if code := main.Run([]string{"previews", "join"}); code != 1 {
		t.Fatalf("join without a key exit = %d, want usage error 1", code)
	}
}
