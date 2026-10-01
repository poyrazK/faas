package githubdgrpc_test

import (
	"strings"
	"testing"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestProtectedBranchEvidenceRoundTripAndReceiptValidation(t *testing.T) {
	const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct {
		name     string
		mutate   func(*githubdgrpc.ProtectedBranchEvidence)
		wantErr  bool
		upstream error
	}{
		{name: "qualified"},
		{name: "not qualified", mutate: func(e *githubdgrpc.ProtectedBranchEvidence) {
			*e = githubdgrpc.ProtectedBranchEvidence{Reason: "branch_unprotected"}
		}},
		{name: "wrong repository", wantErr: true, mutate: func(e *githubdgrpc.ProtectedBranchEvidence) { e.RepositoryID++ }},
		{name: "wrong installation", wantErr: true, mutate: func(e *githubdgrpc.ProtectedBranchEvidence) { e.InstallationID++ }},
		{name: "wrong branch", wantErr: true, mutate: func(e *githubdgrpc.ProtectedBranchEvidence) { e.Branch = "staging" }},
		{name: "wrong commit", wantErr: true, mutate: func(e *githubdgrpc.ProtectedBranchEvidence) { e.CommitSHA = strings.Repeat("b", 40) }},
		{name: "missing digest", wantErr: true, mutate: func(e *githubdgrpc.ProtectedBranchEvidence) { e.PolicyDigest = "" }},
		{name: "unknown profile", wantErr: true, mutate: func(e *githubdgrpc.ProtectedBranchEvidence) { e.Profile = "other" }},
		{name: "stale evidence", wantErr: true, mutate: func(e *githubdgrpc.ProtectedBranchEvidence) {
			e.CheckedAt = time.Now().Add(-2 * api.EnvironmentGitProtectedBranchEvidenceMaxAge)
		}},
		{name: "future evidence", wantErr: true, mutate: func(e *githubdgrpc.ProtectedBranchEvidence) { e.CheckedAt = time.Now().Add(time.Hour) }},
		{name: "missing evidence", wantErr: true, mutate: func(e *githubdgrpc.ProtectedBranchEvidence) { e.CheckedAt = time.Time{} }},
		{name: "qualified with blocker", wantErr: true, mutate: func(e *githubdgrpc.ProtectedBranchEvidence) { e.Reason = "branch_unprotected" }},
		{name: "upstream unavailable", wantErr: true, upstream: api.ErrSourceRefUnavailable("Policy evidence is unavailable.")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evidence := githubdgrpc.ProtectedBranchEvidence{Qualified: true, Profile: githubdgrpc.ProtectedBranchProfile,
				InstallationID: 42, RepositoryID: 123, Repository: "octo/api", Branch: "main", CommitSHA: sha,
				PolicyDigest: strings.Repeat("c", 64), CheckedAt: time.Now().UTC()}
			if tc.mutate != nil {
				tc.mutate(&evidence)
			}
			request := make(chan *githubdpb.GetProtectedBranchEvidenceRequest, 1)
			_, client := newStreamServer(t, &streamSvc{protectedEvidence: evidence, protectedErr: tc.upstream, protectedRequest: request})
			got, err := client.GetProtectedBranchEvidence(t.Context(), "owner", 42, 123, "octo/api", "main", sha)
			select {
			case req := <-request:
				if req.GetAccountId() != "owner" || req.GetInstallationId() != 42 || req.GetRepositoryId() != 123 || req.GetRepoFullName() != "octo/api" || req.GetBranch() != "main" || req.GetCommitSha() != sha {
					t.Fatalf("wire scope changed: %+v", req)
				}
			default:
				t.Fatal("evidence provider was not called")
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("evidence: %+v err=%v", got, err)
			}
			if !tc.wantErr && got != evidence {
				t.Fatalf("receipt changed: %+v want %+v", got, evidence)
			}
			if tc.wantErr && got.Qualified {
				t.Fatal("rejected wire evidence retained qualification")
			}
		})
	}
}

func TestProtectedBranchEvidenceUnimplementedFailsClosed(t *testing.T) {
	_, err := newServer(t).GetProtectedBranchEvidence(t.Context(), &githubdpb.GetProtectedBranchEvidenceRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("older service response: %v", err)
	}
}

func TestProtectedBranchEvidenceRawRPCRejectsMissingReceipt(t *testing.T) {
	client, _ := newStreamServer(t, &streamSvc{protectedEvidence: githubdgrpc.ProtectedBranchEvidence{Qualified: true}})
	response, err := client.GetProtectedBranchEvidence(t.Context(), &githubdpb.GetProtectedBranchEvidenceRequest{AccountId: "owner",
		InstallationId: 42, RepositoryId: 123, RepoFullName: "octo/api", Branch: "main", CommitSha: strings.Repeat("a", 40)})
	if err == nil || response.GetQualified() {
		t.Fatalf("raw RPC accepted invalid receipt: %+v %v", response, err)
	}
}
