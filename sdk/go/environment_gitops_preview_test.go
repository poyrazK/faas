// adr: 1155 — candidate assessment and customer status feedback contracts.
package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestGitOpsCandidatePlanTransport(t *testing.T) {
	sha := strings.Repeat("a", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/projects/shop/environments/staging/gitops/plan" || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("wrong candidate request: %s %s", r.Method, r.URL.Path)
		}
		var request faas.PreviewEnvironmentGitRevisionRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.CommitSHA != sha {
			t.Errorf("wrong candidate SHA: %+v", request)
		}
		_ = json.NewEncoder(w).Encode(faas.PreviewEnvironmentGitPlanResponse{SourceID: "source", ReadOnly: true, ValuesRedacted: true, Plan: faas.EnvironmentGitOpsPlan{CommitSHA: sha, Generation: 0, ObservedVersion: 7, Hash: strings.Repeat("b", 64)}})
	}))
	t.Cleanup(server.Close)
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := client.PreviewEnvironmentGitPlan(context.Background(), "shop", "staging", faas.PreviewEnvironmentGitRevisionRequest{CommitSHA: sha})
	if err != nil || !preview.ReadOnly || !preview.ValuesRedacted || preview.Plan.CommitSHA != sha || preview.Plan.ObservedVersion != 7 || preview.Plan.Generation != 0 {
		t.Fatalf("candidate receipt lost: %+v %v", preview, err)
	}
}
