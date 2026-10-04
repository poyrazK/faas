package githubd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gitapproval"
	"github.com/onebox-faas/faas/pkg/state"
)

type mergeProviderFixture struct {
	pr           gitMergedPR
	associated   []gitMergedPR
	reviews      []gitReview
	permission   string
	permissionID int64
	status       int
	changePolicy bool
	changeHead   bool
}

func TestHTTPReviewedMergeEvidence(t *testing.T) {
	sha, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name, reason string
		unavailable  bool
		mutate       func(*mergeProviderFixture)
	}{
		{name: "qualified"},
		{name: "second page approval", mutate: func(f *mergeProviderFixture) {
			approval := f.reviews[0]
			f.reviews = nil
			for i := 0; i < 100; i++ {
				r := approval
				r.ID = int64(100 + i)
				r.State = "COMMENTED"
				f.reviews = append(f.reviews, r)
			}
			f.reviews = append(f.reviews, approval)
		}},
		{name: "review history bound", reason: "review_history_too_large", mutate: func(f *mergeProviderFixture) {
			base := f.reviews[0]
			f.reviews = nil
			for i := 0; i < 1001; i++ {
				r := base
				r.ID = int64(100 + i)
				r.State = "COMMENTED"
				f.reviews = append(f.reviews, r)
			}
		}},
		{name: "associated history bound", reason: "pull_request_history_too_large", mutate: func(f *mergeProviderFixture) {
			for len(f.associated) < 100 {
				f.associated = append(f.associated, f.pr)
			}
		}},
		{name: "direct push", reason: "merged_pull_request_unavailable", mutate: func(f *mergeProviderFixture) { f.associated = []gitMergedPR{} }},
		{name: "ambiguous PR", reason: "merged_pull_request_ambiguous", mutate: func(f *mergeProviderFixture) { f.associated = append(f.associated, f.pr) }},
		{name: "wrong target", reason: "merged_pull_request_unavailable", mutate: func(f *mergeProviderFixture) { f.pr.Base.Ref = "staging"; f.associated = []gitMergedPR{f.pr} }},
		{name: "wrong merge SHA", reason: "merged_pull_request_unavailable", mutate: func(f *mergeProviderFixture) { f.pr.MergeSHA = head }},
		{name: "fork head unavailable", reason: "review_head_repository_unavailable", mutate: func(f *mergeProviderFixture) { f.pr.Head.Repo.ID = 999 }},
		{name: "unmerged", reason: "merged_pull_request_unavailable", mutate: func(f *mergeProviderFixture) { f.pr.Merged = false }},
		{name: "old head", reason: "review_approvals_insufficient", mutate: func(f *mergeProviderFixture) { f.reviews[0].CommitSHA = sha }},
		{name: "after merge", reason: "review_approvals_insufficient", mutate: func(f *mergeProviderFixture) { f.reviews[0].SubmittedAt = f.pr.MergedAt.Add(time.Second) }},
		{name: "same second", reason: "review_approvals_insufficient", mutate: func(f *mergeProviderFixture) { f.reviews[0].SubmittedAt = f.pr.MergedAt }},
		{name: "author", reason: "review_approvals_insufficient", mutate: func(f *mergeProviderFixture) { f.reviews[0].User.ID = f.pr.User.ID }},
		{name: "bot", reason: "review_approvals_insufficient", mutate: func(f *mergeProviderFixture) { f.reviews[0].User.Type = "Bot" }},
		{name: "read permission", reason: "review_approvals_insufficient", mutate: func(f *mergeProviderFixture) { f.permission = "read" }},
		{name: "permission identity", reason: "review_approvals_insufficient", mutate: func(f *mergeProviderFixture) { f.permissionID = 999 }},
		{name: "dismissed", reason: "review_approvals_insufficient", mutate: func(f *mergeProviderFixture) { f.reviews[0].State = "DISMISSED" }},
		{name: "requested changes", reason: "review_changes_requested", mutate: func(f *mergeProviderFixture) {
			r := f.reviews[0]
			r.ID++
			r.State = "CHANGES_REQUESTED"
			r.SubmittedAt = r.SubmittedAt.Add(time.Second)
			f.reviews = append(f.reviews, r)
		}},
		{name: "comments preserve approval", mutate: func(f *mergeProviderFixture) {
			r := f.reviews[0]
			r.ID++
			r.State = "COMMENTED"
			r.SubmittedAt = r.SubmittedAt.Add(time.Second)
			f.reviews = append(f.reviews, r)
		}},
		{name: "later approval supersedes changes", mutate: func(f *mergeProviderFixture) {
			r := f.reviews[0]
			r.ID--
			r.State = "CHANGES_REQUESTED"
			r.SubmittedAt = r.SubmittedAt.Add(-time.Second)
			f.reviews = append([]gitReview{r}, f.reviews...)
		}},
		{name: "duplicate review IDs", reason: "review_history_incomplete", mutate: func(f *mergeProviderFixture) { f.reviews = append(f.reviews, f.reviews[0]) }},
		{name: "malformed review", reason: "review_history_incomplete", mutate: func(f *mergeProviderFixture) { f.reviews[0].SubmittedAt = time.Time{} }},
		{name: "unknown review state", reason: "review_history_incomplete", mutate: func(f *mergeProviderFixture) { f.reviews[0].State = "UNKNOWN" }},
		{name: "permission outage", unavailable: true, mutate: func(f *mergeProviderFixture) { f.status = http.StatusServiceUnavailable }},
		{name: "policy changes during history", reason: "branch_protection_changed", mutate: func(f *mergeProviderFixture) { f.changePolicy = true }},
		{name: "head changes during history", reason: "branch_protection_changed", mutate: func(f *mergeProviderFixture) { f.changeHead = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pr := gitMergedPR{ID: 1234, Number: 7, Merged: true, State: "closed", MergeSHA: sha, MergedAt: now.Add(-time.Hour), User: gitReviewUser{ID: 10, Login: "author", Type: "User"}}
			pr.Base.Ref = "main"
			pr.Base.Repo.ID = 123
			pr.Head.SHA = head
			pr.Head.Repo.ID = 123
			f := mergeProviderFixture{pr: pr, associated: []gitMergedPR{pr}, permission: "write", permissionID: 11,
				reviews: []gitReview{{ID: 2, State: "APPROVED", CommitSHA: head, SubmittedAt: now.Add(-2 * time.Hour), User: gitReviewUser{ID: 11, Login: "reviewer", Type: "User"}}}}
			if tc.mutate != nil {
				tc.mutate(&f)
			}
			protections, heads := 0, 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer private-credential" {
					t.Error("missing installation authentication")
				}
				enc := json.NewEncoder(w)
				switch r.URL.Path {
				case "/repos/octo/api":
					_ = enc.Encode(map[string]any{"id": 123, "full_name": "octo/api"})
				case "/repos/octo/api/branches/main":
					heads++
					s := sha
					if f.changeHead && heads > 2 {
						s = head
					}
					_ = enc.Encode(map[string]any{"name": "main", "protected": true, "commit": map[string]any{"sha": s}})
				case "/repos/octo/api/branches/main/protection":
					protections++
					p := reviewedBranchPolicy
					if f.changePolicy && protections > 2 {
						p = strings.Replace(p, "review_count\":1", "review_count\":2", 1)
					}
					_, _ = w.Write([]byte(p))
				case "/repos/octo/api/commits/" + sha + "/pulls":
					_ = enc.Encode(f.associated)
				case "/repos/octo/api/pulls/7":
					_ = enc.Encode(f.pr)
				case "/repos/octo/api/pulls/7/reviews":
					start := 0
					if r.URL.Query().Get("page") != "" {
						page, _ := strconv.Atoi(r.URL.Query().Get("page"))
						start = (page - 1) * 100
					}
					end := start + 100
					if end > len(f.reviews) {
						end = len(f.reviews)
					}
					if start > len(f.reviews) {
						start = len(f.reviews)
					}
					_ = enc.Encode(f.reviews[start:end])
				case "/repos/octo/api/collaborators/reviewer/permission":
					if f.status != 0 {
						w.WriteHeader(f.status)
						_, _ = w.Write([]byte("private-credential"))
						return
					}
					_ = enc.Encode(map[string]any{"permission": f.permission, "user": gitReviewUser{ID: f.permissionID, Login: "reviewer", Type: "User"}})
				default:
					t.Errorf("unexpected provider path: %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer provider.Close()
			tokens := NewTokenCache(fakeFetcher(func(context.Context, int64) (string, time.Time, error) {
				return "private-credential", time.Now().Add(time.Hour), nil
			}), time.Minute)
			client := NewHTTPReviewedMerges(tokens, &singleHostClient{base: provider.Client(), api: provider.URL})
			evidence, err := client.ReviewedMergeEvidence(t.Context(), 42, 123, "octo/api", "main", sha)
			if tc.unavailable {
				if !errors.Is(err, ErrProtectedBranchEvidenceUnavailable) || evidence.Qualified || strings.Contains(err.Error(), "private-credential") {
					t.Fatalf("unsafe outage: %+v %v", evidence, err)
				}
				return
			}
			if err != nil || evidence.Reason != tc.reason || evidence.Qualified != (tc.reason == "") {
				t.Fatalf("evidence: %+v %v want=%s", evidence, err, tc.reason)
			}
			if tc.reason == "" && !evidence.ValidFor(42, 123, "octo/api", "main", sha, time.Now(), api.EnvironmentGitProtectedBranchEvidenceMaxAge) {
				t.Fatal("provider produced invalid receipt")
			}
		})
	}
}

type reviewedMergeRecorder struct{ calls int }

func (p *reviewedMergeRecorder) ReviewedMergeEvidence(context.Context, int64, int64, string, string, string) (gitapproval.MergeEvidence, error) {
	p.calls++
	return gitapproval.MergeEvidence{Reason: "merged_pull_request_unavailable"}, nil
}
func TestReviewedMergeEvidenceServiceRequiresAccountInstallation(t *testing.T) {
	installs := newMemStoreInstalls()
	if err := installs.Upsert(t.Context(), state.GitHubInstall{AccountID: "owner", InstallationID: 42, AuditGithubLogin: "owner"}); err != nil {
		t.Fatal(err)
	}
	provider := &reviewedMergeRecorder{}
	service := &RealService{Installs: installs, ReviewedMerges: provider}
	if _, err := service.GetReviewedMergeEvidence(t.Context(), "neighbor", 42, 123, "octo/api", "main", strings.Repeat("a", 40)); err == nil || provider.calls != 0 {
		t.Fatalf("foreign account reached provider: %v", err)
	}
	if _, err := service.GetReviewedMergeEvidence(t.Context(), "owner", 42, 123, "octo/api", "main", strings.Repeat("a", 40)); err != nil || provider.calls != 1 {
		t.Fatalf("owner evidence: %v calls=%d", err, provider.calls)
	}
}
