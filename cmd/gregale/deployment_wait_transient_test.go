package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Production-us rc.297: `gregale deployment wait <id> --rollout` exited after
// 84 s on one "connection reset by peer" while the canary was still
// progressing. A wait rides out a few transient read failures (Hunt 14 H14-99).
func TestDeploymentWaitRidesOutTransientFailures(t *testing.T) {
	const id = "8172d1f8-55c1-49d6-b01d-e3d0a4e41de2"
	for _, tc := range []struct {
		name     string
		failures int
		fail     func(w http.ResponseWriter)
		wantCode int
	}{
		{name: "connection reset", failures: 2, fail: func(w http.ResponseWriter) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}, wantCode: 0},
		{name: "gateway 503", failures: 2, fail: func(w http.ResponseWriter) {
			api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Unavailable", "retry"))
		}, wantCode: 0},
		{name: "not found is final", failures: 1000, fail: func(w http.ResponseWriter) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such deployment"))
		}, wantCode: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/deployments/"+id {
					http.NotFound(w, r)
					return
				}
				if int(calls.Add(1)) <= tc.failures {
					tc.fail(w)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: id, Status: "live"})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			stdout, readStderr, restore := main.SwapIO(t)
			code := main.Run([]string{"deployment", "wait", id, "--timeout", "30"})
			stderr := readStderr()
			restore()
			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d; stdout %q stderr %q", code, tc.wantCode, stdout, stderr)
			}
			if tc.wantCode == 0 {
				if !strings.Contains(stdout.String(), "is live") || strings.Count(stderr, "still waiting") != 1 {
					t.Fatalf("stdout %q stderr %q; want one still-waiting warning and a live receipt", stdout, stderr)
				}
			} else if calls.Load() != 1 {
				t.Fatalf("a 404 was retried %d times", calls.Load()-1)
			}
		})
	}
}
