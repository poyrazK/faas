package main

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestResolveRuntimeLogIdentityJoinsStateProvenance(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "logs@example.test", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "logs-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	createdAt := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		ID: "dep-1", AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive,
		CommitSHA: "abc123", Tag: "stable", ImageDigest: "sha256:deadbeef", CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	region := "eu-fsn1"
	if _, err := store.CreateComputeNode(ctx, state.ComputeNode{ID: "node-1", Name: "node-1", Active: true, Region: &region}); err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateRunning), 256, "node-1", "wake-1")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	identity := resolveRuntimeLogIdentity(ctx, store, scheddgrpc.LogFrame{InstanceID: instance.ID}, account.ID, app.ID, "", make(map[string]api.PlatformIdentity))
	if identity.AppID != app.ID || identity.TenantID != account.ID || identity.DeploymentID != deployment.ID || identity.InstanceID != instance.ID {
		t.Fatalf("identity ids = %+v", identity)
	}
	if identity.NodeID != "node-1" || identity.Region != region || identity.CommitSHA != "abc123" || identity.DeploymentTag != "stable" || identity.ImageDigest != "sha256:deadbeef" {
		t.Fatalf("identity provenance = %+v", identity)
	}
	if identity.DeploymentCreatedAt != createdAt.Format(time.RFC3339Nano) {
		t.Fatalf("created_at = %q, want %q", identity.DeploymentCreatedAt, createdAt.Format(time.RFC3339Nano))
	}
}
