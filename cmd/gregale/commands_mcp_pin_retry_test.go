package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPCatalogPinSeparatesRetryIdentityAndConfirmsOverride(t *testing.T) {
	for _, scenario := range []string{"retry", "replayed discovery", "missing override", "confirmation unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			digest := strings.Repeat("a", 64)
			preview := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/healthz" {
					t.Errorf("unexpected probe path %s", r.URL.Path)
				}
				_, _ = fmt.Fprintf(w, `{"status":"ok","catalog_revision":1,"catalog_sha256":%q}`, digest)
			}))
			defer preview.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/url") {
					_ = json.NewEncoder(w).Encode(map[string]any{"url": preview.URL, "alive": true})
					return
				}
				if scenario == "confirmation unavailable" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				keys := []string{"MCP_CATALOG_EXPECTED_SHA256"}
				if scenario == "missing override" {
					keys = nil
				}
				_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "pinned", OverrideEnvKeys: keys})
			}))
			defer server.Close()
			const originalKey = "customer-release-123"
			var pinKey string
			calls := 0
			submit := func(args []string, env map[string]string) (api.DeploymentResponse, error) {
				calls++
				key := ""
				for i, arg := range args {
					if arg == "--idempotency-key" && i+1 < len(args) {
						key = args[i+1]
					}
				}
				if key == "" || key == originalKey || pinKey != "" && pinKey != key {
					t.Fatalf("pin retry key %q must be distinct from discovery and stable across retries (%q)", key, pinKey)
				}
				pinKey = key
				if env["MCP_CATALOG_EXPECTED_SHA256"] != digest {
					t.Fatalf("missing candidate digest in override: %v", env)
				}
				id := "pinned"
				if scenario == "replayed discovery" {
					id = "discovery"
				}
				return api.DeploymentResponse{ID: id}, nil
			}
			for attempt := 0; attempt < 2; attempt++ {
				pinned, gotDigest, err := main.PinMCPCandidateWithSubmit(context.Background(), api.NewClient(server.URL, "token"), originalKey,
					mcphosting.Config{Endpoint: "/mcp"}, api.DeploymentResponse{ID: "discovery"}, submit)
				if scenario == "retry" {
					if err != nil || pinned.ID != "pinned" || gotDigest != digest {
						t.Fatalf("pin attempt %d: deployment=%s digest=%s error=%v", attempt, pinned.ID, gotDigest, err)
					}
				} else if err == nil {
					t.Fatalf("unsafe candidate accepted in scenario %s", scenario)
				}
			}
			if calls != 2 {
				t.Fatalf("submissions=%d, want two retry attempts", calls)
			}
		})
	}
}
