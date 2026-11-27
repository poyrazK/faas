// adr: 20261126164847
package githubdgrpc_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type publicStreamCall struct {
	repo, ref string
	max       int64
}

// publicStreamSvc implements the optional PublicSourceRefService on top of
// the Unimplemented base.
type publicStreamSvc struct {
	githubdgrpc.UnimplementedService

	body  string
	sha   string
	err   error
	calls chan publicStreamCall
}

func (s *publicStreamSvc) StreamPublicSourceRef(_ context.Context, repo, ref string, maxArchiveBytes int64) (io.ReadCloser, string, error) {
	s.calls <- publicStreamCall{repo: repo, ref: ref, max: maxArchiveBytes}
	if s.err != nil {
		return nil, "", s.err
	}
	return io.NopCloser(strings.NewReader(s.body)), s.sha, nil
}

func newPublicStreamClient(t *testing.T, svc githubdgrpc.Service) *githubdgrpc.Client {
	t.Helper()
	srv := grpc.NewServer()
	githubdgrpc.New(svc, wire.NewOpsMetrics("githubd_public_stream"), nil).Register(srv)
	lis := bufconn.Listen(4 * 1024 * 1024)
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

func TestStreamPublicSourceRef_RoundTrip(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	payload := strings.Repeat("archive-", 20_000) // spans several 32 KiB frames
	svc := &publicStreamSvc{body: payload, sha: sha, calls: make(chan publicStreamCall, 1)}
	c := newPublicStreamClient(t, svc)

	res, err := c.StreamPublicSourceRef(context.Background(), "gregale-dev/mcp-server-node", "main", 1<<20)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("body = %d bytes, want %d", len(got), len(payload))
	}
	if res.Stats.ResolvedCommitSHA != sha || res.Stats.BytesStreamed != int64(len(payload)) || res.Stats.Truncated {
		t.Fatalf("stats = %+v, want sha %s, %d bytes, not truncated", *res.Stats, sha, len(payload))
	}
	if call := <-svc.calls; call != (publicStreamCall{repo: "gregale-dev/mcp-server-node", ref: "main", max: 1 << 20}) {
		t.Fatalf("service call = %+v", call)
	}
}

func TestStreamPublicSourceRef_ProblemPreservesCode(t *testing.T) {
	notPublic := api.NewProblem(http.StatusForbidden, api.CodeGitHubRepoNotAccessible, "Repository is not accessible", "private or missing")
	svc := &publicStreamSvc{err: notPublic, calls: make(chan publicStreamCall, 1)}
	c := newPublicStreamClient(t, svc)

	res, err := c.StreamPublicSourceRef(context.Background(), "acme/private", "main", 1<<20)
	if err != nil {
		t.Fatalf("initial stream: %v", err)
	}
	_, _ = io.ReadAll(res.Body)
	err = res.Body.Close()
	if p := api.AsProblem(err); p == nil || p.Code != api.CodeGitHubRepoNotAccessible {
		t.Fatalf("close error = %v, want code %s", err, api.CodeGitHubRepoNotAccessible)
	}
}

// A githubd whose service lacks the extension answers Unimplemented as a bare
// status, which apid reads as "no public path" (ADR-20261126164847,
// Compatibility).
func TestStreamPublicSourceRef_UnimplementedWithoutService(t *testing.T) {
	c := newPublicStreamClient(t, githubdgrpc.UnimplementedService{})

	res, err := c.StreamPublicSourceRef(context.Background(), "acme/api", "main", 1<<20)
	if err == nil {
		_, _ = io.ReadAll(res.Body)
		err = res.Body.Close()
	}
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("error = %v, want Unimplemented", err)
	}
}
