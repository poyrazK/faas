package main

import (
	"context"
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	egresspb "github.com/onebox-faas/faas/api/proto/onebox/faas/egress/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type billedEgressRecordingStore struct {
	instanceBilled []bool
	edgeFrames     int
}

func (s *billedEgressRecordingStore) AppendGatewayUsageEvent(_ context.Context, _, _, _ string, _ time.Time, _, _ int64, _ int32, billedEgress bool) error {
	s.instanceBilled = append(s.instanceBilled, billedEgress)
	return nil
}

func (s *billedEgressRecordingStore) AppendGatewayEdgeUsageEvent(context.Context, string, string, string, time.Time, int64) error {
	s.edgeFrames++
	return nil
}

// ADR-20261121210517: with [meter] gateway_response_egress on, every persisted
// instance frame is written as billed app egress; off, it stays diagnostic
// tx_bytes only. Edge frames keep their own path either way.
func TestFleetGatewayEgressAdapterBillsGatewayResponses(t *testing.T) {
	t.Parallel()
	for _, on := range []bool{true, false} {
		anchor := time.Date(2026, 11, 21, 12, 0, 30, 0, time.UTC)
		gateway := "tcp://10.0.0.2:8080"
		store := &billedEgressRecordingStore{}
		a := &fleetGatewayEgressAdapter{
			nodes:   &fleetEgressNodeSource{nodes: []state.ComputeNode{{ID: "node-a", Active: true, GatewayTargetURL: &gateway}}},
			store:   store,
			now:     func() time.Time { return anchor },
			metrics: newFleetEgressMetrics(),
			active:  make(map[string]*fleetEgressEntry),
			dialFn: func(context.Context, string, *tls.Config) (egresspb.EgressTxServiceClient, error) {
				return nil, errors.New("stream disabled in unit test")
			},
			gatewayResponseEgress: on,
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		a.reconcile(ctx)
		a.mu.Lock()
		persist := a.active["node-a"].adapter.persistFrame
		a.mu.Unlock()
		if persist == nil {
			t.Fatal("fleet adapter did not install a persisting frame writer")
		}
		frames := []*egresspb.BytesFrame{
			{EventId: "11111111-1111-4111-8111-111111111111", InstanceId: "instance-a", Minute: timestamppb.New(anchor), Bytes: 4096, Requests: 1},
			{EventId: "22222222-2222-4222-8222-222222222222", AppId: "app-a", Minute: timestamppb.New(anchor), Bytes: 512},
		}
		for _, frame := range frames {
			if err := persist(context.Background(), frame); err != nil {
				t.Fatal(err)
			}
		}
		if len(store.instanceBilled) != 1 || store.instanceBilled[0] != on || store.edgeFrames != 1 {
			t.Fatalf("on=%v: instance billed=%v edge frames=%d", on, store.instanceBilled, store.edgeFrames)
		}
	}
}

// FAAS_GATEWAY_RESPONSE_EGRESS overrides [meter] gateway_response_egress and
// refuses a value that is not a boolean.
func TestLoadConfig_GatewayResponseEgress(t *testing.T) {
	t.Setenv("FAAS_QUIET_PAUSE_UNBILLED", "")
	t.Setenv("FAAS_BILLING_USAGE_SOURCE", "")
	t.Setenv("FAAS_GATEWAY_RESPONSE_EGRESS", "")
	dir := t.TempDir()
	cfg, err := LoadConfig(filepath.Join(dir, "missing.toml"))
	if err != nil || cfg.Meter.GatewayResponseEgress {
		t.Fatalf("default = %+v (%v), want off", cfg, err)
	}
	tomlPath := filepath.Join(dir, "meterd.toml")
	if err := os.WriteFile(tomlPath, []byte("[meter]\ngateway_response_egress = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadConfig(tomlPath); err != nil || !cfg.Meter.GatewayResponseEgress {
		t.Fatalf("toml on: %+v, %v", cfg, err)
	}
	t.Setenv("FAAS_GATEWAY_RESPONSE_EGRESS", "false")
	if cfg, err = LoadConfig(tomlPath); err != nil || cfg.Meter.GatewayResponseEgress {
		t.Fatalf("env over toml: %+v, %v", cfg, err)
	}
	t.Setenv("FAAS_GATEWAY_RESPONSE_EGRESS", "maybe")
	if _, err := LoadConfig(tomlPath); err == nil {
		t.Fatal("FAAS_GATEWAY_RESPONSE_EGRESS=maybe accepted")
	}
}
