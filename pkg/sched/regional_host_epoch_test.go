package sched

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"google.golang.org/grpc"
)

type hostEpochStatsClient struct {
	vmmdpb.VmmdClient
	epoch *vmmdpb.RegionalRuntimeHostEpoch
}

func (c *hostEpochStatsClient) Stats(context.Context, *vmmdpb.StatsRequest, ...grpc.CallOption) (*vmmdpb.StatsResponse, error) {
	return &vmmdpb.StatsResponse{RegionalRuntimeHostEpoch: c.epoch}, nil
}

func TestVMMClientRegionalHostEpochRejectsMalformedWire(t *testing.T) {
	node, epoch := uuid.NewString(), uuid.NewString()
	for _, malformed := range []*vmmdpb.RegionalRuntimeHostEpoch{
		{}, {NodeId: node}, {HostEpochId: epoch}, {NodeId: uuid.Nil.String(), HostEpochId: epoch},
		{NodeId: node, HostEpochId: strings.ToUpper("a12af96c-3a14-4e94-8bc4-35d46421894c")},
	} {
		c := &VMMClient{cli: &hostEpochStatsClient{epoch: malformed}}
		if _, err := c.Stats(t.Context()); !errors.Is(err, runtimequota.ErrInvalid) {
			t.Fatalf("malformed wire accepted: %+v %v", malformed, err)
		}
	}
	if _, err := (*StatsSnapshot)(nil).RegionalRuntimeEpochForNode(node); !errors.Is(err, runtimequota.ErrTrust) {
		t.Fatalf("nil discovery accepted: %v", err)
	}
}
