package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPromotionMigrationCLIContract(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(map[bool]string{false: "old_server", true: "migration_server"}[supported], func(t *testing.T) {
			resetJSONOut(t)
			oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
			var out, errOut bytes.Buffer
			osStdout, osStderr, jsonOutput = &out, &errOut, true
			t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
			posts := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/promotion-preview") {
					if r.URL.Query().Get("migrate") != "true" || r.URL.Query().Get("verify") != "true" || r.URL.Query().Get("sync_config") != "true" {
						t.Error("migration query missing")
					}
					preview := api.ProjectEnvironmentPromotionPreviewResponse{ProjectSlug: "shop", FromEnvironment: "staging", ToEnvironment: "production", CanPromote: true, PromotionToken: "token"}
					if supported {
						preview.Health = &api.ProjectPromotionHealthPolicy{Migration: &api.ProjectPromotionMigrationPolicy{AppID: "app", Slug: "api", CommandHash: strings.Repeat("a", 64), Compatibility: "backward-compatible"}}
					}
					writeJSONTest(w, preview)
					return
				}
				posts++
				w.WriteHeader(http.StatusAccepted)
				writeJSONTest(w, api.ProjectEnvironmentPromotionResponse{ProjectSlug: "shop", PromotionID: "migration", Status: "running"})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			code := cmdProjectsEnvironmentPromote([]string{"shop", "--from", "staging", "--to", "production", "--sync-config", "--verify", "--migrate", "--yes"})
			if supported && (code != 0 || posts != 1) || !supported && (code != 1 || posts != 0) {
				t.Fatalf("exit=%d posts=%d output=%s errors=%s", code, posts, &out, &errOut)
			}
		})
	}
}
