// End-to-end handler tests via bufconn: an in-process githubd gRPC
// server backed by a fake Service. Mirrors pkg/scheddgrpc/bufconn_test.go.
// Slice 1: every RPC returns Unimplemented; we assert the round-trip
// is wired AND the error envelope is codes.Unimplemented. Slices 7+8
// add real coverage.

package githubdgrpc_test

import (
	"context"
	"net"
	"testing"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// recordingSvc captures every call so slice 7+ tests can assert on
// the arguments. Slice 1 always returns Unimplemented-equivalent
// errors to match githubdgrpc.UnimplementedService's shape — the
// fake's purpose here is to prove the round-trip wires correctly, not
// to provide business logic.
type recordingSvc struct {
	githubdgrpc.UnimplementedService

	getInstallStateCalls    int
	lastGetInstallAccountID string
}

// GetInstallState records the account_id and increments the counter.
// It still returns Unimplemented so slice 1's tests keep their shape;
// later slices swap in a real return value without touching the tests.
func (r *recordingSvc) GetInstallState(accountID string) (githubdgrpc.InstallState, string, string, error) {
	r.getInstallStateCalls++
	r.lastGetInstallAccountID = accountID
	return r.UnimplementedService.GetInstallState(accountID)
}

func newServer(t *testing.T) githubdpb.GithubdClient {
	t.Helper()
	srv := grpc.NewServer()
	githubdgrpc.New(&recordingSvc{}, wire.NewOpsMetrics("githubd_test"), nil).Register(srv)

	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return githubdpb.NewGithubdClient(conn)
}

func TestGetInstallState_Unimplemented(t *testing.T) {
	cli := newServer(t)
	_, err := cli.GetInstallState(context.Background(), &githubdpb.GetInstallStateRequest{AccountId: "acct-1"})
	if err == nil {
		t.Fatal("expected unimplemented error")
	}
	if code := status.Code(err); code != codes.Unimplemented {
		t.Errorf("code = %v, want Unimplemented", code)
	}
}

func TestExchangeOAuthCode_Unimplemented(t *testing.T) {
	cli := newServer(t)
	_, err := cli.ExchangeOAuthCode(context.Background(), &githubdpb.ExchangeOAuthCodeRequest{
		AccountId: "acct-1",
		Code:      "code-xyz",
		State:     "state-abc",
	})
	if err == nil {
		t.Fatal("expected unimplemented error")
	}
	if code := status.Code(err); code != codes.Unimplemented {
		t.Errorf("code = %v, want Unimplemented", code)
	}
}

func TestCreateDeploymentFromPush_Unimplemented(t *testing.T) {
	cli := newServer(t)
	_, err := cli.CreateDeploymentFromPush(context.Background(), &githubdpb.CreateDeploymentFromPushRequest{
		RepoFullName: "jane/api",
		Ref:          "refs/heads/main",
		CommitSha:    "deadbeef",
		Pusher:       "jane",
	})
	if err == nil {
		t.Fatal("expected unimplemented error")
	}
	if code := status.Code(err); code != codes.Unimplemented {
		t.Errorf("code = %v, want Unimplemented", code)
	}
}

func TestWriteCheck_Unimplemented(t *testing.T) {
	cli := newServer(t)
	_, err := cli.WriteCheck(context.Background(), &githubdpb.WriteCheckRequest{
		RepoFullName: "jane/api",
		CommitSha:    "deadbeef",
		Phase:        githubdpb.CheckPhase_QUEUED,
		LogsUrl:      "https://example.test/dashboard/apps/api/logs",
		Summary:      "Build queued",
	})
	if err == nil {
		t.Fatal("expected unimplemented error")
	}
	if code := status.Code(err); code != codes.Unimplemented {
		t.Errorf("code = %v, want Unimplemented", code)
	}
}

// TestClientRoundTrip confirms pkg/githubdgrpc.Client (apid's handle)
// can talk to the githubd Server via bufconn. Slice 1 proves the
// client+server contract is consistent (same proto types, same status
// envelope) — slice 7 will exercise a happy path.
func TestClientRoundTrip(t *testing.T) {
	c := githubdgrpc.NewClient(newBufConn(t, newProtoServer(t)))
	_, _, _, err := c.GetInstallState(context.Background(), "acct-1")
	if err == nil {
		t.Fatal("expected unimplemented error via client")
	}
	if code := status.Code(err); code != codes.Unimplemented {
		t.Errorf("code = %v, want Unimplemented", code)
	}
}

// newProtoServer + newBufConn factor out the round-trip setup so the
// client test doesn't have to duplicate the listener plumbing. They
// return the underlying *grpc.ClientConn (test-owned cleanup).
func newProtoServer(t *testing.T) *grpc.Server {
	t.Helper()
	srv := grpc.NewServer()
	githubdgrpc.New(&recordingSvc{}, wire.NewOpsMetrics("githubd_test_client"), nil).Register(srv)
	t.Cleanup(srv.Stop)
	return srv
}

// TestRecorderEarnsItsKeep confirms the slice-1 fake actually records
// calls (otherwise the unused-field lint trips). The recorder's real
// payoff lands in slices 7+8; this test just proves the wiring is live.
func TestRecorderEarnsItsKeep(t *testing.T) {
	svc := &recordingSvc{}
	srv := grpc.NewServer()
	githubdgrpc.New(svc, wire.NewOpsMetrics("githubd_test_recorder"), nil).Register(srv)
	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cli := githubdpb.NewGithubdClient(conn)
	if _, err := cli.GetInstallState(context.Background(), &githubdpb.GetInstallStateRequest{AccountId: "acct-rec"}); err == nil {
		t.Fatal("expected unimplemented error")
	}
	if svc.getInstallStateCalls != 1 {
		t.Errorf("recorder calls = %d, want 1", svc.getInstallStateCalls)
	}
	if svc.lastGetInstallAccountID != "acct-rec" {
		t.Errorf("recorder account = %q, want %q", svc.lastGetInstallAccountID, "acct-rec")
	}
}

func newBufConn(t *testing.T, srv *grpc.Server) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
