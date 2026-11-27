package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r27: in a Compose checkout (linked to the project, no app)
// `release list` failed "project has multiple or no workloads; link again
// with --app <slug>". It now lists the account's releases, and commands that
// need one app say to pass --app.
func TestProjectOnlyLinkReleaseListAndAppAdvice(t *testing.T) {
	dir := t.TempDir()
	if _, err := main.SaveProjectContext(dir, main.LocalProjectContext{Version: 1, Project: "proj"}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	main.WithResetJSONOutput(t, false)
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/v1/deployments" {
			main.WriteJSONTest(w, api.DeploymentListResponse{})
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "test stub"))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	_, restoreOut := main.CaptureStdout(t)
	stderr, restoreErr := main.CaptureStderr(t)
	code := main.Run([]string{"release", "list"})
	restoreErr()
	restoreOut()
	mu.Lock()
	got := strings.Join(paths, " ")
	mu.Unlock()
	if code != 0 || !strings.Contains(got, "/v1/deployments") {
		t.Fatalf("release list exit %d, requests %q, stderr %s", code, got, stderr.String())
	}

	_, restoreOut = main.CaptureStdout(t)
	stderr, restoreErr = main.CaptureStderr(t)
	main.Run([]string{"app", "instances"})
	restoreErr()
	restoreOut()
	if msg := stderr.String(); !strings.Contains(msg, "pass --app <slug>") || strings.Contains(msg, "link again") {
		t.Fatalf("one-app command advice: %s", msg)
	}
}
