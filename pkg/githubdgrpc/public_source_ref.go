package githubdgrpc

import (
	"context"
	"io"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PublicSourceRefService streams the archive of a repository GitHub reports
// as public, with no account or installation (ADR-20261126164847). It is
// optional so older and test Service implementations stay valid; the server
// answers Unimplemented without it.
type PublicSourceRefService interface {
	StreamPublicSourceRef(ctx context.Context, repoFullName, ref string, maxArchiveBytes int64) (rc io.ReadCloser, resolvedCommitSHA string, err error)
}

// StreamPublicSourceRef streams a public repository's archive in the same
// frames as StreamSourceRef.
func (s *Server) StreamPublicSourceRef(req *githubdpb.StreamPublicSourceRefRequest, stream grpc.ServerStreamingServer[githubdpb.StreamSourceRefChunk]) error {
	const op = "StreamPublicSourceRef"
	service, ok := s.svc.(PublicSourceRefService)
	if !ok {
		return status.Error(codes.Unimplemented, "githubd: public source-ref streaming is not configured")
	}
	start := time.Now()
	rc, resolvedCommitSHA, err := service.StreamPublicSourceRef(stream.Context(), req.GetRepoFullName(), req.GetRef(), req.GetMaxArchiveBytes())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return toStatusErr(err)
	}
	if rc == nil {
		return status.Error(codes.Unavailable, "githubd: public source-ref streamer returned nil body")
	}
	return sendSourceArchive(stream, rc, resolvedCommitSHA, false, req.GetMaxArchiveBytes())
}

// StreamPublicSourceRef wraps the server-streaming RPC of the same name. The
// result reads exactly like StreamSourceRef's: Stats and any terminal error
// surface on Close, and problems keep their stable code through liftErr.
func (c *Client) StreamPublicSourceRef(ctx context.Context, repoFullName, ref string, maxArchiveBytes int64) (*StreamSourceRefResult, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := c.cli.StreamPublicSourceRef(streamCtx, &githubdpb.StreamPublicSourceRefRequest{
		RepoFullName:    repoFullName,
		Ref:             ref,
		MaxArchiveBytes: maxArchiveBytes,
	})
	if err != nil {
		cancel()
		return nil, liftErr(err)
	}
	return newSourceRefResult(stream, cancel), nil
}
