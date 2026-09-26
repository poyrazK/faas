package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectReleaseSetKeepsServiceGraphConsistent(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "graph-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := m.CreateProject(ctx, Project{AccountID: account.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(slug string) (App, Deployment) {
		t.Helper()
		app, err := m.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: slug, Status: AppActive,
			Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", ImageDigest: "sha256:" + slug})
		if err != nil {
			t.Fatal(err)
		}
		if err := m.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		return app, dep
	}
	apiApp, apiV1 := create("shop-api")
	billing, billingV1 := create("shop-billing")
	if _, err := m.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-api-pr-12", PreviewOfSlug: apiApp.Slug,
		PreviewPrNumber: 12, Status: AppActive}); err != nil {
		t.Fatal(err)
	}
	first, err := m.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []ProjectReleaseMember{
		{AppID: apiApp.ID, DeploymentID: apiV1.ID}, {AppID: billing.ID, DeploymentID: billingV1.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ExpiresAt != nil {
		t.Fatalf("active release unexpectedly expires at %v", first.ExpiresAt)
	}
	manifest := billing.Manifest
	manifest.RevisionPinTTLSeconds = 0
	if _, err := m.UpdateApp(ctx, billing.ID, UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	billingV2, err := m.CreateDeployment(ctx, Deployment{AppID: billing.ID, Scope: "production", ImageDigest: "sha256:billing-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLive(ctx, billingV2.ID); err != nil {
		t.Fatal(err)
	}
	if releaseID, depID, err := m.ResolveProjectRelease(ctx, billing.ID, "production", first.ID); err != nil || releaseID != first.ID || depID != billingV1.ID {
		t.Fatalf("active graph after direct pin disabled = %q/%q, %v", releaseID, depID, err)
	}
	manifest.RevisionPinTTLSeconds = 3600
	if _, err := m.UpdateApp(ctx, billing.ID, UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.revisionPins[billingV1.ID] = time.Now().Add(-time.Minute)
	m.mu.Unlock()
	if count, err := m.ExpireRevisionPins(ctx); err != nil || count != 0 {
		t.Fatalf("active graph member swept: count %d err %v", count, err)
	}
	if _, err := m.ResolveRevisionPin(ctx, billing.ID, "production", billingV1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired direct revision pin = %v, want not found", err)
	}
	if releaseID, depID, err := m.ResolveProjectRelease(ctx, billing.ID, "production", first.ID); err != nil || releaseID != first.ID || depID != billingV1.ID {
		t.Fatalf("active graph with expired direct pin = %q/%q, %v", releaseID, depID, err)
	}
	second, err := m.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []ProjectReleaseMember{
		{AppID: apiApp.ID, DeploymentID: apiV1.ID}, {AppID: billing.ID, DeploymentID: billingV2.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ExpiresAt != nil {
		t.Fatalf("new active release unexpectedly expires at %v", second.ExpiresAt)
	}
	m.mu.Lock()
	retired := m.projectReleaseSets[first.ID]
	m.mu.Unlock()
	if retired.ExpiresAt == nil || time.Until(*retired.ExpiresAt) < 1700*time.Second {
		t.Fatalf("retired release TTL did not start at replacement: %v", retired.ExpiresAt)
	}
	m.mu.Lock()
	oldBillingPin := m.revisionPins[billingV1.ID]
	m.mu.Unlock()
	if oldBillingPin.Before(*retired.ExpiresAt) {
		t.Fatalf("old billing revision expires before the release set: %v < %v", oldBillingPin, retired.ExpiresAt)
	}
	if releaseID, depID, err := m.ResolveProjectRelease(ctx, billing.ID, "production", ""); err != nil || releaseID != second.ID || depID != billingV2.ID {
		t.Fatalf("active billing = %q/%q, %v", releaseID, depID, err)
	}
	if releaseID, depID, err := m.ResolveProjectRelease(ctx, billing.ID, "production", first.ID); err != nil || releaseID != first.ID || depID != billingV1.ID {
		t.Fatalf("old billing = %q/%q, %v", releaseID, depID, err)
	}
	if releaseID, depID, err := m.ResolveServiceRelease(ctx, apiApp.ID, apiV1.ID, billing.ID, first.ID); err != nil || releaseID != first.ID || depID != billingV1.ID {
		t.Fatalf("old graph service = %q/%q, %v", releaseID, depID, err)
	}
	if _, _, err := m.ResolveServiceRelease(ctx, apiApp.ID, apiV1.ID, billing.ID, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("ambiguous unmarked call = %v", err)
	}
	if _, _, err := m.ResolveServiceRelease(ctx, apiApp.ID, billingV1.ID, billing.ID, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("forged caller deployment = %v", err)
	}
	m.mu.Lock()
	expired := m.projectReleaseSets[first.ID]
	expires := time.Now().Add(-time.Second)
	expired.ExpiresAt = &expires
	m.projectReleaseSets[first.ID] = expired
	m.mu.Unlock()
	if _, _, err := m.ResolveProjectRelease(ctx, billing.ID, "production", first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired release = %v", err)
	}
}

func TestProjectReleaseSetActivatesDarkDeploymentWithoutTrafficShift(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "dark-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := m.CreateProject(ctx, Project{AccountID: account.ID, Slug: "dark-graph"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(slug string) (App, Deployment) {
		t.Helper()
		app, err := m.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: slug, Status: AppActive,
			Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", ImageDigest: "sha256:" + slug})
		if err != nil {
			t.Fatal(err)
		}
		if err := m.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		return app, dep
	}
	apiApp, apiV1 := create("dark-api")
	billing, billingV1 := create("dark-billing")
	first, err := m.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []ProjectReleaseMember{
		{AppID: apiApp.ID, DeploymentID: apiV1.ID}, {AppID: billing.ID, DeploymentID: billingV1.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	dark, err := m.CreateDeployment(ctx, Deployment{AppID: billing.ID, Scope: "production", ImageDigest: "sha256:dark-billing-v2",
		TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLive(ctx, dark.ID); err != nil {
		t.Fatal(err)
	}
	if weighted, err := m.LiveDeploymentForScope(ctx, billing.ID, "production"); err != nil || weighted.ID != billingV1.ID {
		t.Fatalf("dark deploy changed weighted route: %+v, %v", weighted, err)
	}
	second, err := m.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []ProjectReleaseMember{
		{AppID: apiApp.ID, DeploymentID: apiV1.ID}, {AppID: billing.ID, DeploymentID: dark.ID},
	})
	if err != nil {
		t.Fatalf("publish dark graph: %v", err)
	}
	if id, dep, err := m.ResolveProjectRelease(ctx, billing.ID, "production", ""); err != nil || id != second.ID || dep != dark.ID {
		t.Fatalf("active dark target = %q/%q, %v", id, dep, err)
	}
	if id, dep, err := m.ResolveServiceRelease(ctx, apiApp.ID, apiV1.ID, billing.ID, first.ID); err != nil || id != first.ID || dep != billingV1.ID {
		t.Fatalf("old graph target = %q/%q, %v", id, dep, err)
	}
	if id, dep, err := m.ResolveServiceRelease(ctx, apiApp.ID, apiV1.ID, billing.ID, second.ID); err != nil || id != second.ID || dep != dark.ID {
		t.Fatalf("new graph target = %q/%q, %v", id, dep, err)
	}
}
