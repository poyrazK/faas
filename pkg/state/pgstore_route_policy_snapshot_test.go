//go:build !no_pg

package state_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPg_DeploymentRoutePolicySnapshotIsCapturedOnce(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	account, err := store.CreateAccount(ctx, fmt.Sprintf("route-policy-snapshot-%d@example.com", time.Now().UnixNano()), api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: account.ID, Slug: "route-policy-snapshot-" + account.ID,
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60, Status: state.AppActive,
	}, api.MustLimitsFor(account.Plan))
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	appID := app.ID
	createRule := func(path string) {
		t.Helper()
		if _, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
			AccountID: account.ID, AppID: appID, MatchHost: "api.example.com", MatchPath: path,
			MatchMethods: []string{"GET"}, Enabled: true, Kind: state.EdgeRuleKindRoute,
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute},
		}); err != nil {
			t.Fatalf("CreateEdgeRule(%s): %v", path, err)
		}
	}
	createRule("/before-live")
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:route-policy-snapshot", Scope: "prod",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	first, err := store.DeploymentRoutePolicySnapshotByDeployment(ctx, deployment.ID)
	if err != nil {
		t.Fatalf("read first snapshot: %v", err)
	}
	firstRules, err := state.UnmarshalDeploymentRoutePolicySnapshot(first)
	if err != nil || len(firstRules) != 1 || firstRules[0].MatchPath != "/before-live" {
		t.Fatalf("first snapshot rules=%+v err=%v", firstRules, err)
	}
	createRule("/after-live")
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatalf("MarkDeploymentLive retry: %v", err)
	}
	second, err := store.DeploymentRoutePolicySnapshotByDeployment(ctx, deployment.ID)
	if err != nil {
		t.Fatalf("read second snapshot: %v", err)
	}
	secondRules, err := state.UnmarshalDeploymentRoutePolicySnapshot(second)
	if err != nil || first.SHA256 != second.SHA256 || len(secondRules) != 1 || secondRules[0].MatchPath != "/before-live" {
		t.Fatalf("route policy snapshot changed after live: first=%+v second=%+v rules=%+v err=%v", first, second, secondRules, err)
	}
}
