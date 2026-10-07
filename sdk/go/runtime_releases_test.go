package faas_test

// adr: 682

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestRuntimeReleaseReadsAndEncodedPreview(t *testing.T) {
	target := "candidate&literal=value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error("read request", r.Method)
		}
		if strings.HasSuffix(r.URL.Path, "upgrade-preview") {
			if r.URL.Query().Get("target") != target {
				t.Error("query was not escaped", r.URL)
			}
			_ = json.NewEncoder(w).Encode(faas.RuntimeUpgradePreviewResponse{Disposition: "blocked", ExecutionAvailable: false})
			return
		}
		_ = json.NewEncoder(w).Encode(faas.DeploymentRuntimeResponse{DeploymentID: "deployment", Status: "unknown"})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	current, err := client.GetDeploymentRuntime(context.Background(), "deployment")
	if err != nil || current.Status != "unknown" {
		t.Fatal(current, err)
	}
	preview, err := client.PreviewRuntimeUpgrade(context.Background(), "deployment", target)
	if err != nil || preview.Disposition != "blocked" || preview.ExecutionAvailable {
		t.Fatal(preview, err)
	}
}
