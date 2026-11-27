package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildprogress"
)

// buildStepServer serves one deployment that builds for three reads, then
// goes live. Its build log gains a step on each log read.
func buildStepServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var depReads, logReads atomic.Int32
	steps := []string{"[build 1/3] FROM maven:3.9", "[build 2/3] RUN mvn package", "[build 2/3] RUN mvn package", "exporting to oci image format"}
	building := json.RawMessage(`{"current":"image_build","history":[]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/deployments/orders":
			dep := api.DeploymentResponse{ID: "orders", Status: "building", StageState: building}
			if depReads.Add(1) > 3 {
				dep = api.DeploymentResponse{ID: "orders", Status: "live", RolloutState: "complete", TrafficPercent: 100}
			}
			_ = json.NewEncoder(w).Encode(dep)
		case "/v1/deployments/orders/logs":
			n := int(logReads.Add(1))
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", mustJSON(t, map[string]string{"line": "dependency cache cold — installing dependencies\n"}))
			for _, step := range steps[:min(n, len(steps))] {
				_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", mustJSON(t, map[string]string{"line": buildprogress.StepLogPrefix + step + "\n"}))
			}
			_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &logReads
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A project wait showed "image_build" for a whole ten-minute Maven build
// (retail-store feedback A3). It now prints each new BuildKit step once.
func TestProjectWaitShowsBuildSteps(t *testing.T) {
	old := *main.ProjectBuildStepInterval
	*main.ProjectBuildStepInterval = 0
	t.Cleanup(func() { *main.ProjectBuildStepInterval = old })

	for _, jsonMode := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%v", jsonMode), func(t *testing.T) {
			srv, logReads := buildStepServer(t)
			var stderr bytes.Buffer
			ctx := main.WithProjectWaitProgress(t.Context(), &stderr, jsonMode)
			apply := api.ApplyResponse{Builds: []api.AppliedBuild{{Slug: "retail-orders", DeploymentID: "orders"}}}
			main.WaitForProjectApplyTimeouts(ctx, main.NewClient(srv.URL, "token"), apply, 20*time.Second)

			got := stderr.String()
			if logReads.Load() == 0 {
				t.Fatal("the wait never read the build log")
			}
			if jsonMode {
				for _, want := range []string{
					`"event":"service_build_step","service":"retail-orders","deployment_id":"orders","step":"[build 1/3] FROM maven:3.9"`,
					`"step":"[build 2/3] RUN mvn package"`,
				} {
					if !strings.Contains(got, want) {
						t.Errorf("progress lacks %q:\n%s", want, got)
					}
				}
				return
			}
			for _, want := range []string{
				"retail-orders: image_build [build 1/3] FROM maven:3.9 (",
				"retail-orders: image_build [build 2/3] RUN mvn package (",
				"retail-orders: live after",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("progress lacks %q:\n%s", want, got)
				}
			}
			if n := strings.Count(got, "RUN mvn package"); n != 1 {
				t.Errorf("step printed %d times, want once:\n%s", n, got)
			}
			if strings.Contains(got, "dependency cache cold") {
				t.Errorf("a non-step log line was shown as a step:\n%s", got)
			}
		})
	}
}
