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

// Prod hunt r27: `app wake-timeline bh29-front` in a project checkout read
// the slug as a wake ID of the linked app and failed "project has multiple
// or no workloads". A lone app slug now shows that app's most recent wake.
func TestWakeTimelineOfAnAppShowsItsLatestWake(t *testing.T) {
	got := main.LatestWakeID([]api.InstanceResponse{
		{WakeID: "w-old", StartedAt: "2026-11-22T14:00:00Z"},
		{WakeID: "", StartedAt: "2026-11-22T15:30:00Z"},
		{WakeID: "w-new", StartedAt: "2026-11-22T15:00:00Z"},
	})
	if got != "w-new" {
		t.Fatalf("latest wake = %q, want w-new", got)
	}
	if main.LatestWakeID(nil) != "" {
		t.Fatal("no instances should pick no wake")
	}

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
		switch r.URL.Path {
		case "/v1/apps/bh29-front/instances":
			main.WriteJSONTest(w, []api.InstanceResponse{{WakeID: "01a203bc-cc32-722d-8eb0-4c416b9990de", StartedAt: "2026-11-22T14:50:35Z"}})
		case "/v1/apps/bh29-front/wakes/01a203bc-cc32-722d-8eb0-4c416b9990de/timeline":
			main.WriteJSONTest(w, api.WakeTimelineResponse{})
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "test stub"))
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	_, restoreOut := main.CaptureStdout(t)
	stderr, restoreErr := main.CaptureStderr(t)
	main.Run([]string{"app", "wake-timeline", "bh29-front"})
	restoreErr()
	restoreOut()
	if strings.Contains(stderr.String(), "multiple or no workloads") {
		t.Fatalf("slug read as a wake ID: %s", stderr.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(strings.Join(paths, " "), "/wakes/01a203bc-cc32-722d-8eb0-4c416b9990de/timeline") {
		t.Fatalf("requests %v, want the latest wake's timeline; stderr: %s", paths, stderr.String())
	}
}
