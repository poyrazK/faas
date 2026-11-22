package gateway

import (
	"context"
	"errors"

	"google.golang.org/grpc"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/vmmdhop"
)

// ForwardHopFactory builds the forward hop client for one node's vmmd at
// target, or returns nil to keep that node on gRPC (ADR-20261121202814).
// name is the compute_nodes.name the node's gRPC dial was bound to
// (WithNodeBinding), "" without the binding; the hop pins the vmmd leaf to
// it. It runs inside the node's dial, so it must not block.
type ForwardHopFactory func(nodeID, target, name string) *vmmdhop.Client

// WithForwardHop makes each node's client carry ForwardHTTPStream over the
// light hop when its vmmd offers it, and over gRPC otherwise. Every other
// RPC stays on gRPC. nil leaves the cache as it was.
func (c *NodeClientCache) WithForwardHop(factory ForwardHopFactory) *NodeClientCache {
	if c != nil && factory != nil {
		c.forwardHop = factory
	}
	return c
}

// hopVmmdClient sends ForwardHTTPStream over the hop. The hop either carries
// the call or reports ErrFallback before sending anything, so falling back
// to gRPC never repeats a request.
type hopVmmdClient struct {
	vmmdpb.VmmdClient
	hop *vmmdhop.Client
}

func (c hopVmmdClient) ForwardHTTPStream(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	stream, err := vmmdhop.NewStream[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse](ctx, c.hop, vmmdpb.Vmmd_ForwardHTTPStream_FullMethodName)
	if errors.Is(err, vmmdhop.ErrFallback) {
		return c.VmmdClient.ForwardHTTPStream(ctx, opts...)
	}
	return stream, err
}
