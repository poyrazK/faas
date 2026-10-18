package githubdgrpc_test

import (
	"strings"
	"testing"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCIPullRequestRoundTripAndIncompleteIdentity(t *testing.T) {
	for _, mode := range []string{"open", "closed", "draft", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			pr := githubdgrpc.CIPullRequest{Number: 42, State: "open", HeadSHA: strings.Repeat("a", 40), BaseSHA: strings.Repeat("b", 40), BaseBranch: "main", HeadRepository: "octo/api", BaseRepository: "octo/api", HeadRepositoryID: 123, BaseRepositoryID: 123}
			if mode == "closed" {
				pr.State = "closed"
			}
			if mode == "draft" {
				pr.Draft = true
			}
			if mode == "incomplete" {
				pr.HeadRepositoryID = 0
			}
			input := make(chan *githubdpb.GetCIPullRequestRequest, 1)
			_, client := newStreamServer(t, &streamSvc{ciPullRequest: pr, ciPullRequestInput: input})
			got, err := client.GetCIPullRequest(t.Context(), "owner", 77, "octo/api", 42)
			req := <-input
			if req.GetAccountId() != "owner" || req.GetInstallationId() != 77 || req.GetRepoFullName() != "octo/api" || req.GetNumber() != 42 {
				t.Fatal("PR RPC lost account/installation/repository scope")
			}
			if mode == "incomplete" {
				if err == nil {
					t.Fatal("incomplete wire identity became PR authority")
				}
			} else if err != nil || got != pr {
				t.Fatalf("current PR wire response changed: %+v %v", got, err)
			}
		})
	}
	if _, err := newServer(t).GetCIPullRequest(t.Context(), &githubdpb.GetCIPullRequestRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatal("older RPC implementation did not fail closed")
	}
}
