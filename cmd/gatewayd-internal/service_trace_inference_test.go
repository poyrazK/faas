// adr: 1138, 20261102082606
package main

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
)

type inflightTraceClient struct {
	vmmdpb.VmmdClient
	got  string
	resp *vmmdpb.InflightRequestTraceResponse
	err  error
}

func (c *inflightTraceClient) InflightRequestTrace(_ context.Context, req *vmmdpb.InflightRequestTraceRequest, _ ...grpc.CallOption) (*vmmdpb.InflightRequestTraceResponse, error) {
	c.got = req.GetSourceIp()
	return c.resp, c.err
}

type inflightTraceClients struct {
	client *inflightTraceClient
	ok     bool
	node   string
}

func (c *inflightTraceClients) ClientFor(_ context.Context, nodeID string) (vmmdpb.VmmdClient, io.Closer, bool) {
	c.node = nodeID
	return c.client, io.NopCloser(nil), c.ok
}

func TestServiceTraceInference(t *testing.T) {
	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	localNode := newLocalNodeID(nil, "node-a")
	localNode.id = "node-1"
	client := &inflightTraceClient{resp: &vmmdpb.InflightRequestTraceResponse{Traceparent: traceparent, DeadlineUnixMs: 1_793_000_000_000}}
	clients := &inflightTraceClients{client: client, ok: true}
	infer := newServiceTraceInference(clients, localNode, time.Second)
	if got := infer(t.Context(), "10.100.0.7:41000"); got.Traceparent != traceparent || !got.Deadline.Equal(time.UnixMilli(1_793_000_000_000)) || client.got != "10.100.0.7" || clients.node != "node-1" {
		t.Fatalf("infer = %+v (asked %q on %q)", got, client.got, clients.node)
	}
	client.resp = &vmmdpb.InflightRequestTraceResponse{Traceparent: traceparent}
	if got := infer(t.Context(), "10.100.0.7:41000"); !got.Deadline.IsZero() {
		t.Fatalf("an older vmmd's answer has a deadline: %+v", got)
	}
	for _, remote := range []string{"", "not-an-address", "[fd00::7]:41000"} {
		client.got = ""
		if got := infer(t.Context(), remote); got != (gateway.InferredCallerRequest{}) || client.got != "" {
			t.Fatalf("remote %q = %+v, asked %q", remote, got, client.got)
		}
	}
	for _, err := range []error{status.Error(codes.Unimplemented, "old vmmd"), errors.New("down")} {
		client.err = err
		if got := infer(t.Context(), "10.100.0.7:41000"); got != (gateway.InferredCallerRequest{}) {
			t.Fatalf("vmmd error %v = %+v", err, got)
		}
	}
	client.err = nil
	clients.ok = false
	if got := infer(t.Context(), "10.100.0.7:41000"); got != (gateway.InferredCallerRequest{}) {
		t.Fatalf("no vmmd client = %+v", got)
	}
}
