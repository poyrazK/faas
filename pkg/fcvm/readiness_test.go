package fcvm

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestReadinessProbeConfigDefaultsAndOverrides(t *testing.T) {
	defaults, err := readinessProbeConfig(json.RawMessage(`{"path":"/readyz"}`))
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Path != "/readyz" || defaults.Port != 8080 || defaults.PeriodSeconds != api.DefaultReadinessPeriodSeconds || defaults.TimeoutSeconds != api.DefaultReadinessTimeoutSeconds || defaults.FailureThreshold != api.DefaultReadinessFailureThreshold {
		t.Fatalf("default config = %+v", defaults)
	}

	grpc, err := readinessProbeConfig(json.RawMessage(`{"grpc":{"service":"catalog.v1.Catalog"},"period_s":7,"timeout_s":3,"failure_threshold":4}`))
	if err != nil {
		t.Fatal(err)
	}
	if !grpc.GRPC || grpc.GRPCService != "catalog.v1.Catalog" || grpc.PeriodSeconds != 7 || grpc.TimeoutSeconds != 3 || grpc.FailureThreshold != 4 {
		t.Fatalf("override config = %+v", grpc)
	}
}

func TestManagerOwnsReadinessLoopLifecycle(t *testing.T) {
	mgr := NewManager(nil, nil, Paths{}, "", nil, nil)
	mgr.live["instance-1"] = &Instance{
		Lease: Lease{Slot: 4},
		AppID: "app-1",
		Port:  8081,
	}
	type startedLoop struct {
		ctx      context.Context
		instance string
		slot     int
		appID    string
		cfg      ReadinessProbeConfig
	}
	started := make(chan startedLoop, 1)
	mgr.WithReadinessProbeStarter(func(ctx context.Context, instance string, slot int, appID string, cfg ReadinessProbeConfig) {
		started <- startedLoop{ctx: ctx, instance: instance, slot: slot, appID: appID, cfg: cfg}
	})
	mgr.startReadinessLoop(context.Background(), "instance-1", 4, json.RawMessage(`{"path":"/readyz"}`))
	loop := <-started
	if loop.instance != "instance-1" || loop.slot != 4 || loop.appID != "app-1" || loop.cfg.Port != 8081 {
		t.Fatalf("started loop = %+v", loop)
	}
	mgr.cancelReadinessLoop("instance-1")
	select {
	case <-loop.ctx.Done():
	default:
		t.Fatal("Manager cancellation did not stop readiness loop")
	}
}

func TestReadinessProbeConfigRejectsMalformedPolicy(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"path":"readyz"}`,
		`{"path":"/readyz","grpc":{}}`,
		`{"path":"/readyz","period_s":61}`,
		`{"path":"/readyz","timeout_s":6}`,
		`{"path":"/readyz","failure_threshold":11}`,
	} {
		if _, err := readinessProbeConfig(json.RawMessage(raw)); err == nil {
			t.Errorf("readinessProbeConfig(%s) unexpectedly succeeded", raw)
		}
	}
}
