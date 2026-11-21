package gateway

// adr: 20261121202814 — with the forward hop on, a node vmmd that does not
// offer it is reached over gRPC, and the hop factory gets the node's dial
// target.

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/vmmdhop"
)

var errGRPCPath = errors.New("grpc path")

type grpcForwardClient struct {
	vmmdpb.VmmdClient
	calls int
}

func (c *grpcForwardClient) ForwardHTTPStream(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	c.calls++
	return nil, errGRPCPath
}

// A vmmd the hop cannot reach (here nothing listens) costs the call nothing:
// it goes to gRPC before any byte was sent on the hop.
func TestHopVmmdClientFallsBackToGRPC(t *testing.T) {
	hop, err := vmmdhop.NewClient("127.0.0.1:1", &tls.Config{ServerName: "vmmd.test", MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	defer hop.Close()
	inner := &grpcForwardClient{}
	_, err = hopVmmdClient{VmmdClient: inner, hop: hop}.ForwardHTTPStream(context.Background())
	if !errors.Is(err, errGRPCPath) || inner.calls != 1 {
		t.Fatalf("err = %v, gRPC calls = %d; want the gRPC path once", err, inner.calls)
	}
}

func TestNodeClientCacheBuildsForwardHopPerNode(t *testing.T) {
	prev := resolveNodeTarget
	SetNodeResolver(func(_ context.Context, nodeID string) (string, bool) { return "tcp://127.0.0.1:1", true })
	defer SetNodeResolver(prev)
	var mu sync.Mutex
	built := map[string]string{}
	cache := NewNodeClientCache(func(_ context.Context, target string) (*grpc.ClientConn, error) {
		return grpc.NewClient("passthrough:///127.0.0.1:1", grpc.WithTransportCredentials(insecure.NewCredentials()))
	}, nil).WithForwardHop(func(_ context.Context, nodeID, target string) *vmmdhop.Client {
		mu.Lock()
		built[nodeID] = target
		mu.Unlock()
		c, _ := vmmdhop.NewClient("127.0.0.1:1", &tls.Config{ServerName: "vmmd.test", MinVersion: tls.VersionTLS13})
		return c
	})
	defer cache.Close()
	for range 2 {
		cli, lease, ok := cache.ClientFor(context.Background(), "node-a")
		if !ok {
			t.Fatal("no client")
		}
		if _, isHop := cli.(hopVmmdClient); !isHop {
			t.Fatalf("client is %T, want the hop wrapper", cli)
		}
		_ = lease.Close()
	}
	if len(built) != 1 || built["node-a"] != "tcp://127.0.0.1:1" {
		t.Fatalf("hop built for %v, want node-a once at its target", built)
	}
	if !cache.evict("node-a") {
		t.Fatal("evict found no entry")
	}
}
