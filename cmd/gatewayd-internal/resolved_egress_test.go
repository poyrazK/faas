// adr: 373 — DNS-gated egress hook to the local vmmd.
package main

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
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
