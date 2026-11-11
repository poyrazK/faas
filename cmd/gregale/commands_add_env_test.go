package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// Outside a linked project a project app's bucket needs --env, and the CLI
// says why (Snapbox SB-2). A standalone app reads the default scope, so the
// CLI uses it instead of asking for --env default (Hunt 32 H32-23).
func TestAddBucketWithoutEnv(t *testing.T) {
	for _, tc := range []struct {
		name      string
		app       string
		wantAsked bool
	}{
		{"project app", `{"id":"a1","slug":"my-api","project_id":"p1"}`, true},
		{"standalone app", `{"id":"a1","slug":"my-api"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var beyondApp atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/apps/my-api" {
					_, _ = w.Write([]byte(tc.app))
					return
				}
				beyondApp.Store(true)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			t.Cleanup(srv.Close)
			t.Setenv("HOME", t.TempDir())
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			main.WithCwd(t, t.TempDir())
			stderr, restore := main.CaptureStderr(t)
			code := main.CmdAddBucket([]string{"files", "--app", "my-api"})
			restore()
			asked := strings.Contains(stderr.String(), "--env is required outside a linked project")
			if asked != tc.wantAsked || code == 0 && tc.wantAsked {
				t.Fatalf("exit = %d, stderr = %q, want asked=%t", code, stderr.String(), tc.wantAsked)
			}
			if !tc.wantAsked && !beyondApp.Load() {
				t.Fatalf("a standalone app's bucket did not proceed past the app lookup: %q", stderr.String())
			}
		})
	}
}
