// adr: 679
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRegistryPublishedCIHandoff(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   string
		wait     bool
		wantCode int
	}{
		{"accepted", "pending", false, 0},
		{"wait live", "live", true, 0},
		{"failed replay", "failed", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			image := "ghcr.io/team/api@sha256:" + strings.Repeat("a", 64)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					if r.URL.Path != "/v1/apps/api/image-published" || r.Header.Get("Authorization") != "Bearer fp_live_ci" {
						t.Errorf("CI handoff = %s %s", r.Method, r.URL.Path)
					}
					var req api.CreateDeploymentRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Image != image || req.Scope != "staging" {
						t.Errorf("published request = %+v, %v", req, err)
					}
				} else if !strings.HasPrefix(r.URL.Path, "/v1/deployments/") {
					t.Errorf("unexpected source build request: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "published", Status: tc.status, ImageDigest: image})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_ci")
			var stdout, stderr bytes.Buffer
			oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
			osStdout, osStderr, jsonOutput = &stdout, &stderr, true
			t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
			args := []string{"published", "--app", "api", "--image", image, "--scope", "staging"}
			if tc.wait {
				args = append(args, "--wait")
			}
			if code := cmdRegistry(args); code != tc.wantCode {
				t.Fatalf("exit = %d, want %d; stdout=%s stderr=%s", code, tc.wantCode, stdout.String(), stderr.String())
			}
			var got api.DeploymentResponse
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got.ID != "published" || got.Status != tc.status {
				t.Fatalf("receipt = %+v, %v: %s", got, err, stdout.String())
			}
		})
	}
}
