package main

// adr: 20261104132753 — meterd asks a gateway for edge frames only when it can
// persist them, and persists an edge frame before acknowledging it.

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	egresspb "github.com/onebox-faas/faas/api/proto/onebox/faas/egress/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

type edgeRequestClient struct {
	replayTestClient
	edgeFrames bool
}

func (c *edgeRequestClient) StreamBytes(ctx context.Context, req *egresspb.StreamBytesRequest, opts ...grpc.CallOption) (egresspb.EgressTxService_StreamBytesClient, error) {
	c.edgeFrames = req.GetEdgeFrames()
	return c.replayTestClient.StreamBytes(ctx, req, opts...)
}

func TestGatewayEgressAdapterAsksForAndPersistsEdgeFrames(t *testing.T) {
	t.Parallel()
	appID := "33333333-3333-4333-8333-333333333333"
	eventID := "44444444-4444-4444-8444-444444444444"
	frame := &egresspb.BytesFrame{
		EventId: eventID, InstanceId: api.EdgeUsageInstanceID(appID), AppId: appID,
		Minute: timestamppb.Now(), Bytes: 1 << 20,
	}
	client := &edgeRequestClient{replayTestClient: replayTestClient{stream: &replayTestStream{frame: frame}}}
	var got *egresspb.BytesFrame
	a := &gatewayEgressAdapter{persistFrame: func(_ context.Context, f *egresspb.BytesFrame) error {
		got = f
		return nil
	}}
	a.consumeStream(context.Background(), client, nil)
	if !client.edgeFrames {
		t.Fatal("a persisting adapter did not ask for edge frames")
	}
	if got == nil || got.GetAppId() != appID || len(client.acked) != 1 || client.acked[0] != eventID {
		t.Fatalf("persisted=%+v acked=%v, want the edge frame persisted then acknowledged", got, client.acked)
	}

	legacy := &edgeRequestClient{replayTestClient: replayTestClient{stream: &replayTestStream{}}}
	(&gatewayEgressAdapter{data: map[string]map[int64]gatewayUsageBucket{}}).consumeStream(context.Background(), legacy, nil)
	if legacy.edgeFrames {
		t.Fatal("an adapter without durable persistence asked for edge frames")
	}
}
