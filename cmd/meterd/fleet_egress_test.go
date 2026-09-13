package main

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"
	"time"

	egresspb "github.com/onebox-faas/faas/api/proto/onebox/faas/egress/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fleetEgressNodeSource struct {
	nodes []state.ComputeNode
}

func (s *fleetEgressNodeSource) ActiveComputeNodes(context.Context) ([]state.ComputeNode, error) {
	return append([]state.ComputeNode(nil), s.nodes...), nil
}

func TestFleetGatewayEgressAdapterFansInBothComputeNodes(t *testing.T) {
	t.Parallel()
	anchor := time.Date(2026, 9, 13, 12, 0, 30, 0, time.UTC)
	gatewayA, gatewayB := "tcp://10.0.0.2:8080", "tcp://10.0.0.3:8080"
	nodes := &fleetEgressNodeSource{nodes: []state.ComputeNode{
		{ID: "node-a", Active: true, GatewayTargetURL: &gatewayA},
		{ID: "node-b", Active: true, GatewayTargetURL: &gatewayB},
	}}
	m := newFleetEgressMetrics()
	a := &fleetGatewayEgressAdapter{
		nodes: nodes, now: func() time.Time { return anchor }, metrics: m,
		active: make(map[string]*fleetEgressEntry),
		dialFn: func(context.Context, string, *tls.Config) (egresspb.EgressTxServiceClient, error) {
			return nil, errors.New("stream disabled in unit test")
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // reconcile still discovers; stream goroutines exit immediately.
	a.reconcile(ctx)

	a.mu.Lock()
	if len(a.active) != 2 {
		a.mu.Unlock()
		t.Fatalf("active streams = %d, want 2", len(a.active))
	}
	a.active["node-a"].adapter.recordFrame(&egresspb.BytesFrame{
		InstanceId: "instance-shared", Minute: timestamppb.New(anchor), Bytes: 100, Requests: 2, ColdBoots: 1,
	})
	a.active["node-b"].adapter.recordFrame(&egresspb.BytesFrame{
		InstanceId: "instance-shared", Minute: timestamppb.New(anchor), Bytes: 250, Requests: 3,
	})
	a.mu.Unlock()

	delta, ok := a.ReadUsageDeltas("instance-shared")
	if !ok || delta.TXBytes != 350 || delta.Requests != 5 || delta.ColdBootCount != 1 {
		t.Fatalf("fleet usage = %+v, %v; want bytes=350 requests=5 cold_boots=1", delta, ok)
	}
	if _, ok := a.ReadUsageDeltas("instance-shared"); ok {
		t.Fatal("fleet usage was delivered twice")
	}
	assertGaugeValue(t, m.registry, "meterd_fleet_egress_expected_streams", 2)
}

func TestFleetGatewayEgressAdapterRetainsTailAcrossNodeRemoval(t *testing.T) {
	t.Parallel()
	anchor := time.Date(2026, 9, 13, 12, 0, 30, 0, time.UTC)
	gateway := "tcp://[fd00::2]:8080"
	nodes := &fleetEgressNodeSource{nodes: []state.ComputeNode{{ID: "node-a", Active: true, GatewayTargetURL: &gateway}}}
	m := newFleetEgressMetrics()
	a := &fleetGatewayEgressAdapter{
		nodes: nodes, now: func() time.Time { return anchor }, metrics: m,
		active: make(map[string]*fleetEgressEntry),
		dialFn: func(context.Context, string, *tls.Config) (egresspb.EgressTxServiceClient, error) {
			return nil, errors.New("stream disabled in unit test")
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.reconcile(ctx)
	a.mu.Lock()
	entry := a.active["node-a"]
	if entry.target != "tcp://[fd00::2]:9092" {
		a.mu.Unlock()
		t.Fatalf("derived egress target = %q", entry.target)
	}
	entry.adapter.recordFrame(&egresspb.BytesFrame{InstanceId: "instance-a", Minute: timestamppb.New(anchor), Bytes: 99, Requests: 1})
	a.mu.Unlock()

	nodes.nodes = nil
	a.reconcile(ctx)
	delta, ok := a.ReadUsageDeltas("instance-a")
	if !ok || delta.TXBytes != 99 || delta.Requests != 1 {
		t.Fatalf("retired stream tail = %+v, %v", delta, ok)
	}
}

func TestTLSForServiceClonesWithoutMutatingSharedConfig(t *testing.T) {
	t.Parallel()
	original := &tls.Config{ServerName: "static.invalid", MinVersion: tls.VersionTLS13}
	clone := tlsForService(original, "egress.faas")
	if clone == original || clone.ServerName != "egress.faas" || original.ServerName != "static.invalid" {
		t.Fatalf("clone=%p/%q original=%p/%q", clone, clone.ServerName, original, original.ServerName)
	}
}
