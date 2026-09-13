package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemStoreAppSoftDeleteRestoreLifecycle(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acct, err := m.CreateAccount(ctx, "app-tombstone@example.com", "pro")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "tombstone", RAMMB: 256,
		IdleTimeoutS: 30, MaxConcurrency: 1, Status: AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := m.CreateDeployment(ctx, Deployment{
		ID: "tombstone-deployment", AppID: app.ID,
		Kind: DeploymentKindImage, Status: DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if _, err := m.CreateSnapshot(ctx, Snapshot{
		ID: "tombstone-snapshot", DeploymentID: dep.ID,
		FCVersion: "fc-1", StorageKey: SnapMemKey(dep.ID),
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	graceUntil := time.Now().UTC().Add(24 * time.Hour)
	deleted, err := m.ScheduleAppDeletion(ctx, app.ID, graceUntil)
	if err != nil {
		t.Fatalf("ScheduleAppDeletion: %v", err)
	}
	if deleted.Status != AppDeleted || deleted.DeletedAt == nil || deleted.DeleteGraceUntil == nil {
		t.Fatalf("tombstone = %+v, want deleted timestamps", deleted)
	}
	if !deleted.DeleteGraceUntil.Equal(graceUntil) {
		t.Fatalf("DeleteGraceUntil = %v, want %v", deleted.DeleteGraceUntil, graceUntil)
	}
	if _, err := m.AppBySlug(ctx, app.Slug); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AppBySlug deleted app = %v, want ErrNotFound", err)
	}
	if got, err := m.AppBySlugIncludingDeleted(ctx, app.Slug); err != nil || got.ID != app.ID {
		t.Fatalf("AppBySlugIncludingDeleted = %+v, %v", got, err)
	}
	if _, err := m.LatestSnapshot(ctx, dep.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LatestSnapshot deleted app = %v, want ErrNotFound", err)
	}
	deletedApps, err := m.ListDeletedApps(ctx)
	if err != nil || len(deletedApps) != 1 || deletedApps[0].ID != app.ID {
		t.Fatalf("ListDeletedApps = %+v, %v", deletedApps, err)
	}

	// A repeated request preserves the original deletion deadline.
	if repeated, err := m.ScheduleAppDeletion(ctx, app.ID, graceUntil.Add(24*time.Hour)); err != nil {
		t.Fatalf("repeated ScheduleAppDeletion: %v", err)
	} else if !repeated.DeleteGraceUntil.Equal(graceUntil) {
		t.Fatalf("repeated DeleteGraceUntil = %v, want original %v", repeated.DeleteGraceUntil, graceUntil)
	}
	if _, err := m.RestoreApp(ctx, app.ID); err != nil {
		t.Fatalf("RestoreApp: %v", err)
	}
	if restored, err := m.AppBySlug(ctx, app.Slug); err != nil || restored.Status != AppActive || restored.DeletedAt != nil || restored.DeleteGraceUntil != nil {
		t.Fatalf("restored app = %+v, %v", restored, err)
	}
	if _, err := m.RestoreApp(ctx, app.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("RestoreApp on active app = %v, want ErrConflict", err)
	}

	// An expired tombstone is eligible for permanent purge.
	if _, err := m.ScheduleAppDeletion(ctx, app.ID, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("expired ScheduleAppDeletion: %v", err)
	}
	if err := m.DeleteAppPermanently(ctx, app.ID); err != nil {
		t.Fatalf("DeleteAppPermanently: %v", err)
	}
	if _, err := m.AppByID(ctx, app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AppByID after purge = %v, want ErrNotFound", err)
	}
	if err := m.DeleteAppPermanently(ctx, app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeated DeleteAppPermanently = %v, want ErrNotFound", err)
	}
	if _, err := m.ScheduleAppDeletion(ctx, "missing-app", graceUntil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ScheduleAppDeletion missing app = %v, want ErrNotFound", err)
	}
}

func TestMemStoreSoftDeleteCascadeAlwaysStampsDeadlineAndDeduplicatesSharedArtifacts(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acct, err := m.CreateAccount(ctx, "shared-artifact@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	first, _ := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: "first", Status: AppActive})
	second, _ := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: "second", Status: AppActive})
	firstDep, _ := m.CreateDeployment(ctx, Deployment{ID: "first-dep", AppID: first.ID, Status: DeployLive})
	secondDep, _ := m.CreateDeployment(ctx, Deployment{ID: "second-dep", AppID: second.ID, Status: DeployLive})
	const shared = "apps/shared/rootfs.ext4"
	if err := m.SetDeploymentRootfs(ctx, firstDep.ID, "/first", shared, 1024); err != nil {
		t.Fatal(err)
	}
	if err := m.SetDeploymentRootfs(ctx, secondDep.ID, "/second", shared, 1024); err != nil {
		t.Fatal(err)
	}
	deleted, err := m.SoftDeleteAppCascade(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.DeletedAt == nil || deleted.DeleteGraceUntil == nil || !deleted.DeleteGraceUntil.After(*deleted.DeletedAt) {
		t.Fatalf("incomplete deletion deadline: %+v", deleted)
	}
	artifacts, err := m.ListAppDeletionArtifacts(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("shared artifact returned as exclusively deletable: %+v", artifacts)
	}
}
