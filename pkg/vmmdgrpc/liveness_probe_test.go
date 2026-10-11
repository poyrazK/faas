package vmmdgrpc

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// production-us hunt #8: the deployment's liveness_probe override never
// reached vmmd, so plan defaults always applied.
func TestLivenessProbeForwardedToWakeAndColdBoot(t *testing.T) {
	const raw = `{"path":"/live","interval_s":60,"consecutive_failures":10}`
	wake, err := toWakeRequest(context.Background(), &vmmdpb.CreateFromSnapshotRequest{
		Instance: "instance-1",
		App:      &vmmdpb.AppSpec{LivenessProbeJson: raw},
	})
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	if string(wake.LivenessProbe) != raw {
		t.Fatalf("wake liveness probe = %s, want %s", wake.LivenessProbe, raw)
	}
	cold, err := toColdBootRequest(context.Background(), &vmmdpb.CreateColdBootRequest{
		Instance: "instance-2",
		App:      &vmmdpb.AppSpec{LivenessProbeJson: raw},
	})
	if err != nil {
		t.Fatalf("toColdBootRequest: %v", err)
	}
	if string(cold.LivenessProbe) != raw {
		t.Fatalf("cold-boot liveness probe = %s, want %s", cold.LivenessProbe, raw)
	}
}
