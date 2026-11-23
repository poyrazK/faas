package sched_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261123120000
// A refused deployment smoke surfaces as deployment_verification_unavailable;
// the diagnostic must name the holders and the ledger counts, once per window.
func TestRefusedDeploymentSmokeLogsHoldersOncePerWindow(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, serving := sched.SeedApp(t, store, api.PlanScale, 256, 1)
	staged, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:def", Status: state.DeployLive,
		TrafficPercent: 0, TrafficPercentExplicit: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:123", Status: state.DeploySnapshotting,
		TrafficPercent: 0, TrafficPercentExplicit: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	var logs bytes.Buffer
	*e.Log() = slog.New(slog.NewTextHandler(&logs, nil))
	servingIns, err := e.AdmitInstance(ctx, app.ID, serving.ID, "", sched.TriggerGateway)
	if err != nil || servingIns.InstanceID == "" {
		t.Fatalf("admit serving: %+v %v", servingIns, err)
	}
	if staged, err := e.AdmitInstance(ctx, app.ID, staged.ID, "", sched.TriggerGateway); err != nil || staged.InstanceID == "" {
		t.Fatalf("admit staged: %+v %v", staged, err)
	}
	// Both slots are held by instances younger than MinInstanceAge, so the
	// zero-traffic sibling is not parked and the smoke is refused.
	for range 3 {
		res, err := e.AdmitInstance(ctx, app.ID, candidate.ID, "", sched.TriggerDeploymentSmoke)
		if err == nil && !res.AtCapacity {
			t.Fatalf("smoke admission = %+v; want a refusal", res)
		}
	}
	out := logs.String()
	if n := strings.Count(out, "sched: deployment smoke refused"); n != 1 {
		t.Fatalf("refusal logged %d times, want once per window:\n%s", n, out)
	}
	for _, want := range []string{candidate.ID, servingIns.InstanceID[:8], "ledger_app_concurrency=2", "live_instances="} {
		if !strings.Contains(out, want) {
			t.Fatalf("refusal log lacks %q:\n%s", want, out)
		}
	}
}
