package vmmdgrpc

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func TestReadinessProbeForwardedToWakeAndColdBoot(t *testing.T) {
	const raw = `{"path":"/readyz","period_s":7}`
	wake, err := toWakeRequest(context.Background(), &vmmdpb.CreateFromSnapshotRequest{
		Instance: "instance-1",
		App:      &vmmdpb.AppSpec{ReadinessProbeJson: raw},
	})
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	if string(wake.ReadinessProbe) != raw {
		t.Fatalf("wake readiness probe = %s, want %s", wake.ReadinessProbe, raw)
	}

	cold, err := toColdBootRequest(context.Background(), &vmmdpb.CreateColdBootRequest{
		Instance: "instance-2",
		App:      &vmmdpb.AppSpec{ReadinessProbeJson: raw},
	})
	if err != nil {
		t.Fatalf("toColdBootRequest: %v", err)
	}
	if string(cold.ReadinessProbe) != raw {
		t.Fatalf("cold-boot readiness probe = %s, want %s", cold.ReadinessProbe, raw)
	}
}
