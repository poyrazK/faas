package githubdgrpc_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/ci"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *streamSvc) GetCIChangedFiles(_ context.Context, account string, installation int64, repository, base, head string) (ci.ChangedFiles, error) {
	if s.ciChangesInput != nil {
		s.ciChangesInput <- &githubdpb.GetCIChangedFilesRequest{AccountId: account, InstallationId: installation, RepoFullName: repository, BaseSha: base, HeadSha: head}
	}
	return s.ciChanges, s.ciChangesErr
}

func TestCIChangedFilesRoundTripAndIncompleteEvidence(t *testing.T) {
	for _, mode := range []string{"valid", "incomplete", "wrong_pin", "missing_rename"} {
		t.Run(mode, func(t *testing.T) {
			base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
			changes := ci.ChangedFiles{BaseSHA: base, HeadSHA: head, MergeBaseSHA: base, Complete: true, Files: []ci.ChangedFile{{Path: "docs/readme", PreviousPath: "src/readme", Status: "renamed"}}}
			switch mode {
			case "incomplete":
				changes.Complete = false
			case "wrong_pin":
				changes.HeadSHA = base
			case "missing_rename":
				changes.Files[0].PreviousPath = ""
			}
			input := make(chan *githubdpb.GetCIChangedFilesRequest, 1)
			_, client := newStreamServer(t, &streamSvc{ciChanges: changes, ciChangesInput: input})
			got, err := client.GetCIChangedFiles(t.Context(), "owner", 77, "octo/api", base, head)
			req := <-input
			if req.GetAccountId() != "owner" || req.GetInstallationId() != 77 || req.GetRepoFullName() != "octo/api" || req.GetBaseSha() != base || req.GetHeadSha() != head {
				t.Fatal("comparison RPC lost scope or immutable pins")
			}
			if mode == "valid" {
				if err != nil || !reflect.DeepEqual(got, changes) {
					t.Fatalf("wire comparison changed: %+v %v", got, err)
				}
			} else if err == nil {
				t.Fatal("incomplete wire evidence became path authority")
			}
		})
	}
	if _, err := newServer(t).GetCIChangedFiles(t.Context(), &githubdpb.GetCIChangedFilesRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatal("older service did not fail closed")
	}
}
