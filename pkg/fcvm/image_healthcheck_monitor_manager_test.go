// adr:644
package fcvm

import (
	"context"
	"encoding/json"
	"testing"
)

func TestImageHealthcheckMonitorManagerLifecycle(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	registry := NewLivenessRegistry()
	var parents []context.Context
	m.WithLivenessProbes(registry, LivenessProbeConfig{}).WithLivenessProbeStarter(func(ctx context.Context, _ string, _ int, _ string, cfg LivenessProbeConfig) context.CancelFunc {
		if !cfg.ImageHealthcheckRequired || cfg.PeriodSeconds != 0 {
			t.Fatalf("image-only monitor configuration: %+v", cfg)
		}
		ctx, cancel := context.WithCancel(ctx)
		parents = append(parents, ctx)
		return cancel
	})
	m.mu.Lock()
	m.live["image"] = &Instance{ImageHealthcheckRequired: true}
	m.live["ordinary"] = &Instance{}
	m.mu.Unlock()
	m.startLivenessLoop(t.Context(), "ordinary", 1, nil)
	if len(parents) != 0 {
		t.Fatal("disabled ordinary liveness started")
	}
	m.startLivenessLoop(t.Context(), "image", 1, nil)
	if len(parents) != 1 {
		t.Fatal("image monitor depended on HTTP liveness being enabled")
	}
	m.cancelLivenessLoop("image")
	if parents[0].Err() != context.Canceled {
		t.Fatal("park/destroy cancellation did not stop image monitor")
	}
	m.startLivenessLoop(t.Context(), "image", 1, nil)
	if len(parents) != 2 || parents[1].Err() != nil {
		t.Fatal("resume reused a retired monitor")
	}
	m.cancelLivenessLoop("image")
}

func TestImageHealthcheckMonitorDoesNotInventHTTPHealthEndpoint(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "declared command", true: "additional explicit HTTP"}[explicit], func(t *testing.T) {
			m := newTestManager(&fakeRunner{}, &fakeVMM{})
			var captured LivenessProbeConfig
			m.WithLivenessProbes(NewLivenessRegistry(), LivenessProbeConfig{Path: "/healthz", PeriodSeconds: 5}).WithLivenessProbeStarter(func(_ context.Context, _ string, _ int, _ string, cfg LivenessProbeConfig) context.CancelFunc {
				captured = cfg
				return func() {}
			})
			m.mu.Lock()
			m.live["image"] = &Instance{ImageHealthcheckRequired: true}
			m.mu.Unlock()
			var override json.RawMessage
			if explicit {
				override = json.RawMessage("{\"path\":\"/ready\"}")
			}
			m.startLivenessLoop(t.Context(), "image", 1, override)
			if !captured.ImageHealthcheckRequired || (captured.PeriodSeconds > 0) != explicit {
				t.Fatalf("invented HTTP probe: %+v", captured)
			}
			if explicit && captured.Path != "/ready" {
				t.Fatal("explicit HTTP liveness was not retained")
			}
			m.cancelLivenessLoop("image")
		})
	}
}
