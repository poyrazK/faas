// adr: 20261027090010
package githubdgrpc_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

type commentSvc struct {
	githubdgrpc.UnimplementedService
	got []any
	err error
}

func (s *commentSvc) UpsertIssueComment(_ context.Context, account string, installation int64, repository string, number int64, key, body string) (githubdgrpc.IssueComment, error) {
	s.got = []any{account, installation, repository, number, key, body}
	if s.err != nil {
		return githubdgrpc.IssueComment{}, s.err
	}
	return githubdgrpc.IssueComment{ID: 99, URL: "https://github.com/acme/app/pull/7#issuecomment-99", Created: true}, nil
}

func commentClient(t *testing.T, svc githubdgrpc.Service) *githubdgrpc.Client {
	t.Helper()
	srv := grpc.NewServer()
	githubdgrpc.New(svc, wire.NewOpsMetrics("githubd_comment"), nil).Register(srv)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return githubdgrpc.NewClient(conn)
}

// The comment RPC carries its scope, and its status codes survive the wire,
// because agentd tells a refusal from a lost write by them.
func TestUpsertIssueCommentRoundTrip(t *testing.T) {
	svc := &commentSvc{}
	client := commentClient(t, svc)
	c, err := client.UpsertIssueComment(t.Context(), "owner", 42, "acme/app", 7, "agent-abc", "hi")
	if err != nil || c.ID != 99 || !c.Created || c.URL == "" {
		t.Fatalf("%+v %v", c, err)
	}
	if want := []any{"owner", int64(42), "acme/app", int64(7), "agent-abc", "hi"}; len(svc.got) != len(want) || svc.got[0] != want[0] ||
		svc.got[1] != want[1] || svc.got[2] != want[2] || svc.got[3] != want[3] || svc.got[4] != want[4] || svc.got[5] != want[5] {
		t.Fatalf("request %v", svc.got)
	}
	for _, code := range []codes.Code{codes.NotFound, codes.FailedPrecondition, codes.Unavailable, codes.Unknown, codes.InvalidArgument} {
		svc.err = status.Error(code, "x")
		if _, err := client.UpsertIssueComment(t.Context(), "owner", 42, "acme/app", 7, "agent-abc", "hi"); status.Code(err) != code {
			t.Errorf("%s became %v", code, err)
		}
	}
	if _, err := commentClient(t, githubdgrpc.UnimplementedService{}).UpsertIssueComment(t.Context(), "owner", 42, "acme/app", 7, "agent-abc", "hi"); status.Code(err) != codes.Unimplemented {
		t.Fatalf("a githubd without comments: %v", err)
	}
}
