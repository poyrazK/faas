// adr: 20261126164847
package githubd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gitfetch"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakePublicStreamer struct {
	res SourceRefStream
	err error
}

func (f fakePublicStreamer) StreamPublic(context.Context, string, string, int64) (SourceRefStream, error) {
	return f.res, f.err
}

// ADR-20261126164847: what apid receives for each streamer outcome.
func TestStreamPublicSourceRefProblems(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct {
		name     string
		err      error
		ref      string
		wantCode string
	}{
		{name: "not public", err: fmt.Errorf("lookup: %w", ErrRepositoryNotPublic), ref: "main", wantCode: api.CodeGitHubRepoNotAccessible},
		{name: "missing branch", err: fmt.Errorf("commit: %w", gitfetch.ErrNotFound), ref: "main", wantCode: api.CodeInvalidRef},
		{name: "missing commit", err: fmt.Errorf("archive: %w", gitfetch.ErrNotFound), ref: sha, wantCode: api.CodeSourceRefUnavailable},
		{name: "refused or rate limited", err: fmt.Errorf("lookup: %w", gitfetch.ErrUnauthorized), ref: "main", wantCode: api.CodeSourceRefUnavailable},
		{name: "bad archive", err: fmt.Errorf("archive: %w", gitfetch.ErrBadArchive), ref: "main", wantCode: api.CodeSourceRefUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &RealService{PublicStreamer: fakePublicStreamer{err: tc.err}}
			_, _, err := svc.StreamPublicSourceRef(context.Background(), "acme/api", tc.ref, 1<<20)
			if p := api.AsProblem(err); p == nil || p.Code != tc.wantCode {
				t.Fatalf("err = %v, want problem %s", err, tc.wantCode)
			}
		})
	}
}

func TestStreamPublicSourceRefPassesBodyAndCommit(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	svc := &RealService{PublicStreamer: fakePublicStreamer{res: SourceRefStream{Body: io.NopCloser(strings.NewReader("tar")), ResolvedCommitSHA: sha}}}
	rc, got, err := svc.StreamPublicSourceRef(context.Background(), "acme/api", "main", 1<<20)
	if err != nil || got != sha {
		t.Fatalf("StreamPublicSourceRef = %q, %v", got, err)
	}
	body, _ := io.ReadAll(rc)
	if string(body) != "tar" {
		t.Fatalf("body = %q", body)
	}
	// Without FAAS_GITHUB_PUBLIC_READ_TOKEN githubd wires no streamer; the
	// RPC answers Unimplemented so apid keeps the installation answer.
	if _, _, err := (&RealService{}).StreamPublicSourceRef(context.Background(), "acme/api", "main", 1); status.Code(err) != codes.Unimplemented {
		t.Fatalf("unconfigured streamer err = %v, want Unimplemented", err)
	}
}

// The gRPC server finds the optional service by type assertion on the
// decorator, so the decorator must forward it, and must answer Unimplemented
// over a base without it.
func TestRecoveryServiceForwardsPublicSourceRef(t *testing.T) {
	base := &RealService{PublicStreamer: fakePublicStreamer{err: ErrRepositoryNotPublic}}
	var svc githubdgrpc.Service = NewRecoveryService(base, nil, nil)
	public, ok := svc.(githubdgrpc.PublicSourceRefService)
	if !ok {
		t.Fatal("RecoveryService does not expose PublicSourceRefService")
	}
	_, _, err := public.StreamPublicSourceRef(context.Background(), "acme/api", "main", 1)
	if p := api.AsProblem(err); p == nil || p.Code != api.CodeGitHubRepoNotAccessible {
		t.Fatalf("forwarded err = %v", err)
	}

	bare := NewRecoveryService(githubdgrpc.UnimplementedService{}, nil, nil)
	_, _, err = bare.StreamPublicSourceRef(context.Background(), "acme/api", "main", 1)
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("bare err = %v, want Unimplemented", err)
	}
}
