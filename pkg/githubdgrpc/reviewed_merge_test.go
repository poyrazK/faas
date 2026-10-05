package githubdgrpc_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gitapproval"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func reviewedWireFixture() gitapproval.MergeEvidence {
	now := time.Now().UTC()
	head := strings.Repeat("b", 40)
	return gitapproval.MergeEvidence{Qualified: true, Profile: gitapproval.ReviewedMergeProfile, Policy: gitapproval.PolicyEvidence{
		Qualified: true, Profile: gitapproval.ProtectedBranchProfile, InstallationID: 42, RepositoryID: 123, Repository: "octo/api", Branch: "main",
		CommitSHA: strings.Repeat("a", 40), PolicyDigest: strings.Repeat("c", 64), RequiredReviewCount: 1, CheckedAt: now},
		PullRequestID: 1234, PullRequestNumber: 7, AuthorID: 10, HeadSHA: head, MergedAt: now.Add(-time.Hour), CheckedAt: now,
		Reviews: []gitapproval.ReviewEvidence{{ID: 1, ReviewerID: 11, Reviewer: "reviewer", HeadSHA: head, SubmittedAt: now.Add(-2 * time.Hour)}}}
}

func TestReviewedMergeEvidenceRoundTripAndReceiptValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutate   func(*gitapproval.MergeEvidence)
		wantErr  bool
		upstream error
	}{
		{name: "qualified"},
		{name: "unqualified", mutate: func(e *gitapproval.MergeEvidence) {
			*e = gitapproval.MergeEvidence{Reason: "merged_pull_request_unavailable"}
		}},
		{name: "wrong SHA", wantErr: true, mutate: func(e *gitapproval.MergeEvidence) { e.Policy.CommitSHA = strings.Repeat("d", 40) }},
		{name: "stale policy", wantErr: true, mutate: func(e *gitapproval.MergeEvidence) { e.Policy.CheckedAt = time.Now().Add(-2 * time.Minute) }},
		{name: "author review", wantErr: true, mutate: func(e *gitapproval.MergeEvidence) { e.Reviews[0].ReviewerID = e.AuthorID }},
		{name: "outage", wantErr: true, upstream: api.ErrSourceRefUnavailable("Review provider unavailable.")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := reviewedWireFixture()
			if tc.mutate != nil {
				tc.mutate(&e)
			}
			reqs := make(chan *githubdpb.GetProtectedBranchEvidenceRequest, 1)
			_, client := newStreamServer(t, &streamSvc{mergeEvidence: e, mergeErr: tc.upstream, mergeRequest: reqs})
			got, err := client.GetReviewedMergeEvidence(t.Context(), "owner", 42, 123, "octo/api", "main", strings.Repeat("a", 40))
			req := <-reqs
			if req.GetAccountId() != "owner" || req.GetRepositoryId() != 123 || req.GetInstallationId() != 42 || req.GetBranch() != "main" || req.GetCommitSha() != strings.Repeat("a", 40) {
				t.Fatalf("scope changed: %+v", req)
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("receipt: %+v %v", got, err)
			}
			if !tc.wantErr && !reflect.DeepEqual(e, got) {
				t.Fatalf("evidence changed: %+v", got)
			}
			if tc.wantErr && got.Qualified {
				t.Fatal("rejected evidence retained authority")
			}
		})
	}
}

func TestReviewedMergeEvidenceOlderServiceFailsClosed(t *testing.T) {
	_, err := newServer(t).GetReviewedMergeEvidence(t.Context(), &githubdpb.GetProtectedBranchEvidenceRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("older service: %v", err)
	}
}
