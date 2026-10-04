// adr: 531
package main

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTargetLifetimeReadinessProducerIdentity(t *testing.T) {
	store := state.NewMemStore()
	platform := events.NewPlatform("vmmd", store, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	platform.Emit(t.Context(), events.AppReadiness{EmitAt: time.Now(), AppID: "app", InstanceID: "instance", WakeID: "wake", NodeID: "node", Status: "ready"})
	emitter := &SidecarEventsThroughPlatform{Platform: platform}
	emitter.EmitSidecarHealth(t.Context(), "instance", "app", "wake", "node", sidecarHealthWire{Sidecar: "proxy", Status: "unready"})
	for _, identity := range []state.ReadinessTarget{
		{AppID: "app", InstanceID: "instance", WakeID: "wake", NodeID: "node"},
		{AppID: "app", InstanceID: "instance", WakeID: "old", NodeID: "node"},
		{AppID: "app", InstanceID: "instance", WakeID: "wake", NodeID: "other"},
	} {
		got, err := store.LatestInstanceReadinessForTargets(t.Context(), []state.ReadinessTarget{identity})
		want := identity.WakeID == "wake" && identity.NodeID == "node"
		if err != nil || (len(got) == 1) != want {
			t.Fatalf("producer identity=%+v got=%+v err=%v", identity, got, err)
		}
		if want && (!got["instance"]["primary_app"].Ready || got["instance"]["sidecar:proxy"].Ready || len(got["instance"]) != 2) {
			t.Fatalf("independent producer gates=%+v", got)
		}
	}
}
