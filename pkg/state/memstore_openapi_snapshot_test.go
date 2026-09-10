package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestMemStoreMarkDeploymentLiveCapturesOpenAPISnapshot(t *testing.T) {
	openapidiff.RegisterStateCapture()
	defer state.RegisterOpenAPICapture(nil)

	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "snapshot@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
		AccountID:    account.ID,
		AppID:        app.ID,
		MatchHost:    "api.example.com",
		MatchPath:    "/v1/orders",
		MatchMethods: []string{"GET"},
		Enabled:      true,
		Kind:         state.EdgeRuleKindRoute,
		Action:       state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute},
	}); err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}

	snapshot, err := store.OpenAPISnapshotByDeployment(ctx, deployment.ID)
	if err != nil {
		t.Fatalf("OpenAPISnapshotByDeployment: %v", err)
	}
	if snapshot.AppID != app.ID || snapshot.Scope != "prod" {
		t.Fatalf("snapshot identity = app=%q scope=%q, want app=%q scope=prod", snapshot.AppID, snapshot.Scope, app.ID)
	}
	if snapshot.SHA256 == "" || snapshot.SchemaVersion != openapidiff.SnapshotSchemaVersion {
		t.Fatalf("snapshot metadata = sha=%q schema=%d", snapshot.SHA256, snapshot.SchemaVersion)
	}
	spec, err := openapidiff.UnmarshalSnapshot(snapshot.Snapshot)
	if err != nil {
		t.Fatalf("UnmarshalSnapshot: %v", err)
	}
	if _, ok := spec.Paths["api.example.com/v1/orders"]; !ok {
		t.Fatalf("captured paths = %v, want projected route", spec.Paths)
	}
}

func TestMemStoreMarkDeploymentLiveCaptureFailureIsAtomic(t *testing.T) {
	state.RegisterOpenAPICapture(func(context.Context, sqlc.DBTX, string, string, string, []api.CreateEdgeRuleRequest) (state.OpenAPISnapshot, error) {
		return state.OpenAPISnapshot{}, context.Canceled
	})
	defer state.RegisterOpenAPICapture(nil)

	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "snapshot-fail@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "snapshot-fail"})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err == nil {
		t.Fatal("MarkDeploymentLive succeeded despite capture failure")
	}
	got, err := store.DeploymentByID(ctx, deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == state.DeployLive {
		t.Fatalf("deployment status = live after failed capture; want unchanged")
	}
}
