package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prod test 2026-11-15: an instance that stopped without releasing its ledger
// reservation left one app slot counted. Ordinary wakes repaired it (the
// request gate reconciles before its cap decision), but the deployment smoke
// admits an explicit deployment, which bypasses the gates; the ledger refused
// it at max_concurrency + the rollout overlap, so every immediate-rollout
// verification of that app got 429 until schedd restarted. A cap refusal now
// releases stale reservations and admits once more.
func TestDeploymentSmokeRepairsAStaleReservationBeforeRefusing(t *testing.T) {
	ctx := context.Background()
	mem, e, app, v1, ids := seedSupersededDrain(ctx, t, api.PlanFree, 1, 1)
	leaked := ids[0]
	// A canary brings v2 up beside v1's instance in the rollout overlap.
	v2 := goLiveBeside(ctx, t, mem, app, v1)
	serving, err := e.AdmitInstance(ctx, app.ID, "", "", sched.TriggerGateway)
	if err != nil || serving.AtCapacity || serving.DeploymentID != v2.ID {
		t.Fatalf("v2 wake beside v1 = %+v, %v", serving, err)
	}
	// The rollout completes: v1's instance stops, but its reservation stays
	// held, so the ledger counts two for a one-instance app.
	if err := mem.UpdateInstanceStateToTerminal(ctx, leaked, string(state.StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	if !e.Ledger().ResidentFor(leaked) || e.Ledger().Concurrency(app.ID) != 2 {
		t.Fatal("fixture: the stopped instance's reservation should still be counted")
	}
	candidate, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", Status: state.DeploySnapshotting})
	if err != nil {
		t.Fatal(err)
	}

	res, err := e.AdmitInstanceForDeployment(ctx, app.ID, candidate.ID, "", sched.TriggerDeploymentSmoke)
	if err != nil || res.AtCapacity || res.DeploymentID != candidate.ID {
		t.Fatalf("smoke beside a serving instance with a stale reservation = %+v, %v; want the candidate admitted", res, err)
	}
	if e.Ledger().ResidentFor(leaked) {
		t.Fatal("stale reservation still held after the smoke admission")
	}
	if got := e.Ledger().Concurrency(app.ID); got != 2 {
		t.Fatalf("ledger concurrency = %d, want the serving instance plus the candidate", got)
	}
}
