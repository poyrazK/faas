package vmmdgrpc

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// ADR-20261119140000: AppSpec.probe_host reaches both wake paths; an older
// schedd's request carries none and keeps the legacy probe.
func TestProbeHostForwardedToWakeRequests(t *testing.T) {
	app := &vmmdpb.AppSpec{BaseKey: "/b", LayerKey: "/l", HealthcheckPath: "/healthz", ProbeHost: "my-site.gregale.dev"}
	restore, err := toWakeRequest(context.Background(), &vmmdpb.CreateFromSnapshotRequest{Instance: "i-restore", App: app})
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	cold, err := toColdBootRequest(context.Background(), &vmmdpb.CreateColdBootRequest{Instance: "i-cold", App: app})
	if err != nil {
		t.Fatalf("toColdBootRequest: %v", err)
	}
	if restore.ProbeHost != "my-site.gregale.dev" || cold.ProbeHost != "my-site.gregale.dev" {
		t.Fatalf("probe host restore=%q cold=%q, want my-site.gregale.dev", restore.ProbeHost, cold.ProbeHost)
	}
	legacy, err := toColdBootRequest(context.Background(), &vmmdpb.CreateColdBootRequest{Instance: "i-old", App: &vmmdpb.AppSpec{BaseKey: "/b", LayerKey: "/l"}})
	if err != nil || legacy.ProbeHost != "" {
		t.Fatalf("older request: probe host %q (err %v), want none", legacy.ProbeHost, err)
	}
}
