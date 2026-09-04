package state

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreCanaryProgressionIsAtomic(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "canary-atomic@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "canary-atomic"})
	if err != nil {
		t.Fatal(err)
	}

	prior, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:prior"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, prior.ID); err != nil {
		t.Fatal(err)
	}
	canary, err := store.CreateDeployment(ctx, Deployment{
		AppID:            app.ID,
		ImageDigest:      "sha256:canary",
		CanaryPreset:     "balanced",
		CanaryStep:       0,
		CanaryTotalSteps: 4,
		RolloutState:     "pending",
		TrafficPercent:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.DeploymentByID(ctx, prior.ID); err != nil || got.Status != DeployLive {
		t.Fatalf("prior after canary create = %+v, %v; want live residual", got, err)
	}
	if err := store.MarkDeploymentLive(ctx, canary.ID); err != nil {
		t.Fatal(err)
	}

	advance := func(expected, percent int) Deployment {
		t.Helper()
		got, _, err := store.AdvanceCanary(ctx, canary.ID, CanaryAdvanceParams{
			ExpectedStep:   expected,
			TrafficPercent: percent,
			Audit:          DeploymentAudit{Kind: DeployTrafficChanged, Actor: "test"},
		})
		if err != nil {
			t.Fatalf("AdvanceCanary(%d, %d): %v", expected, percent, err)
		}
		return got
	}

	got := advance(0, 10)
	if got.CanaryStep != 1 || got.TrafficPercent != 10 || got.RolloutState != "rolling_out" {
		t.Fatalf("first advance = %+v, want step=1 traffic=10 rolling_out", got)
	}
	prior, err = store.DeploymentByID(ctx, prior.ID)
	if err != nil {
		t.Fatal(err)
	}
	if prior.TrafficPercent != 90 {
		t.Errorf("prior traffic after first advance = %d, want 90", prior.TrafficPercent)
	}

	if _, _, err := store.AdvanceCanary(ctx, canary.ID, CanaryAdvanceParams{
		ExpectedStep:   0,
		TrafficPercent: 10,
		Audit:          DeploymentAudit{Kind: DeployTrafficChanged, Actor: "stale"},
	}); !errors.Is(err, ErrCanaryStepConflict) {
		t.Fatalf("stale advance error = %v, want ErrCanaryStepConflict", err)
	}
	unchanged, err := store.DeploymentByID(ctx, canary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.CanaryStep != 1 || unchanged.TrafficPercent != 10 {
		t.Fatalf("stale advance mutated deployment = %+v", unchanged)
	}

	got = advance(1, 50)
	if got.CanaryStep != 2 || got.TrafficPercent != 50 {
		t.Fatalf("second advance = %+v, want step=2 traffic=50", got)
	}
	prior, err = store.DeploymentByID(ctx, prior.ID)
	if err != nil {
		t.Fatal(err)
	}
	if prior.TrafficPercent != 50 {
		t.Errorf("prior traffic after second advance = %d, want 50", prior.TrafficPercent)
	}

	got = advance(2, 100)
	if got.CanaryStep != 4 || got.TrafficPercent != 100 || got.RolloutState != "complete" {
		t.Fatalf("terminal advance = %+v, want step=4 traffic=100 complete", got)
	}
	prior, err = store.DeploymentByID(ctx, prior.ID)
	if err != nil {
		t.Fatal(err)
	}
	if prior.Status != DeploySuperseded || prior.TrafficPercent != 0 {
		t.Errorf("prior after terminal advance = %+v, want superseded/0", prior)
	}
	audit, err := store.ListDeploymentAudit(ctx, canary.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 3 {
		t.Fatalf("audit rows = %d, want 3; rows=%+v", len(audit), audit)
	}
	for _, row := range audit {
		if row.DeploymentID != uuid.MustParse(canary.ID) || row.Kind != DeployTrafficChanged {
			t.Errorf("audit row = %+v, want canary traffic change", row)
		}
	}
}

func TestMemStoreFirstCanaryCompletesWithoutResidualRevision(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "canary-first@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "canary-first"})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, Deployment{
		AppID:            app.ID,
		ImageDigest:      "sha256:first-canary",
		CanaryPreset:     "balanced",
		CanaryStep:       0,
		CanaryTotalSteps: 4,
		RolloutState:     "pending",
		TrafficPercent:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != DeployLive || got.TrafficPercent != 100 || got.CanaryStep != got.CanaryTotalSteps || got.RolloutState != "complete" {
		t.Fatalf("first canary activation = %+v, want complete 100%% deployment", got)
	}
}

func TestMemStoreStableDeploymentClearsCanaryOverlap(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "canary-stable@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "canary-stable"})
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:prior"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, prior.ID); err != nil {
		t.Fatal(err)
	}
	canary, err := store.CreateDeployment(ctx, Deployment{
		AppID: app.ID, ImageDigest: "sha256:canary", CanaryPreset: "balanced",
		CanaryStep: 0, CanaryTotalSteps: 4, RolloutState: "pending", TrafficPercent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, canary.ID); err != nil {
		t.Fatal(err)
	}
	stable, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{prior.ID, canary.ID} {
		got, err := store.DeploymentByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != DeploySuperseded || got.TrafficPercent != 0 {
			t.Errorf("overlap deployment %s = %+v, want superseded/0", id, got)
		}
	}
	if stable.TrafficPercent != 100 || stable.Status != DeployPending {
		t.Fatalf("stable replacement = %+v, want pending/100", stable)
	}
}
