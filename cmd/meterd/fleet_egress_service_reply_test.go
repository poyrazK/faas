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

type serviceReplyEventStore struct {
	plain   int
	replies []int64
}

func (s *serviceReplyEventStore) AppendGatewayUsageEvent(context.Context, string, string, string, time.Time, int64, int64, int32) error {
	s.plain++
	return nil
}

func (s *serviceReplyEventStore) AppendGatewayEdgeUsageEvent(context.Context, string, string, string, time.Time, int64) error {
	return nil
}

func (s *serviceReplyEventStore) AppendGatewayUsageEventWithServiceReply(_ context.Context, _, _, _ string, _ time.Time, _, _ int64, _ int32, serviceReplyBytes int64) error {
	s.replies = append(s.replies, serviceReplyBytes)
	return nil
}

// Billing review R8-2 slice 3: a frame's service reply bytes reach the store
// only while [meter] internal_egress_unbilled is on.
func TestFleetGatewayEgressPersistsServiceReplyBytesOnlyWhenOn(t *testing.T) {
	anchor := time.Date(2026, 11, 15, 12, 0, 30, 0, time.UTC)
	gateway := "tcp://10.0.0.2:8080"
	frame := &egresspb.BytesFrame{InstanceId: "instance-1", EventId: "event-1", Minute: timestamppb.New(anchor), Bytes: 10, ServiceReplyBytes: 4096}
	for _, on := range []bool{false, true} {
		store := &serviceReplyEventStore{}
		a := &fleetGatewayEgressAdapter{
			nodes:  &fleetEgressNodeSource{nodes: []state.ComputeNode{{ID: "node-a", Active: true, GatewayTargetURL: &gateway}}},
			store:  store,
			now:    func() time.Time { return anchor },
			active: make(map[string]*fleetEgressEntry), metrics: newFleetEgressMetrics(),
			dialFn: func(context.Context, string, *tls.Config) (egresspb.EgressTxServiceClient, error) {
				return nil, errors.New("stream disabled in unit test")
			},
			serviceReplyUnbilled: on,
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		a.reconcile(ctx)
		if err := a.active["node-a"].adapter.persistFrame(context.Background(), frame); err != nil {
			t.Fatalf("on=%v: persistFrame: %v", on, err)
		}
		if on && (store.plain != 0 || len(store.replies) != 1 || store.replies[0] != 4096) {
			t.Fatalf("on: plain=%d replies=%v, want the 4096 reply bytes persisted", store.plain, store.replies)
		}
		if !on && (store.plain != 1 || len(store.replies) != 0) {
			t.Fatalf("off: plain=%d replies=%v, want the plain path", store.plain, store.replies)
		}
	}
}
