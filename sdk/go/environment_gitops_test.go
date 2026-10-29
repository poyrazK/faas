package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/poyrazK/faas/sdk/go/preview"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestEnvironmentGitOpsRolloutWebhookPreservesEvidenceAndDiagnostics(t *testing.T) {
	for _, status := range []string{"serving", "failed", "superseded"} {
		t.Run(status, func(t *testing.T) {
			body := `{"version":1,"transition_id":"transition","source_id":"source","project":"shop","environment":"production","axis":"rollout","previous_status":"progressing","status":"` + status + `","generation":3,"observed_version":7,"approved_revision_id":"revision","commit_sha":"` + strings.Repeat("a", 40) + `","definition_digest":"` + strings.Repeat("b", 64) + `","graph_id":"graph","plan_hash":"` + strings.Repeat("c", 64) + `","observed_at":"2026-10-17T12:00:00Z","dashboard_path":"/dashboard/projects/shop/environments/production/gitops","changes":[],"blocking_reasons":["environment_workload_build_failed"],"details_truncated":false,"diagnostics":[{"resource":"workload/api","stage":"preparation","code":"environment_workload_build_failed","actor":"customer","message":"The candidate build failed.","action":"Inspect build diagnostics."}]}`
			var payload preview.EnvironmentGitOpsChangedWebhookPayload
			if err := json.Unmarshal([]byte(body), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Axis != "rollout" || payload.Status != status || payload.Generation != 3 || payload.ObservedVersion != 7 || payload.DefinitionDigest != strings.Repeat("b", 64) ||
				payload.GraphID != "graph" || payload.PlanHash != strings.Repeat("c", 64) || payload.CommitSHA != strings.Repeat("a", 40) || len(payload.Diagnostics) != 1 {
				t.Fatal("rollout identity or guidance lost", payload)
			}
			var diagnostic preview.EnvironmentGitOpsDiagnostic = payload.Diagnostics[0]
			if diagnostic.Resource != "workload/api" || diagnostic.Code != "environment_workload_build_failed" || diagnostic.Action == "" {
				t.Fatal("safe guidance lost", diagnostic)
			}
			encoded, err := json.Marshal(payload)
			var before, after any
			if err != nil || json.Unmarshal([]byte(body), &before) != nil || json.Unmarshal(encoded, &after) != nil {
				t.Fatal(err)
			}
			original, _ := json.Marshal(before)
			roundTrip, _ := json.Marshal(after)
			if string(original) != string(roundTrip) {
				t.Fatalf("wire metadata changed: %s", encoded)
			}
		})
	}
}

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
			_ = json.NewEncoder(w).Encode(preview.PreviewEnvironmentGitRevisionResponse{CommitSHA: sha, DefinitionDigest: digest, Generation: 9, Definition: json.RawMessage(`{}`)})
		case 2:
			var request preview.ApproveEnvironmentGitRevisionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ExpectedGeneration != 9 || request.CommitSHA != sha || request.DefinitionDigest != digest {
				t.Errorf("approval: %+v %v", request, err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{}`))
		case 3:
			if r.Method != http.MethodDelete || !strings.HasSuffix(r.URL.Path, "/gitops/overrides") {
				t.Errorf("override removal: %s %s", r.Method, r.URL.Path)
			}
			var request preview.RemoveEnvironmentGitOpsOverrideRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Resource != "workload/api" || request.Path != "variables/MODE" {
				t.Errorf("override field: %+v %v", request, err)
			}
			w.WriteHeader(http.StatusNoContent)
		case 4:
			if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/gitops") {
				t.Errorf("status: %s %s", r.Method, r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(preview.EnvironmentGitOpsStatusResponse{Source: preview.EnvironmentGitSource{
				ApprovedRevisionID: "approved", SourceCommitSHA: sha, SourceDefinitionDigest: digest, SourceVerifiedAt: &verified,
				SourceErrorCode: "environment_git_source_unavailable",
			}, WorkloadEvidence: &preview.EnvironmentWorkloadActivationEvidence{CommitSHA: strings.Repeat("c", 40), RevisionID: "approved"}})
		}
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	review, err := client.PreviewEnvironmentGitRevision(ctx, "my project", "production", preview.PreviewEnvironmentGitRevisionRequest{CommitSHA: sha})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ApproveEnvironmentGitRevision(ctx, "my project", "production", preview.ApproveEnvironmentGitRevisionRequest{CommitSHA: review.CommitSHA, DefinitionDigest: review.DefinitionDigest, ExpectedGeneration: review.Generation}); err != nil {
		t.Fatal(err)
	}
	if err := client.RemoveEnvironmentGitOpsOverride(ctx, "my project", "production", preview.RemoveEnvironmentGitOpsOverrideRequest{Resource: "workload/api", Path: "variables/MODE"}); err != nil {
		t.Fatal(err)
	}
	status, err := client.GetEnvironmentGitOps(ctx, "my project", "production")
	if err != nil || status.Source.SourceCommitSHA != sha || status.Source.SourceDefinitionDigest != digest ||
		status.WorkloadEvidence == nil || status.WorkloadEvidence.CommitSHA != strings.Repeat("c", 40) ||
		status.Source.SourceVerifiedAt == nil || !status.Source.SourceVerifiedAt.Equal(verified) ||
		status.Source.SourceErrorCode != "environment_git_source_unavailable" || status.Source.ApprovedRevisionID != "approved" {
		t.Fatalf("source availability/authority: %+v %v", status, err)
	}
	if requests != 4 {
		t.Fatalf("requests: %d", requests)
	}
}

func TestGitLabProtectedSourceUsesOAuthRepositoryIdentity(t *testing.T) {
	spec := preview.EnvironmentGitSourceSpec{Provider: "gitlab", RepositoryID: 41, Repository: "team/subgroup/shop", Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: "report", ApprovalPolicy: "protected_branch"}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	spec.InstallationID = 42
	if err := spec.Validate(); err == nil {
		t.Fatal("GitLab accepted a GitHub installation")
	}
}
