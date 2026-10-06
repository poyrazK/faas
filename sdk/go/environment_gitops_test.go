package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestEnvironmentGitOpsReviewedRevisionAndOverrideTransport(t *testing.T) {
	sha, digest := strings.Repeat("a", 40), strings.Repeat("b", 64)
	verified := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing bearer token")
		}
		switch requests {
		case 1:
			if r.Method != http.MethodPost || r.URL.EscapedPath() != "/v1/projects/my%20project/environments/production/gitops/revisions/preview" {
				t.Errorf("review request: %s %s", r.Method, r.URL.EscapedPath())
			}
			_ = json.NewEncoder(w).Encode(faas.PreviewEnvironmentGitRevisionResponse{CommitSHA: sha, DefinitionDigest: digest, Generation: 9, Definition: json.RawMessage(`{}`)})
		case 2:
			var request faas.ApproveEnvironmentGitRevisionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ExpectedGeneration != 9 || request.CommitSHA != sha || request.DefinitionDigest != digest {
				t.Errorf("approval: %+v %v", request, err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{}`))
		case 3:
			if r.Method != http.MethodDelete || !strings.HasSuffix(r.URL.Path, "/gitops/overrides") {
				t.Errorf("override removal: %s %s", r.Method, r.URL.Path)
			}
			var request faas.RemoveEnvironmentGitOpsOverrideRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Resource != "workload/api" || request.Path != "variables/MODE" {
				t.Errorf("override field: %+v %v", request, err)
			}
			w.WriteHeader(http.StatusNoContent)
		case 4:
			if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/gitops") {
				t.Errorf("status: %s %s", r.Method, r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(faas.EnvironmentGitOpsStatusResponse{Source: faas.EnvironmentGitSource{
				ApprovedRevisionID: "approved", SourceCommitSHA: sha, SourceDefinitionDigest: digest, SourceVerifiedAt: &verified,
				SourceErrorCode: "environment_git_source_unavailable",
			}})
		}
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	review, err := client.PreviewEnvironmentGitRevision(ctx, "my project", "production", faas.PreviewEnvironmentGitRevisionRequest{CommitSHA: sha})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ApproveEnvironmentGitRevision(ctx, "my project", "production", faas.ApproveEnvironmentGitRevisionRequest{CommitSHA: review.CommitSHA, DefinitionDigest: review.DefinitionDigest, ExpectedGeneration: review.Generation}); err != nil {
		t.Fatal(err)
	}
	if err := client.RemoveEnvironmentGitOpsOverride(ctx, "my project", "production", faas.RemoveEnvironmentGitOpsOverrideRequest{Resource: "workload/api", Path: "variables/MODE"}); err != nil {
		t.Fatal(err)
	}
	status, err := client.GetEnvironmentGitOps(ctx, "my project", "production")
	if err != nil || status.Source.SourceCommitSHA != sha || status.Source.SourceDefinitionDigest != digest ||
		status.Source.SourceVerifiedAt == nil || !status.Source.SourceVerifiedAt.Equal(verified) ||
		status.Source.SourceErrorCode != "environment_git_source_unavailable" || status.Source.ApprovedRevisionID != "approved" {
		t.Fatalf("source availability/authority: %+v %v", status, err)
	}
	if requests != 4 {
		t.Fatalf("requests: %d", requests)
	}
}
