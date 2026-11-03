// adr: 20261101160300
package githubdgrpc_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// repositorySvc answers the single-repository lookup.
type repositorySvc struct {
	githubdgrpc.UnimplementedService
	repo  githubdgrpc.InstallationRepository
	found bool
	err   error
}

func (s *repositorySvc) GetInstallationRepository(context.Context, string, int64, string) (githubdgrpc.InstallationRepository, bool, error) {
	return s.repo, s.found, s.err
}

func newRepositoryClient(t *testing.T, svc githubdgrpc.Service) *githubdgrpc.Client {
	t.Helper()
	srv := grpc.NewServer()
	githubdgrpc.New(svc, wire.NewOpsMetrics("githubd_installation_repository"), nil).Register(srv)
	lis := bufconn.Listen(1 << 20)
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
	return githubdgrpc.NewClient(conn)
}

func TestGetInstallationRepository(t *testing.T) {
	ctx := context.Background()
	want := githubdgrpc.InstallationRepository{ID: 4242, OwnerID: 7, FullName: "acme/shop"}
	got, found, err := newRepositoryClient(t, &repositorySvc{repo: want, found: true}).GetInstallationRepository(ctx, "acct", 77, "acme/shop")
	if err != nil || !found || got != want {
		t.Fatalf("found repository = %+v, %v, %v; want %+v", got, found, err, want)
	}
	if got, found, err := newRepositoryClient(t, &repositorySvc{}).GetInstallationRepository(ctx, "acct", 77, "acme/shop"); err != nil || found {
		t.Fatalf("unseen repository = %+v, %v, %v; want not found", got, found, err)
	}
	if _, _, err := newRepositoryClient(t, &repositorySvc{err: errors.New("github down")}).GetInstallationRepository(ctx, "acct", 77, "acme/shop"); err == nil || errors.Is(err, githubdgrpc.ErrInstallationRepositoryUnsupported) {
		t.Fatalf("lookup failure = %v, want a provider error", err)
	}
	// A githubd from before the RPC: the caller lists repositories instead.
	if _, _, err := newRepositoryClient(t, githubdgrpc.UnimplementedService{}).GetInstallationRepository(ctx, "acct", 77, "acme/shop"); !errors.Is(err, githubdgrpc.ErrInstallationRepositoryUnsupported) {
		t.Fatalf("older githubd = %v, want ErrInstallationRepositoryUnsupported", err)
	}
}
