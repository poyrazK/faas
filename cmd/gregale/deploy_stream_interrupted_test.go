package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// production-us hunt #8: a long Go image build sent no log lines, the build
// log stream was cut, and `gregale deploy` exited 3 ("stream closed; follow
// manually") although the deployment went live. An interrupted stream now
// falls through to the deployment status poll.
func TestDeployStreamInterruptionFollowsDeploymentStatus(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame string
	}{
		{"server error frame", "event: error\ndata: {\"error\":\"log stream unavailable\"}\n\n"},
		{"connection cut mid-stream", "event: log\ndata: {\"line\":\"building\"}\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/v1/deployments/d1/logs"):
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, tc.frame)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					if hj, ok := w.(http.Hijacker); ok && tc.name == "connection cut mid-stream" {
						conn, _, err := hj.Hijack()
						if err == nil {
							_ = conn.Close()
						}
					}
				case r.URL.Path == "/v1/deployments/d1":
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: statusLive})
				default:
					http.Error(w, "no", http.StatusNotFound)
				}
			}))
			defer srv.Close()
			_, restoreOut := captureStdout(t)
			stderr, restoreErr := captureStderr(t)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			code := streamDeployLogsContextWithOptions(ctx, NewClient(srv.URL, "fp_test"),
				api.DeploymentResponse{ID: "d1", Status: "building"}, "demo",
				streamDeployOptions{waitTimeout: 15 * time.Second})
			restoreErr()
			restoreOut()
			if code != 0 {
				t.Fatalf("exit=%d stderr=%q, want 0 for a deployment that went live", code, stderr.String())
			}
			if strings.Contains(stderr.String(), "follow manually") {
				t.Fatalf("stderr still tells the user to follow manually: %q", stderr.String())
			}
		})
	}
}
