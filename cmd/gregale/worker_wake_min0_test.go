package main_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Hunt 14 H14-75: a worker pool's wake only reconciles it to its desired
// replicas, so with min 0 and no queued work nothing starts. The CLI said
// "Wake queued" and --wait waited out its timeout for an instance schedd
// never creates.
func TestCmdWakeExplainsAScaleToZeroWorker(t *testing.T) {
	for _, tc := range []struct {
		name     string
		min      int
		wait     bool
		wantCode int
		wantText string
	}{
		{"min 0 explains", 0, false, 0, "worker pool with min 0"},
		{"min 0 --wait refuses at once", 0, true, 1, "worker pool with min 0"},
		{"min 1 says nothing extra", 1, false, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instanceReads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/wk/wake":
					main.WriteJSONTest(w, api.AppWakeResponse{WakeID: "01995d7a-6c55-7e82-8cc8-9bd68041b5d8"})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/wk":
					app := api.AppResponse{Slug: "wk", Status: "active"}
					app.Manifest.ExecutionMode = api.ExecutionModeWorker
					app.Manifest.WorkerReplicas = &api.WorkerScaling{Min: tc.min, Max: 1}
					main.WriteJSONTest(w, app)
				case r.URL.Path == "/v1/apps/wk/instances":
					instanceReads++
					main.WriteJSONTest(w, []api.InstanceResponse{})
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			var stdout, stderr bytes.Buffer
			oldOut, oldErr := *main.OsStdout, *main.OsStderr
			*main.OsStdout, *main.OsStderr = &stdout, &stderr
			defer func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr }()
			main.ResetJSONOut(t)

			args := []string{"wk"}
			if tc.wait {
				args = []string{"--wait", "--poll-interval", "100ms", "--timeout", "30s", "wk"}
			}
			if code := main.CmdWake(args); code != tc.wantCode {
				t.Fatalf("wake = %d, want %d; stdout=%q stderr=%q", code, tc.wantCode, stdout.String(), stderr.String())
			}
			out := stdout.String() + stderr.String()
			if tc.wantText == "" && strings.Contains(out, "min 0") || tc.wantText != "" && !strings.Contains(out, tc.wantText) {
				t.Fatalf("output = %q, want %q", out, tc.wantText)
			}
			if tc.wait && instanceReads != 0 {
				t.Fatalf("--wait polled instances %d times for a pool that starts none", instanceReads)
			}
		})
	}
}
