package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPromotionSecretCLI(t *testing.T) {
	for _, scenario := range []string{"accepted", "old_server", "wrong_version", "missing_health", "missing_confirmation", "workers"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			previousOut, previousErr, previousJSON := osStdout, osStderr, jsonOutput
			var out, errOut bytes.Buffer
			osStdout, osStderr = &out, &errOut
			t.Cleanup(func() { osStdout, osStderr, jsonOutput = previousOut, previousErr, previousJSON })
			secretFile := filepath.Join(t.TempDir(), "production.json")
			if err := os.WriteFile(secretFile, []byte(`{"api":{"TOKEN":"only-the-staging-endpoint-receives-this"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			version := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			staged, previewed, approved, submitted := 0, 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if !strings.HasSuffix(r.URL.Path, "/release-secrets") && bytes.Contains(body, []byte("only-the-staging-endpoint")) {
					t.Error("secret value escaped staging")
				}
				switch {
				case r.URL.Path == "/v1/projects/shop/release-secrets":
					staged++
					writeJSONTest(w, api.ProjectReleaseSecretStageResponse{RequestID: strings.Repeat("a", 32), Versions: map[string]map[string]string{"api": {"TOKEN": version}}})
				case strings.HasSuffix(r.URL.Path, "/promotion-preview"):
					previewed++
					var req api.ProjectEnvironmentPromotionSecretPreviewRequest
					if r.Method != http.MethodPost || json.Unmarshal(body, &req) != nil || req.Secrets["api"]["TOKEN"] != version {
						t.Error("preview did not bind staged version")
					}
					preview := api.ProjectEnvironmentPromotionPreviewResponse{ProjectSlug: "shop", FromEnvironment: "staging", ToEnvironment: "production", SyncConfig: true, CanPromote: true, ApprovalRequired: true, PromotionToken: "exact-preview", Health: &api.ProjectPromotionHealthPolicy{}, Secrets: &api.ProjectReleaseSecretConfiguration{Versions: req.Secrets}}
					if scenario == "old_server" {
						preview.Secrets = nil
					}
					if scenario == "wrong_version" {
						preview.Secrets.Versions["api"]["TOKEN"] = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
					}
					if scenario == "missing_health" {
						preview.Health = nil
					}
					writeJSONTest(w, preview)
				case strings.HasSuffix(r.URL.Path, "/approvals"):
					approved++
					var req api.CreateProjectEnvironmentApprovalRequest
					if json.Unmarshal(body, &req) != nil || req.PromotionToken != "exact-preview" {
						t.Error("approval changed preview identity")
					}
					writeJSONTest(w, api.ProjectEnvironmentApprovalResponse{ApprovalToken: "approval"})
				case strings.HasSuffix(r.URL.Path, "/promote"):
					submitted++
					var req api.PromoteProjectEnvironmentRequest
					if json.Unmarshal(body, &req) != nil || req.PromotionToken != "exact-preview" || req.ApprovalToken != "approval" {
						t.Error("execute lost approval")
					}
					writeJSONTest(w, api.ProjectEnvironmentPromotionResponse{PromotionID: "promotion", ProjectSlug: "shop", Status: "running"})
				default:
					t.Error("unexpected source/build request")
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fixture-token")
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			args := []string{"projects", "environments", "promote", "shop", "--from", "staging", "--to", "production", "--sync-config", "--verify", "--release-secrets-file", secretFile, "--release-secrets-request-id", strings.Repeat("a", 32), "--json"}
			if scenario != "missing_confirmation" {
				args = append(args, "--yes")
			}
			if scenario == "workers" {
				args = append(args, "--workers")
			}
			code := run(args)
			if scenario == "accepted" {
				if code != 0 || staged != 1 || previewed != 1 || approved != 1 || submitted != 1 {
					t.Fatalf("incomplete flow: code=%d staged=%d preview=%d approval=%d submit=%d %s", code, staged, previewed, approved, submitted, &errOut)
				}
			} else if code == 0 || submitted != 0 || approved != 0 {
				t.Fatalf("unsafe secret promotion submitted: code=%d approval=%d submit=%d", code, approved, submitted)
			}
			if (scenario == "missing_confirmation" || scenario == "workers") && staged != 0 {
				t.Fatal("invalid options staged credentials")
			}
			if strings.Contains(out.String()+errOut.String(), "only-the-staging-endpoint") {
				t.Fatal("plaintext in CLI output")
			}
		})
	}
}
