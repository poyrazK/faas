package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261121170000 amendment 2: a route split's row moves forward only
// from the phase the caller saw, one split per app is in flight, and the
// route's app is deployed from the app's live artifact with its values.
func TestMemRouteSplits(t *testing.T) {
	routeSplits(t, state.NewMemStore())
}

func TestPgRouteSplits(t *testing.T) {
	s, _, _ := pgWithPool(t)
	routeSplits(t, s)
}

func routeSplits(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, "route-split-"+uuid.NewString()[:8]+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	suffix := uuid.NewString()[:6]
	source, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop-" + suffix, Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 1024, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	live, err := s.CreateDeployment(ctx, state.Deployment{AppID: source.ID, Scope: "default", Kind: state.DeploymentKindImage, ImageDigest: "sha256:shop"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, live.ID, "/shop.ext4", "layers/shop.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAppEnv(ctx, acct.ID, source.ID, "COLOR", "blue"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAppSecret(ctx, acct.ID, source.ID, "API_KEY", []byte("sealed")); err != nil {
		t.Fatal(err)
	}

	split, err := s.CreateRouteSplit(ctx, state.RouteSplit{
		AccountID: acct.ID, SourceAppID: source.ID, TargetSlug: "shop-" + suffix + "-exports", Kind: "memory",
		Route: "GET /exports/{id}", Method: "GET", PathGlob: "/exports/*", RouteRAMMB: 1920, MainRAMMB: 256, PreviousRAMMB: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if split.Phase != state.RouteSplitCreating || split.ID == "" || len(split.EdgeRuleIDs) != 0 {
		t.Fatalf("created split = %+v", split)
	}
	if _, err := s.CreateRouteSplit(ctx, state.RouteSplit{
		AccountID: acct.ID, SourceAppID: source.ID, TargetSlug: "shop-" + suffix + "-other", Kind: "cpu",
		Route: "POST /render", PathGlob: "/render", RouteRAMMB: 1024, MainRAMMB: 1024, PreviousRAMMB: 1024,
	}); !errors.Is(err, state.ErrRouteSplitInFlight) {
		t.Fatalf("second split in flight: %v, want ErrRouteSplitInFlight", err)
	}

	target, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: split.TargetSlug, Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 1920, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	split.TargetAppID = target.ID
	if split, err = s.AdvanceRouteSplit(ctx, split, state.RouteSplitCreating); err != nil {
		t.Fatal(err)
	}
	seed := state.RouteSplitSeed{AccountID: acct.ID, SplitID: split.ID, SourceAppID: source.ID, TargetAppID: target.ID}
	dep, err := s.SeedRouteSplitTarget(ctx, seed)
	if err != nil {
		t.Fatal(err)
	}
	if dep.AppID != target.ID || dep.Reason != state.RouteSplitSeedReason+split.ID || dep.Status != state.DeploySnapshotting || dep.RootfsKey != "layers/shop.ext4" {
		t.Fatalf("seeded deployment = %+v", dep)
	}
	again, err := s.SeedRouteSplitTarget(ctx, seed)
	if err != nil || again.ID != dep.ID {
		t.Fatalf("second seed = %+v, %v; want the same deployment", again, err)
	}
	envs, err := s.ListAppEnv(ctx, acct.ID, target.ID)
	if err != nil || len(envs) != 1 || envs[0].Key != "COLOR" || envs[0].Value != "blue" {
		t.Fatalf("target values = %+v, %v", envs, err)
	}
	secrets, err := s.ListAppSecrets(ctx, acct.ID, target.ID)
	if err != nil || len(secrets) != 1 || secrets[0].Key != "API_KEY" || string(secrets[0].Ciphertext) != "sealed" {
		t.Fatalf("target secrets = %+v, %v", secrets, err)
	}

	split.TargetDeploymentID, split.Phase = dep.ID, state.RouteSplitDeploying
	if split, err = s.AdvanceRouteSplit(ctx, split, state.RouteSplitCreating); err != nil {
		t.Fatal(err)
	}
	stale := split
	stale.Phase = state.RouteSplitRolledBack
	if _, err := s.AdvanceRouteSplit(ctx, stale, state.RouteSplitCreating); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("advance from a stale phase: %v, want ErrConflict", err)
	}
	inFlight, err := s.InFlightRouteSplits(ctx, 10)
	if err != nil || !containsSplit(inFlight, split.ID) {
		t.Fatalf("in flight = %+v, %v", inFlight, err)
	}

	rule := uuid.NewString()
	split.EdgeRuleIDs, split.Phase = []string{rule}, state.RouteSplitRuleLive
	if split, err = s.AdvanceRouteSplit(ctx, split, state.RouteSplitDeploying); err != nil {
		t.Fatal(err)
	}
	split.MainResized, split.Phase = true, state.RouteSplitDone
	if split, err = s.AdvanceRouteSplit(ctx, split, state.RouteSplitRuleLive); err != nil {
		t.Fatal(err)
	}
	got, err := s.RouteSplit(ctx, acct.ID, split.ID)
	if err != nil || got.Phase != state.RouteSplitDone || !got.MainResized || len(got.EdgeRuleIDs) != 1 || got.EdgeRuleIDs[0] != rule ||
		got.TargetAppID != target.ID || got.TargetDeploymentID != dep.ID {
		t.Fatalf("read split = %+v, %v", got, err)
	}
	if _, err := s.RouteSplit(ctx, uuid.NewString(), split.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("other account read: %v, want ErrNotFound", err)
	}
	inFlight, err = s.InFlightRouteSplits(ctx, 10)
	if err != nil || containsSplit(inFlight, split.ID) {
		t.Fatalf("a done split is still in flight: %+v, %v", inFlight, err)
	}
	listed, err := s.RouteSplitsForApp(ctx, acct.ID, source.ID, 10)
	if err != nil || len(listed) != 1 || listed[0].ID != split.ID {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
	// A finished split frees the app for the next one.
	if _, err := s.CreateRouteSplit(ctx, state.RouteSplit{
		AccountID: acct.ID, SourceAppID: source.ID, TargetSlug: "shop-" + suffix + "-render", Kind: "cpu",
		Route: "POST /render", PathGlob: "/render", RouteRAMMB: 256, MainRAMMB: 256, PreviousRAMMB: 256,
	}); err != nil {
		t.Fatalf("split after a finished one: %v", err)
	}
}

func containsSplit(splits []state.RouteSplit, id string) bool {
	for _, s := range splits {
		if s.ID == id {
			return true
		}
	}
	return false
}
