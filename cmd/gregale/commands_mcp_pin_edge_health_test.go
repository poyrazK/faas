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

// The gateway answers an app's health path itself ({"status":"ok",
// "source":"edge"}), so the public /healthz probe never saw the catalog
// digest and --require-catalog-pin refused every first deploy. The pin now
// reads it from a running replica through the exact-instance probe.
func TestMCPCatalogPinReadsReplicaWhenEdgeAnswersHealth(t *testing.T) {
	digest := strings.Repeat("b", 64)
	const deploymentID = "0b0f8c1e-6f0e-4d5c-9a3b-2f6f7f5a4c11"
	const instanceID = "5a9e2d44-1c3b-4b8e-8f61-0d3c2b1a9e77"
	preview := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Faas-Health-Source", "edge")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","source":"edge"}`))
	}))
	defer preview.Close()
	replicaProbes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/url"):
			_ = json.NewEncoder(w).Encode(map[string]any{"url": preview.URL, "alive": true})
		case strings.HasSuffix(r.URL.Path, "/instances"):
			_ = json.NewEncoder(w).Encode([]api.InstanceResponse{{ID: instanceID, DeploymentID: deploymentID, State: "running"}})
		case strings.HasSuffix(r.URL.Path, "/mcp-health"):
			replicaProbes++
			_ = json.NewEncoder(w).Encode(api.MCPInstanceHealthProbeResponse{AppID: "app", DeploymentID: deploymentID, InstanceID: instanceID, HTTPStatus: http.StatusOK,
				HealthBody: fmt.Sprintf(`{"status":"ok","catalog_revision":1,"catalog_sha256":%q}`, digest)})
		default:
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "pinned", OverrideEnvKeys: []string{"MCP_CATALOG_EXPECTED_SHA256"}})
		}
	}))
	defer server.Close()
	submit := func(_ []string, env map[string]string) (api.DeploymentResponse, error) {
		if env["MCP_CATALOG_EXPECTED_SHA256"] != digest {
			t.Fatalf("pin override = %v, want the replica's digest", env)
		}
		return api.DeploymentResponse{ID: "pinned"}, nil
	}
	pinned, gotDigest, err := main.PinMCPCandidateWithSubmit(context.Background(), api.NewClient(server.URL, "token"), "key",
		mcphosting.Config{Endpoint: "/mcp"}, api.DeploymentResponse{ID: deploymentID}, submit)
	if err != nil || pinned.ID != "pinned" || gotDigest != digest || replicaProbes != 1 {
		t.Fatalf("pinned=%s digest=%s probes=%d err=%v", pinned.ID, gotDigest, replicaProbes, err)
	}
}
