package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gitapproval"
	"github.com/onebox-faas/faas/pkg/state"
)

type environmentGitSourcePollingCall struct {
	AccountID      string
	InstallationID int64
	Repository     string
	Ref            string
	Limit          int64
}

type environmentGitSourcePollingClient struct {
	stubGithubdClient
	evidence      gitapproval.MergeEvidence
	evidenceErr   error
	evidenceCalls chan environmentGitSourcePollingCall
	repositories  []Repo
	headArchive   []byte
	archive       []byte
	stats         StreamSourceRefStats
	err           error
	calls         chan environmentGitSourcePollingCall
}

func (c *environmentGitSourcePollingClient) ListInstallableRepos(context.Context, string, int64) ([]Repo, error) {
	return c.repositories, nil
}

func (c *environmentGitSourcePollingClient) StreamSourceRef(_ context.Context, accountID string, installID int64, repo, ref string, limit int64) (*StreamSourceRefResult, error) {
	if c.calls != nil {
		c.calls <- environmentGitSourcePollingCall{accountID, installID, repo, ref, limit}
	}
	if c.err != nil {
		return nil, c.err
	}
	stats := c.stats
	if len(ref) == 40 {
		stats.ResolvedCommitSHA = ref
	}
	archive := c.archive
	if ref == c.evidence.HeadSHA && c.headArchive != nil {
		archive = c.headArchive
	}
	return &StreamSourceRefResult{Body: io.NopCloser(bytes.NewReader(archive)), Stats: &stats}, nil
}

func environmentGitSourcePollingFixture(t *testing.T) (*server, *state.MemStore, state.EnvironmentGitSource, *environmentGitSourcePollingClient) {
	return environmentGitSourcePollingFixturePolicy(t, "manual")
}

func environmentGitSourcePollingFixturePolicy(t *testing.T, policy string) (*server, *state.MemStore, state.EnvironmentGitSource, *environmentGitSourcePollingClient) {
	t.Helper()
	srv, store, account, project, _ := newProjectLifecycleFixture(t)
	if _, err := store.UpdateProjectBinding(t.Context(), account.ID, project.ID, "example/shop", "main", 42); err != nil {
		t.Fatal(err)
	}
	client := &environmentGitSourcePollingClient{repositories: []Repo{{ID: 123, FullName: "example/shop"}},
		archive: environmentGitOpsArchive(t), stats: StreamSourceRefStats{ResolvedCommitSHA: strings.Repeat("a", 40)}}
	srv.githubd = client
	rec := gitOpsHandlerRequest(t, srv, account, http.MethodPost, "source", map[string]string{"manifest_path": "environments/production.yaml", "approval_policy": policy}, srv.createEnvironmentGitSource)
	if rec.Code != http.StatusCreated {
		t.Fatalf("source binding: %d %s", rec.Code, rec.Body.String())
	}
	source, err := store.EnvironmentGitSource(t.Context(), account.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	return srv, store, source, client
}

func TestEnvironmentGitSourcePollingReaderVerifiesIdentityAndCompleteStream(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*state.EnvironmentGitSource, *environmentGitSourcePollingClient)
		code   string
	}{
		{name: "branch"},
		{name: "tag", modify: func(source *state.EnvironmentGitSource, _ *environmentGitSourcePollingClient) {
			source.Spec.Ref = "refs/tags/release-v1"
		}},
		{name: "repository identity changed", code: "environment_git_repository_unavailable", modify: func(_ *state.EnvironmentGitSource, c *environmentGitSourcePollingClient) { c.repositories[0].ID = 999 }},
		{name: "invalid manifest", code: "environment_git_definition_invalid", modify: func(_ *state.EnvironmentGitSource, c *environmentGitSourcePollingClient) {
			c.archive = []byte("invalid archive")
		}},
		{name: "truncated stream", code: "environment_git_source_unavailable", modify: func(_ *state.EnvironmentGitSource, c *environmentGitSourcePollingClient) { c.stats.Truncated = true }},
		{name: "stream failure", code: "environment_git_source_unavailable", modify: func(_ *state.EnvironmentGitSource, c *environmentGitSourcePollingClient) {
			c.stats.Err = errors.New("transport credential")
		}},
		{name: "unresolved commit", code: "environment_git_source_unavailable", modify: func(_ *state.EnvironmentGitSource, c *environmentGitSourcePollingClient) {
			c.stats.ResolvedCommitSHA = "main"
		}},
		{name: "transport unavailable", code: "environment_git_source_unavailable", modify: func(_ *state.EnvironmentGitSource, c *environmentGitSourcePollingClient) {
			c.err = errors.New("transport credential")
		}},
		{name: "wrong environment", code: "environment_git_scope_mismatch", modify: func(source *state.EnvironmentGitSource, _ *environmentGitSourcePollingClient) {
			source.EnvironmentSlug = "staging"
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, source, client := environmentGitSourcePollingFixture(t)
			client.calls = make(chan environmentGitSourcePollingCall, 1)
			if tc.modify != nil {
				tc.modify(&source, client)
			}
			result, err := (&environmentGitSourceReader{server: srv}).ReadEnvironmentGitSource(t.Context(), source)
			if err != nil || result.ErrorCode != tc.code {
				t.Fatalf("source observation: %+v %v", result, err)
			}
			if tc.code != "" {
				if result.CommitSHA != "" || result.Digest != "" {
					t.Fatalf("unverified stream retained candidate: %+v", result)
				}
				return
			}
			call := <-client.calls
			if result.CommitSHA != client.stats.ResolvedCommitSHA || len(result.Digest) != 64 || call.AccountID != source.AccountID ||
				call.InstallationID != source.Spec.InstallationID || call.Repository != source.Spec.Repository || call.Ref != source.Spec.Ref || call.Limit <= 0 {
				t.Fatalf("candidate was not read through scoped immutable stream: %+v %+v", result, call)
			}
		})
	}
}

func TestEnvironmentGitSourcePollingStartsFromApidWithoutGrantingApproval(t *testing.T) {
	srv, store, source, client := environmentGitSourcePollingFixture(t)
	client.calls = make(chan environmentGitSourcePollingCall, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	srv.startEnvironmentGitSourcePolling(ctx, func(key string) string {
		if key == "FAAS_ENVIRONMENT_GIT_SOURCE_POLLING_ENABLED" {
			return "false"
		}
		return ""
	})
	if srv.environmentGitSourcePollingEnabled.Load() {
		t.Fatal("explicitly disabled polling was advertised as enabled")
	}
	select {
	case <-client.calls:
		t.Fatal("disabled source polling fetched Git")
	case <-time.After(20 * time.Millisecond):
	}
	srv.startEnvironmentGitSourcePolling(ctx, func(string) string { return "" })
	if !srv.environmentGitSourcePollingEnabled.Load() {
		t.Fatal("configured polling was advertised as disabled")
	}
	select {
	case <-client.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("apid did not start candidate discovery")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		observed, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
		if err != nil {
			t.Fatal(err)
		}
		if observed.SourceCheckedAt != nil {
			if observed.SourceErrorCode != "" || observed.SourceVerifiedAt == nil || observed.SourceCommitSHA != client.stats.ResolvedCommitSHA ||
				observed.ApprovedRevisionID != "" || observed.Generation != source.Generation || observed.IntentVersion != source.IntentVersion {
				t.Fatalf("startup discovery mutated authority: %+v", observed)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("candidate was fetched but not persisted")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEnvironmentGitSourcePollingDashboardSeparatesOutageFromApproval(t *testing.T) {
	srv, store, source, client := environmentGitSourcePollingFixture(t)
	now := time.Now().UTC()
	lease, err := store.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	reader := &environmentGitSourceReader{server: srv}
	result, err := reader.ReadEnvironmentGitSource(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishEnvironmentGitSourcePoll(t.Context(), lease, result, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	failedAt := now.Add(2 * time.Minute)
	lease, err = store.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), failedAt, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishEnvironmentGitSourcePoll(t.Context(), lease, state.EnvironmentGitSourcePollResult{
		ErrorCode: "environment_git_source_unavailable"}, failedAt, failedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	account, err := store.AccountByID(t.Context(), source.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: sessionCookie, Value: issueDashboardTestCookie(t, store, srv.sessions, account.ID)}
	page := dashboardGet(srv.handler(), "/dashboard/projects/shop/environments/production/gitops", cookie)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "The last approved definition remains authoritative") ||
		!strings.Contains(page.Body.String(), "Last verified candidate") || !strings.Contains(page.Body.String(), client.stats.ResolvedCommitSHA) ||
		!strings.Contains(page.Body.String(), "No approved revision") || strings.Contains(page.Body.String(), "environment_git_source_unavailable") {
		t.Fatalf("dashboard conflated source discovery with authority: %d %s", page.Code, page.Body.String())
	}
	// The API/CLI status carries the stable code while retaining candidate evidence.
	status := gitOpsHandlerRequest(t, srv, account, http.MethodGet, "", nil, srv.getEnvironmentGitOps)
	var response api.EnvironmentGitOpsStatusResponse
	if err := json.Unmarshal(status.Body.Bytes(), &response); err != nil || status.Code != http.StatusOK ||
		response.Source.SourceVerifiedAt == nil || response.Source.SourceCommitSHA != client.stats.ResolvedCommitSHA ||
		response.Source.SourceErrorCode != "environment_git_source_unavailable" || response.Source.ApprovedRevisionID != "" {
		t.Fatalf("status omitted source availability: %d %s", status.Code, status.Body.String())
	}
}

func (c *environmentGitSourcePollingClient) GetReviewedMergeEvidence(_ context.Context, accountID string, installID, repoID int64, repo, branch, sha string) (gitapproval.MergeEvidence, error) {
	if c.evidenceCalls != nil {
		c.evidenceCalls <- environmentGitSourcePollingCall{AccountID: accountID, InstallationID: installID, Repository: repo, Ref: branch, Limit: repoID}
	}
	return c.evidence, c.evidenceErr
}

func apidReviewedMergeFixture(source state.EnvironmentGitSource) gitapproval.MergeEvidence {
	now := time.Now().UTC()
	head := strings.Repeat("b", 40)
	return gitapproval.MergeEvidence{Qualified: true, Profile: gitapproval.ReviewedMergeProfile, Policy: gitapproval.PolicyEvidence{
		Qualified: true, Profile: gitapproval.ProtectedBranchProfile, InstallationID: 42, RepositoryID: 123, Repository: "example/shop", Branch: "main",
		CommitSHA: strings.Repeat("a", 40), PolicyDigest: strings.Repeat("c", 64), RequiredReviewCount: 1, CheckedAt: now},
		PullRequestID: 1234, PullRequestNumber: 7, AuthorID: 10, HeadSHA: head, MergedAt: now.Add(-time.Hour), CheckedAt: now,
		Reviews: []gitapproval.ReviewEvidence{{ID: 1, ReviewerID: 11, Reviewer: "reviewer", HeadSHA: head, SubmittedAt: now.Add(-2 * time.Hour)}}}
}

func TestEnvironmentGitSourcePollingStartupApprovesReviewedMergeAndExposesProof(t *testing.T) {
	srv, store, source, client := environmentGitSourcePollingFixturePolicy(t, "protected_branch")
	client.evidence = apidReviewedMergeFixture(source)
	client.evidenceCalls = make(chan environmentGitSourcePollingCall, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	srv.startEnvironmentGitSourcePolling(ctx, func(string) string { return "" })
	select {
	case call := <-client.evidenceCalls:
		if call.AccountID != source.AccountID || call.InstallationID != 42 || call.Repository != "example/shop" || call.Ref != "main" || call.Limit != 123 {
			t.Fatalf("review scope: %+v", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("startup did not verify reviewed merge")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
		if err != nil {
			t.Fatal(err)
		}
		if current.ApprovedRevisionID != "" {
			account, err := store.AccountByID(t.Context(), source.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			rec := gitOpsHandlerRequest(t, srv, account, http.MethodGet, "", nil, srv.getEnvironmentGitOps)
			var status api.EnvironmentGitOpsStatusResponse
			if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &status) != nil || status.Approval == nil || status.Approval.RevisionID != current.ApprovedRevisionID || status.Approval.DefinitionDigest != current.SourceDefinitionDigest || status.Approval.Evidence.PullRequestID != 1234 {
				t.Fatalf("proof status: %d %s", rec.Code, rec.Body.String())
			}
			job, err := store.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), time.Now(), time.Minute)
			if err != nil || job.Revision.ID != current.ApprovedRevisionID {
				t.Fatalf("reviewed merge not queued: %+v %v", job, err)
			}
			manual := gitOpsHandlerRequest(t, srv, account, http.MethodPost, "revisions/approve", api.ApproveEnvironmentGitRevisionRequest{CommitSHA: client.stats.ResolvedCommitSHA, DefinitionDigest: current.SourceDefinitionDigest, ExpectedGeneration: current.Generation}, srv.approveEnvironmentGitRevision)
			if manual.Code != http.StatusBadRequest {
				t.Fatalf("manual bypass: %d %s", manual.Code, manual.Body.String())
			}
			cookie := &http.Cookie{Name: sessionCookie, Value: issueDashboardTestCookie(t, store, srv.sessions, account.ID)}
			page := dashboardGet(srv.handler(), "/dashboard/projects/shop/environments/production/gitops", cookie)
			if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "PR #7") || !strings.Contains(page.Body.String(), "Qualified reviewed merges are approved automatically") {
				t.Fatalf("dashboard omitted approval provenance: %d %s", page.Code, page.Body.String())
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("reviewed merge not committed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEnvironmentGitSourcePollingProtectedEvidenceFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		modify     func(*environmentGitSourcePollingClient)
	}{
		{"unreviewed merge definition", "environment_git_approval_not_qualified", func(c *environmentGitSourcePollingClient) {
			c.headArchive = environmentGitOpsArchiveDefinition(t, "api_version: gregale.dev/environment/v1\nproject: shop\nenvironment: production\nworkloads:\n  api:\n    app: shop-api\n    variables:\n      MODE: reviewed\n")
		}},
		{"unavailable reviewed definition", "environment_git_approval_unavailable", func(c *environmentGitSourcePollingClient) { c.headArchive = []byte("invalid archive") }},
		{"unqualified", "environment_git_approval_not_qualified", func(c *environmentGitSourcePollingClient) { c.evidence.Qualified = false }},
		{"wrong SHA", "environment_git_approval_not_qualified", func(c *environmentGitSourcePollingClient) { c.evidence.Policy.CommitSHA = strings.Repeat("d", 40) }},
		{"older provider", "environment_git_approval_unavailable", func(c *environmentGitSourcePollingClient) { c.evidenceErr = errGithubdNotReady }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, store, source, client := environmentGitSourcePollingFixturePolicy(t, "protected_branch")
			client.evidence = apidReviewedMergeFixture(source)
			tc.modify(client)
			result, err := (&environmentGitSourceReader{server: srv}).ReadEnvironmentGitSource(t.Context(), source)
			if err != nil || result.ErrorCode != tc.code || result.Approval != nil || result.CommitSHA != "" {
				t.Fatalf("unsafe candidate: %+v %v", result, err)
			}
			if _, err := store.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), time.Now(), time.Minute); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("unqualified candidate became executable: %v", err)
			}
		})
	}
}
