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

func TestProjectReleaseSetRejectsHeldGitOpsCandidateUntilGraphActivation(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "held-release-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := m.CreateProject(ctx, Project{AccountID: account.ID, Slug: "held-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "held-app", Status: AppActive,
		Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", ImageDigest: "sha256:held-candidate",
		TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	candidate.EnvironmentWorkloadRuntime = "{}"
	m.deployments[candidate.ID] = candidate
	m.mu.Unlock()
	if err := m.MarkDeploymentLive(ctx, candidate.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("ordinary promotion of held candidate = %v, want conflict", err)
	}
	if err := m.MarkDeploymentLiveDark(ctx, candidate.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("individual dark promotion of held candidate = %v, want conflict", err)
	}
	if err := m.UpdateDeploymentStatus(ctx, candidate.ID, DeployLive, ""); !errors.Is(err, ErrInvalidStateTransition) {
		t.Fatalf("direct status promotion of held candidate = %v, want invalid transition", err)
	}
	if current, err := m.DeploymentByID(ctx, candidate.ID); err != nil || current.Status != DeployPending {
		t.Fatalf("held candidate changed after rejected promotion: %+v, %v", current, err)
	}

	// Once graph activation lifts the temporary hold, individual promotion and
	// rollback APIs must still not take ownership back from the GitOps graph.
	m.mu.Lock()
	candidate = m.deployments[candidate.ID]
	candidate.Status = DeployLive
	candidate.TrafficPercent = 0
	candidate.TrafficPercentExplicit = true
	candidate.EnvironmentWorkloadHeldValue = environmentWorkloadHeldFlag(false)
	m.deployments[candidate.ID] = candidate
	m.mu.Unlock()
	if err := m.MarkDeploymentLive(ctx, candidate.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("ordinary promotion of activated candidate = %v, want conflict", err)
	}
	if err := m.MarkDeploymentLiveDark(ctx, candidate.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("individual dark promotion of activated candidate = %v, want conflict", err)
	}
	if _, err := m.PrepareDeploymentRollback(ctx, app.ID, candidate.ID); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("rollback preparation of activated candidate = %v, want invalid argument", err)
	}
	if err := m.UpdateDeploymentStatus(ctx, candidate.ID, DeploySuperseded, ""); !errors.Is(err, ErrInvalidStateTransition) {
		t.Fatalf("status update superseded an activated candidate: %v", err)
	}

	// Ordinary release-set publication still rejects a held GitOps candidate.
	m.mu.Lock()
	candidate = m.deployments[candidate.ID]
	candidate.EnvironmentWorkloadHeldValue = environmentWorkloadHeldFlag(true)
	m.deployments[candidate.ID] = candidate
	m.mu.Unlock()
	if _, err := m.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800,
		[]ProjectReleaseMember{{AppID: app.ID, DeploymentID: candidate.ID}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("release set accepted held GitOps candidate = %v, want conflict", err)
	}
	if _, err := m.PublishProjectReleaseSetIfActive(ctx, account.ID, project.ID, "production", "", nil, 1800,
		[]ProjectReleaseMember{{AppID: app.ID, DeploymentID: candidate.ID}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("compare-and-swap release set accepted held GitOps candidate = %v, want conflict", err)
	}
	if _, err := m.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected candidate became active in a release set: %v", err)
	}
}

func TestProjectReleaseSetPreservesActivatedGitOpsMembers(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "managed-release-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := m.CreateProject(ctx, Project{AccountID: account.ID, Slug: "managed-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "managed-app", Status: AppActive,
		Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", ImageDigest: "sha256:managed",
		TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLiveDark(ctx, managed.ID); err != nil {
		t.Fatal(err)
	}
	alternate, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", ImageDigest: "sha256:alternate",
		TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLiveDark(ctx, alternate.ID); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	managed = m.deployments[managed.ID]
	managed.EnvironmentWorkloadRuntime = "{}"
	managed.EnvironmentWorkloadHeldValue = environmentWorkloadHeldFlag(false)
	m.deployments[managed.ID] = managed
	m.mu.Unlock()
	active, err := m.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800,
		[]ProjectReleaseMember{{AppID: app.ID, DeploymentID: managed.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateDeploymentTraffic(ctx, managed.ID, 100); !errors.Is(err, ErrConflict) {
		t.Fatalf("traffic update changed the managed workload: %v", err)
	}
	if _, err := m.UpdateDeploymentTraffic(ctx, alternate.ID, 50); !errors.Is(err, ErrConflict) {
		t.Fatalf("traffic update on sibling changed managed workload weights: %v", err)
	}
	if _, err := m.UpdateDeploymentMinInstances(ctx, managed.ID, managed.MinInstances+1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("minimum-instance update changed managed workload: %v", err)
	}
	if err := m.SetDeploymentSourceURL(ctx, managed.ID, "unapproved-source", "unapproved"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("source update changed managed workload: %v", err)
	}
	if _, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", Status: DeployLive, ImageDigest: "sha256:unreviewed",
		TrafficPercent: 100}); !errors.Is(err, ErrInvalidStateTransition) {
		t.Fatalf("direct live deployment bypassed the managed graph: %v", err)
	}
	if _, err := m.PublishProjectReleaseSetIfActive(ctx, account.ID, project.ID, "production", active.ID, nil, 1800,
		[]ProjectReleaseMember{{AppID: app.ID, DeploymentID: alternate.ID}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("ordinary release publication replaced the GitOps member: %v", err)
	}
	if err := m.DeactivateProjectReleaseSetIfActive(ctx, account.ID, project.ID, "production", active.ID, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("ordinary release deactivation removed the GitOps graph: %v", err)
	}
	current, err := m.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
	if err != nil || current.ID != active.ID || releaseMemberForApp(current, app.ID) != managed.ID {
		t.Fatalf("rejected update changed active GitOps graph: %+v, %v", current, err)
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

func TestProjectReleaseSetPromotionCASAndRollback(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "promotion-graph-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := m.CreateProject(ctx, Project{AccountID: account.ID, Slug: "promotion-graph"})
	if err != nil {
		t.Fatal(err)
	}
	createApp := func(slug string) App {
		t.Helper()
		app, err := m.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: slug, Status: AppActive,
			Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	createLive := func(app App, digest string) Deployment {
		t.Helper()
		dep, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", ImageDigest: digest})
		if err != nil {
			t.Fatal(err)
		}
		if err := m.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		return dep
	}
	apiApp, billingApp := createApp("promotion-api"), createApp("promotion-billing")
	apiOld, billingOld := createLive(apiApp, "sha256:api-old"), createLive(billingApp, "sha256:billing-old")
	oldGraph, err := m.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []ProjectReleaseMember{
		{AppID: apiApp.ID, DeploymentID: apiOld.ID}, {AppID: billingApp.ID, DeploymentID: billingOld.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	apiNew, err := m.CreateDeployment(ctx, Deployment{AppID: apiApp.ID, Scope: "production", ImageDigest: "sha256:api-new",
		TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	billingNew, err := m.CreateDeployment(ctx, Deployment{AppID: billingApp.ID, Scope: "production", ImageDigest: "sha256:billing-new",
		TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range []Deployment{apiNew, billingNew} {
		if err := m.MarkDeploymentLiveDark(ctx, dep.ID); err != nil {
			t.Fatalf("stage %s dark: %v", dep.ID, err)
		}
	}
	if live, err := m.LiveDeploymentForScope(ctx, billingApp.ID, "production"); err != nil || live.ID != billingOld.ID {
		t.Fatalf("staging changed weighted route: %+v, %v", live, err)
	}
	newMembers := []ProjectReleaseMember{{AppID: apiApp.ID, DeploymentID: apiNew.ID}, {AppID: billingApp.ID, DeploymentID: billingNew.ID}}
	newGraph, err := m.PublishProjectReleaseSetIfActive(ctx, account.ID, project.ID, "production", oldGraph.ID, nil, 1800, newMembers)
	if err != nil {
		t.Fatalf("atomically activate new graph: %v", err)
	}
	if active, err := m.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production"); err != nil || active.ID != newGraph.ID {
		t.Fatalf("active graph after publish = %+v, %v", active, err)
	}
	if id, deployment, err := m.ResolveProjectRelease(ctx, billingApp.ID, "production", oldGraph.ID); err != nil || id != oldGraph.ID || deployment != billingOld.ID {
		t.Fatalf("old client graph = %q/%q, %v", id, deployment, err)
	}
	if _, err := m.PublishProjectReleaseSetIfActive(ctx, account.ID, project.ID, "production", oldGraph.ID, nil, 1800, newMembers); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale graph activation = %v, want conflict", err)
	}
	restored, err := m.PublishProjectReleaseSetIfActive(ctx, account.ID, project.ID, "production", newGraph.ID, nil, 1800, []ProjectReleaseMember{
		{AppID: apiApp.ID, DeploymentID: apiOld.ID}, {AppID: billingApp.ID, DeploymentID: billingOld.ID},
	})
	if err != nil {
		t.Fatalf("restore previous graph: %v", err)
	}
	if id, deployment, err := m.ResolveProjectRelease(ctx, billingApp.ID, "production", ""); err != nil || id != restored.ID || deployment != billingOld.ID {
		t.Fatalf("rolled back active graph = %q/%q, %v", id, deployment, err)
	}
	if id, deployment, err := m.ResolveProjectRelease(ctx, billingApp.ID, "production", newGraph.ID); err != nil || id != newGraph.ID || deployment != billingNew.ID {
		t.Fatalf("promoted graph retained for existing clients = %q/%q, %v", id, deployment, err)
	}
}

func TestProjectReleaseSetPromotionCanRemoveFirstGraphWithoutTrafficCutover(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "promotion-first-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := m.CreateProject(ctx, Project{AccountID: account.ID, Slug: "promotion-first"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "promotion-first-api", Status: AppActive,
		Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	old, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", ImageDigest: "sha256:first-old"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLive(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	next, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", ImageDigest: "sha256:first-new",
		TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLiveDark(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	graph, err := m.PublishProjectReleaseSetIfActive(ctx, account.ID, project.ID, "production", "",
		[]ProjectReleaseMember{{AppID: app.ID, DeploymentID: old.ID}}, 1800,
		[]ProjectReleaseMember{{AppID: app.ID, DeploymentID: next.ID}})
	if err != nil {
		t.Fatalf("publish first graph: %v", err)
	}
	if err := m.DeactivateProjectReleaseSetIfActive(ctx, account.ID, project.ID, "production", graph.ID,
		[]ProjectReleaseMember{{AppID: app.ID, DeploymentID: old.ID}}); err != nil {
		t.Fatalf("remove first graph on rollback: %v", err)
	}
	if _, err := m.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("active graph after rollback = %v, want not found", err)
	}
	if live, err := m.LiveDeploymentForScope(ctx, app.ID, "production"); err != nil || live.ID != old.ID {
		t.Fatalf("rollback changed ordinary route: %+v, %v", live, err)
	}
	if id, deployment, err := m.ResolveProjectRelease(ctx, app.ID, "production", graph.ID); err != nil || id != graph.ID || deployment != next.ID {
		t.Fatalf("existing client after graph rollback = %q/%q, %v", id, deployment, err)
	}
}
