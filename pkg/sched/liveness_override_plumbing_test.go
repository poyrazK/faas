// adr: 078 — the deployment's liveness_probe override reaches the VM.
package sched

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppSpecToProtoCarriesLivenessProbe(t *testing.T) {
	const raw = `{"path":"/live","interval_s":60,"consecutive_failures":10}`
	if got := (AppSpec{LivenessProbeJSON: raw}).toProto().GetLivenessProbeJson(); got != raw {
		t.Fatalf("liveness probe JSON = %q, want %q", got, raw)
	}
}

// production-us hunt #8: apid stored overrides.liveness_probe, but no wake
// spec carried it, so every VM ran the plan defaults.
func TestWakeSendsDeploymentLivenessOverride(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 256, 4)
	raw := json.RawMessage(`{"path":"/live","interval_s":60,"consecutive_failures":10}`)
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:liveness-override", Status: state.DeployLive, OverrideLivenessProbe: raw})
	if err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := engine.Wake(ctx, app.ID, dep.ID, "", ""); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if got := vmm.lastColdBootSpec.LivenessProbeJSON; got != string(raw) {
		t.Fatalf("cold boot liveness probe = %q, want %q", got, raw)
	}
}
