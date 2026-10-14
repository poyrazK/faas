//go:build linux

// adr: 1151 — a quiet-paused guest is never probed, and a probe that overlapped
// a pause is dropped, so pausing between requests never withdraws traffic or
// restarts an instance.
package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

func TestReadinessProbeSkipsQuietPausedGuest(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pausedAt   []bool   // gate reading before and after the probe
		epochs     []uint64 // epoch reading before and after the probe
		wantProbes int
		wantFails  int
	}{
		{name: "running guest is probed", pausedAt: []bool{false, false}, epochs: []uint64{4, 4}, wantProbes: 1, wantFails: 1},
		{name: "paused guest is skipped", pausedAt: []bool{true}, epochs: []uint64{5}, wantProbes: 0},
		{name: "paused during the probe", pausedAt: []bool{false, true}, epochs: []uint64{4, 5}, wantProbes: 1},
		{name: "paused and resumed during the probe", pausedAt: []bool{false, false}, epochs: []uint64{4, 6}, wantProbes: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			probes := 0
			loop := &appReadinessProbeLoop{
				instance: "instance-1",
				appID:    "app-1",
				cfg:      fcvm.ReadinessProbeConfig{PeriodSeconds: 5, TimeoutSeconds: 2, FailureThreshold: 3},
				log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
				probeFn: func(context.Context, int) string {
					probes++
					return livenessOutcomeNon200
				},
				quietGate: func() (bool, uint64) {
					i := min(reads, len(tc.pausedAt)-1)
					reads++
					return tc.pausedAt[i], tc.epochs[i]
				},
				ready: true,
			}
			loop.runOne(context.Background())
			if probes != tc.wantProbes || loop.failures != tc.wantFails || !loop.ready {
				t.Fatalf("probes=%d failures=%d ready=%v, want probes=%d failures=%d ready=true",
					probes, loop.failures, loop.ready, tc.wantProbes, tc.wantFails)
			}
		})
	}
}
