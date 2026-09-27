//go:build linux

package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppReadinessProbeTransitionsAreReversible(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	platform := events.NewPlatform("vmmd", store, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	outcome := livenessOutcomeOK
	loop := &appReadinessProbeLoop{
		instance: "instance-1",
		appID:    "app-1",
		cfg:      fcvm.ReadinessProbeConfig{PeriodSeconds: 5, TimeoutSeconds: 2, FailureThreshold: 2},
		events:   platform,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		probeFn:  func(context.Context, int) string { return outcome },
	}
	assertReady := func(want bool) {
		t.Helper()
		got, err := store.LatestInstanceReadiness(ctx, []string{"instance-1"})
		if err != nil {
			t.Fatal(err)
		}
		readiness, ok := got["instance-1"]
		if !ok || readiness.Ready != want {
			t.Fatalf("readiness = %+v, present=%t; want ready=%t", readiness, ok, want)
		}
	}

	loop.emit(ctx, "unready", "awaiting_initial_probe")
	assertReady(false)
	loop.runOne(ctx)
	assertReady(true)

	outcome = livenessOutcomeNon200
	loop.runOne(ctx)
	assertReady(true) // One transient failure does not withdraw traffic.
	loop.runOne(ctx)
	assertReady(false)

	outcome = livenessOutcomeOK
	loop.runOne(ctx)
	assertReady(true) // Readiness recovery restores traffic without a restart.
}
