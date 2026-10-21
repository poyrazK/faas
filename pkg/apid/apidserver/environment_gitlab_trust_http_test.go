package apidserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gitlabsource"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGitLabGitOpsHTTPReviewAdoptionAndPinnedBuildArchive(t *testing.T) {
	t.Setenv(apidserver.SourceSpoolRootEnv, t.TempDir())
	t.Setenv(apidserver.ScanSpoolRootEnv, t.TempDir())
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	srv, store, account, _, _ := apidserver.NewProjectLifecycleFixture(t)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	previousRecipient, previousIdentities := *apidserver.SetSecretRecipient, *apidserver.HostIdentitiesForUnseal
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	*apidserver.HostIdentitiesForUnseal = func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() {
		*apidserver.SetSecretRecipient = previousRecipient
		*apidserver.HostIdentitiesForUnseal = previousIdentities
	})
	expires := time.Now().Add(time.Hour)
	sealed, err := apidserver.SealGitLabTokensForTest(gitlabsource.Tokens{Access: "private-access", Refresh: "private-refresh", Expires: expires})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutGitLabOAuthConnection(t.Context(), state.GitLabOAuthConnection{AccountID: account.ID, GitLabUserID: 41, Username: "reviewer", CredentialsSealed: sealed, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	definition := "api_version: gregale.dev/environment/v1\nproject: shop\nenvironment: production\nworkloads:\n  api:\n    app: shop-api\n    source:\n      kind: dockerfile\n      directory: apps/api\n      dockerfile: deploy/Dockerfile\n    runtime:\n      port: 8080\n"
	archive := apidserver.GitOpsBuildArchiveForTest(t, definition)
	var mismatch atomic.Bool
	substituted := apidserver.GitOpsBuildArchiveForTest(t, strings.ReplaceAll(definition, "port: 8080", "port: 9090"))
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer private-access" {
			t.Error("invalid GitLab read")
		}
		switch r.URL.Path {
		case "/projects/team/subgroup/shop":
			w.Write([]byte(`{"id":41,"path_with_namespace":"team/subgroup/shop"}`))
		case "/projects/41/repository/branches/main":
			w.Write([]byte(`{"commit":{"id":"` + sha + `"}}`))
		case "/projects/41/repository/commits/" + sha:
			w.Write([]byte(`{"id":"` + sha + `"}`))
		case "/projects/41/repository/archive.tar.gz":
			if r.URL.Query().Get("sha") != sha || r.URL.Query().Get("include_lfs_blobs") != "false" {
				t.Error("unpinned archive request")
			}
			if mismatch.Load() {
				w.Write(substituted)
			} else {
				w.Write(archive)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	srv.GitLabClientForTest(gitlabsource.Client{APIURL: provider.URL, HTTP: provider.Client()})
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), account.ID, hash, "GitLab HTTP test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(srv.Handler())
	defer server.Close()
	client := api.NewClient(server.URL, key)
	source, err := client.CreateEnvironmentGitSource(t.Context(), "shop", "production", api.CreateEnvironmentGitSourceRequest{Provider: "gitlab", Repository: "team/subgroup/shop", Ref: "refs/heads/main", ManifestPath: "environments/production.yaml", Mode: "report", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	review, err := client.PreviewEnvironmentGitRevision(t.Context(), "shop", "production", api.PreviewEnvironmentGitRevisionRequest{CommitSHA: sha})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := client.ApproveEnvironmentGitRevision(t.Context(), "shop", "production", api.ApproveEnvironmentGitRevisionRequest{CommitSHA: sha, DefinitionDigest: review.DefinitionDigest, ExpectedGeneration: review.Generation})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := client.PreviewEnvironmentGitOpsAdoption(t.Context(), "shop", "production")
	if err != nil || !plan.CanApply() {
		t.Fatalf("adoption review: %+v %v", plan, err)
	}
	if _, err := client.AdoptEnvironmentGitOps(t.Context(), "shop", "production", api.AdoptEnvironmentGitOpsRequest{PlanHash: plan.Hash}); err != nil {
		t.Fatal(err)
	}
	lease := state.EnvironmentGitOpsLease{Source: approved.Source, Revision: approved.Revision}
	buildID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	request := state.EnvironmentWorkloadSourceRequest{BuildID: buildID.String(), RevisionID: approved.Revision.ID, CommitSHA: sha, DefinitionDigest: review.DefinitionDigest, Source: api.EnvironmentWorkloadSource{Kind: "dockerfile", Directory: "apps/api", Dockerfile: "deploy/Dockerfile"}}
	mismatch.Store(true)
	if _, err := srv.PrepareGitOpsSourceForTest(t.Context(), lease, request); err == nil {
		t.Fatal("substituted definition entered build spool")
	}
	mismatch.Store(false)
	artifact, err := srv.PrepareGitOpsSourceForTest(t.Context(), lease, request)
	if err != nil || artifact.CommitSHA != sha || artifact.DefinitionDigest != review.DefinitionDigest || artifact.Bytes != int64(len(archive)) || len(artifact.SHA256) != 64 {
		t.Fatalf("pinned GitLab artifact: %+v %v", artifact, err)
	}
	protected := approved.Source
	protected.Spec.ApprovalPolicy = "protected_branch"
	checked := time.Now().UTC()
	protected.SourceVerifiedAt, protected.SourceCommitSHA, protected.SourceDefinitionDigest = &checked, sha, review.DefinitionDigest
	poll, err := srv.ReadGitOpsSourceForTest(t.Context(), protected)
	if err != nil || poll.ErrorCode != "environment_git_approval_unavailable" || poll.Approval != nil {
		t.Fatalf("unchanged SHA bypassed GitLab approval verification: %+v %v", poll, err)
	}
	if err := store.DeleteGitLabOAuthConnection(t.Context(), account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.PrepareGitOpsSourceForTest(t.Context(), lease, request); err == nil {
		t.Fatal("disconnected account read build source")
	}
	status, err := client.GetEnvironmentGitOps(t.Context(), "shop", "production")
	if err != nil || status.Source.ID != source.ID || status.Source.ApprovedRevisionID != approved.Revision.ID || status.Source.AppliedRevisionID != "" {
		t.Fatalf("source disconnect lost approval or claimed execution: %+v %v", status, err)
	}
}
