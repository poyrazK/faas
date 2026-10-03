// adr: 373 — DNS-gated egress hook to the local vmmd.
package main

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/state"
)

type recordingResolvedEgressClient struct {
	req *vmmdpb.AllowResolvedEgressRequest
	err error
}

func (c *recordingResolvedEgressClient) AllowResolvedEgress(_ context.Context, in *vmmdpb.AllowResolvedEgressRequest, _ ...grpc.CallOption) (*vmmdpb.AllowResolvedEgressAck, error) {
	c.req = in
	return &vmmdpb.AllowResolvedEgressAck{Matched: true}, c.err
}

func TestAllowResolvedEgressRequest(t *testing.T) {
	cli := &recordingResolvedEgressClient{}
	addrs := []netip.Addr{netip.MustParseAddr("198.51.100.10"), netip.MustParseAddr("2001:db8::1")}
	if err := allowResolvedEgress(context.Background(), cli, "10.100.0.7", addrs, 90*time.Second); err != nil {
		t.Fatal(err)
	}
	if cli.req.GetSourceIp() != "10.100.0.7" || cli.req.GetTtlSeconds() != 90 || len(cli.req.GetAddresses()) != 2 ||
		cli.req.GetAddresses()[1] != "2001:db8::1" {
		t.Fatalf("request = %+v", cli.req)
	}
	cli.err = status.Error(codes.Unimplemented, "old vmmd")
	if err := allowResolvedEgress(context.Background(), cli, "10.100.0.7", addrs, time.Minute); err != nil {
		t.Fatalf("an older vmmd must not be an error: %v", err)
	}
	cli.err = errors.New("unavailable")
	if err := allowResolvedEgress(context.Background(), cli, "10.100.0.7", addrs, time.Minute); err == nil {
		t.Fatal("a real failure must surface")
	}
}

type namedNodes struct {
	ids   map[string]string
	calls int
}

func (n *namedNodes) ComputeNodeByName(_ context.Context, name string) (state.ComputeNode, error) {
	n.calls++
	id, ok := n.ids[name]
	if !ok {
		return state.ComputeNode{}, state.ErrNotFound
	}
	return state.ComputeNode{ID: id, Name: name}, nil
}

type vmmdByID struct {
	clients map[string]*recordingResolvedEgressClient
	asked   []string
}

type resolvedEgressVmmd struct {
	vmmdpb.VmmdClient
	rec *recordingResolvedEgressClient
}

func (v resolvedEgressVmmd) AllowResolvedEgress(ctx context.Context, in *vmmdpb.AllowResolvedEgressRequest, opts ...grpc.CallOption) (*vmmdpb.AllowResolvedEgressAck, error) {
	return v.rec.AllowResolvedEgress(ctx, in, opts...)
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func (v *vmmdByID) ClientFor(_ context.Context, nodeID string) (vmmdpb.VmmdClient, io.Closer, bool) {
	v.asked = append(v.asked, nodeID)
	rec, ok := v.clients[nodeID]
	if !ok {
		return nil, nil, false
	}
	return resolvedEgressVmmd{rec: rec}, nopCloser{}, true
}

// The vmmd client cache is keyed by compute_nodes.id, but gatewayd-internal
// knows its node by manifest name. The hook looked the name up as an id, so
// on production-us every guest DNS answer failed with "local vmmd fsn-2.faas
// unavailable" and DNS-gated egress dropped every guest connection.
func TestResolvedEgressHookUsesTheLocalNodeID(t *testing.T) {
	const name, id = "fsn-2.faas", "da3fb5d2-b517-47cb-921f-b4212378a35d"
	nodes := &namedNodes{ids: map[string]string{name: id}}
	rec := &recordingResolvedEgressClient{}
	clients := &vmmdByID{clients: map[string]*recordingResolvedEgressClient{id: rec}}
	hook := newResolvedEgressHook(clients, newLocalNodeID(nodes, name))
	addrs := []netip.Addr{netip.MustParseAddr("104.20.22.46")}

	for i := 0; i < 3; i++ {
		if err := hook(context.Background(), "10.100.0.2:9186", addrs, time.Minute); err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
	}
	for _, asked := range clients.asked {
		if asked != id {
			t.Fatalf("vmmd looked up by %q, want the node id %q", asked, id)
		}
	}
	if nodes.calls != 1 {
		t.Fatalf("name resolved %d times, want it cached after the first query", nodes.calls)
	}
	if rec.req.GetSourceIp() != "10.100.0.2" || len(rec.req.GetAddresses()) != 1 {
		t.Fatalf("request = %+v", rec.req)
	}

	// A missing client forgets the id, so a re-registered node is re-resolved.
	delete(clients.clients, id)
	if err := hook(context.Background(), "10.100.0.2:1", addrs, time.Minute); err == nil {
		t.Fatal("an unavailable local vmmd must surface")
	}
	clients.clients[id] = rec
	if err := hook(context.Background(), "10.100.0.2:1", addrs, time.Minute); err != nil {
		t.Fatal(err)
	}
	if nodes.calls != 2 {
		t.Fatalf("name resolved %d times after a miss, want 2", nodes.calls)
	}

	unknown := newResolvedEgressHook(clients, newLocalNodeID(nodes, "fsn-9.faas"))
	if err := unknown(context.Background(), "10.100.0.2:1", addrs, time.Minute); err == nil {
		t.Fatal("an unregistered node name must surface")
	}
}
