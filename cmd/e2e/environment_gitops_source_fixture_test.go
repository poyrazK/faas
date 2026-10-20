package e2e_test

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest/gitarchive"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The provider boundary is local; apid still performs the real repository,
// archive, immutable revision and polling checks through its production RPC
// client. Faults never write source status or qualification receipts directly.
type gitOpsSourceFixture struct {
	githubdpb.UnimplementedGithubdServer
	mu          sync.RWMutex
	accountID   string
	head        string
	archives    map[string][]byte
	unavailable bool
	socket      string
}

func newGitOpsSourceFixture(t *testing.T) *gitOpsSourceFixture {
	t.Helper()
	// Keep the socket short enough for Darwin as well as the native runner.
	dir, err := os.MkdirTemp("/tmp", "gitops-source-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &gitOpsSourceFixture{socket: filepath.Join(dir, "githubd.sock"), archives: map[string][]byte{}}
	listener, err := net.Listen("unix", f.socket)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	githubdpb.RegisterGithubdServer(server, f)
	t.Cleanup(server.Stop)
	go func() { _ = server.Serve(listener) }()
	return f
}

func (f *gitOpsSourceFixture) setAccount(accountID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accountID = accountID
}

func (f *gitOpsSourceFixture) setUnavailable(unavailable bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unavailable = unavailable
}

func (f *gitOpsSourceFixture) add(t *testing.T, sha string, definition api.EnvironmentDefinition) {
	f.addFiles(t, sha, definition, nil)
}

func (f *gitOpsSourceFixture) addFiles(t *testing.T, sha string, definition api.EnvironmentDefinition, files map[string][]byte) {
	t.Helper()
	archive, err := gitarchive.Create(sha, definition, files)
	if err != nil {
		t.Fatal(err)
	}
	f.storeArchive(t, sha, archive)
}

func (f *gitOpsSourceFixture) storeArchive(t *testing.T, sha string, archive []byte) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if previous, exists := f.archives[sha]; exists && !bytes.Equal(previous, archive) {
		t.Fatal("fixture commit cannot replace immutable archive bytes")
	}
	f.archives[sha] = bytes.Clone(archive)
	f.head = sha
}

func (f *gitOpsSourceFixture) validScope(accountID string, installationID int64) bool {
	return f.accountID != "" && accountID == f.accountID && installationID == 42
}

func (f *gitOpsSourceFixture) ListInstallableRepos(_ context.Context, request *githubdpb.ListInstallableReposRequest) (*githubdpb.ListInstallableReposResponse, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if !f.validScope(request.AccountId, request.InstallationId) {
		return nil, status.Error(codes.PermissionDenied, "fixture installation scope mismatch")
	}
	return &githubdpb.ListInstallableReposResponse{Repos: []*githubdpb.Repo{{Id: 123, FullName: "example/shop", DefaultBranch: "main"}}}, nil
}

func (f *gitOpsSourceFixture) StreamSourceRef(request *githubdpb.StreamSourceRefRequest, stream grpc.ServerStreamingServer[githubdpb.StreamSourceRefChunk]) error {
	f.mu.RLock()
	if !f.validScope(request.AccountId, request.InstallationId) || request.RepoFullName != "example/shop" {
		f.mu.RUnlock()
		return status.Error(codes.PermissionDenied, "fixture repository scope mismatch")
	}
	sha := request.Ref
	if sha == "refs/heads/main" {
		sha = f.head
	}
	archive, exists := f.archives[sha]
	unavailable := f.unavailable
	f.mu.RUnlock()
	if unavailable {
		return status.Error(codes.Unavailable, "injected source outage")
	}
	if !exists {
		return status.Error(codes.NotFound, "fixture revision not found")
	}
	if request.MaxArchiveBytes <= 0 || int64(len(archive)) > request.MaxArchiveBytes {
		return status.Error(codes.ResourceExhausted, "fixture archive exceeds requested limit")
	}
	if err := stream.Send(&githubdpb.StreamSourceRefChunk{Data: archive}); err != nil {
		return err
	}
	return stream.Send(&githubdpb.StreamSourceRefChunk{BytesStreamed: int64(len(archive)), ResolvedCommitSha: sha})
}
