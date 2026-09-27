//go:build !no_pg

package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgProjectEnvironmentPromotionReleaseGraphCutoverAndRollback(t *testing.T) {
	store, ctx, _ := pgWithPool(t)
	account, err := store.CreateAccount(ctx, "promotion-graph-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "promotion-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	sourceConfigValues, sourceConfigHash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"region":"eu","replicas":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{
		AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: "staging",
		ConfigHash: sourceConfigHash, Values: sourceConfigValues,
	}); err != nil {
		t.Fatal(err)
	}
	previousConfigValues, previousConfigHash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"region":"us","replicas":2}`))
	if err != nil {
		t.Fatal(err)
	}
	previousConfig, err := store.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{
		AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: "production",
		ConfigHash: previousConfigHash, Values: previousConfigValues,
	})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, ProjectID: project.ID, Slug: "app-" + uuid.NewString()[:8], WorkloadName: "api",
		Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 2, IdleTimeoutS: 60,
		Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600},
	})
	if err != nil {
		t.Fatal(err)
	}
	createLive := func(scope, digest string) state.Deployment {
		t.Helper()
		deployment, err := store.CreateDeployment(ctx, state.Deployment{
			AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: digest,
			Status: state.DeployPending, TrafficPercent: 100,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		return deployment
	}
	previous := createLive("production", "sha256:previous")
	previousGraph, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: previous.ID}})
	if err != nil {
		t.Fatal(err)
	}
	promotion, _, err := store.CreateProjectEnvironmentPromotion(ctx, state.ProjectEnvironmentPromotion{
		AccountID: account.ID, ProjectID: project.ID, ProjectSlug: project.Slug,
		FromEnvironment: "staging", ToEnvironment: "production", PromotionHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		IdempotencyKey: "graph-promotion-" + uuid.NewString(), Status: "running",
		PreviousTargetReleaseSetID: previousGraph.ID,
		SyncConfig:                 true, SourceConfigHash: sourceConfigHash, PreviousTargetConfigHash: previousConfigHash,
		SourceConfigSnapshot: sourceConfigValues, PreviousTargetConfigSnapshot: previousConfigValues,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared := createLive("production", "sha256:prepared")
	members := []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: prepared.ID}}
	publisher, ok := any(store).(state.ProjectEnvironmentPromotionReleaseSetStore)
	if !ok {
		t.Fatal("PgStore does not implement project environment promotion graph transactions")
	}
	activated, err := publisher.PublishProjectEnvironmentPromotionReleaseSet(ctx, account.ID, promotion.ID, 1800, members)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := publisher.PublishProjectEnvironmentPromotionReleaseSet(ctx, account.ID, promotion.ID, 1800, members)
	if err != nil || replayed.ID != activated.ID {
		t.Fatalf("cutover replay=%+v err=%v; want release %s", replayed, err, activated.ID)
	}
	current, err := store.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
	if err != nil || current.ID != activated.ID {
		t.Fatalf("active graph=%+v err=%v; want %s", current, err, activated.ID)
	}
	promotion, _, err = store.ProjectEnvironmentPromotionByID(ctx, account.ID, project.Slug, "production", promotion.ID)
	if err != nil || promotion.TargetReleaseSetID != activated.ID {
		t.Fatalf("durable graph checkpoint=%+v err=%v", promotion, err)
	}
	if !promotion.SyncConfig || promotion.TargetConfigVersion <= previousConfig.Version {
		t.Fatalf("durable config checkpoint=%+v; want config version after %d", promotion, previousConfig.Version)
	}
	activeConfig, err := store.ProjectEnvironmentConfigLatest(ctx, account.ID, project.ID, "production")
	if err != nil || activeConfig.ConfigHash != sourceConfigHash || string(activeConfig.Values) != string(sourceConfigValues) {
		t.Fatalf("cutover config=%+v err=%v; want source config %s", activeConfig, err, sourceConfigHash)
	}

	restored, err := publisher.RollbackProjectEnvironmentPromotionReleaseSet(ctx, account.ID, promotion.ID, previousGraph.TTLSeconds, previousGraph.Members)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID == previousGraph.ID || len(restored.Members) != 1 || restored.Members[0].DeploymentID != previous.ID {
		t.Fatalf("rollback graph=%+v, want a new graph containing %s", restored, previous.ID)
	}
	replayedRollback, err := publisher.RollbackProjectEnvironmentPromotionReleaseSet(ctx, account.ID, promotion.ID, previousGraph.TTLSeconds, previousGraph.Members)
	if err != nil || replayedRollback.ID != restored.ID {
		t.Fatalf("rollback replay=%+v err=%v; want release %s", replayedRollback, err, restored.ID)
	}
	promotion, _, err = store.ProjectEnvironmentPromotionByID(ctx, account.ID, project.Slug, "production", promotion.ID)
	if err != nil || promotion.RollbackConfigVersion <= promotion.TargetConfigVersion {
		t.Fatalf("durable rollback config checkpoint=%+v err=%v", promotion, err)
	}
	rolledBackConfig, err := store.ProjectEnvironmentConfigLatest(ctx, account.ID, project.ID, "production")
	if err != nil || rolledBackConfig.ConfigHash != previousConfigHash || string(rolledBackConfig.Values) != string(previousConfigValues) {
		t.Fatalf("rollback config=%+v err=%v; want previous config %s", rolledBackConfig, err, previousConfigHash)
	}
	external := createLive("production", "sha256:external")
	externalGraph, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: external.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.RollbackProjectEnvironmentPromotionReleaseSet(ctx, account.ID, promotion.ID, previousGraph.TTLSeconds, previousGraph.Members); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale rollback error=%v, want conflict", err)
	}
	current, err = store.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
	if err != nil || current.ID != externalGraph.ID {
		t.Fatalf("stale rollback overwrote graph: active=%+v err=%v", current, err)
	}
}
