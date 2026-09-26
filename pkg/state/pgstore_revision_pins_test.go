//go:build !no_pg

package state_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRevisionPinRetainsZeroTrafficStableDeployment(t *testing.T) {
	s, ctx, _ := pgWithPool(t)
	account, err := s.CreateAccount(ctx, "pin-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "pin-" + uuid.NewString()[:8], Type: state.AppTypeApp,
		RAMMB: 128, MaxConcurrency: 2, IdleTimeoutS: 60, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	create := func(digest string) state.Deployment {
		t.Helper()
		dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: digest, Status: state.DeployPending, Scope: "default", TrafficPercent: 100})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		return dep
	}
	old := create("sha256:pin-old")
	current := create("sha256:pin-new")
	if pinned, err := s.ResolveRevisionPin(ctx, app.ID, "default", old.ID); err != nil || pinned.ID != old.ID || pinned.TrafficPercent != 0 {
		t.Fatalf("old pin = %+v, %v", pinned, err)
	}
	if currentLive, err := s.LiveDeploymentForScope(ctx, app.ID, "default"); err != nil || currentLive.ID != current.ID {
		t.Fatalf("current live = %+v, %v", currentLive, err)
	}
}

func TestPgProjectReleaseGraphSurvivesExpiredDirectPin(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	account, err := s.CreateAccount(ctx, "graph-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "graph-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	createApp := func(slug string) state.App {
		t.Helper()
		name := slug + "-" + uuid.NewString()[:8]
		app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: name, WorkloadName: name,
			Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 2, IdleTimeoutS: 60,
			Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	apiApp, billingApp := createApp("api"), createApp("billing")
	if _, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "preview-" + uuid.NewString()[:8],
		PreviewOfSlug: apiApp.Slug, PreviewPrNumber: 12, Type: state.AppTypeApp, RAMMB: 128,
		Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}}); err != nil {
		t.Fatal(err)
	}
	createDeployment := func(app state.App, digest string) state.Deployment {
		t.Helper()
		dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: digest, Status: state.DeployPending, Scope: "production", TrafficPercent: 100})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		return dep
	}
	apiV1 := createDeployment(apiApp, "sha256:graph-api-v1")
	billingV1 := createDeployment(billingApp, "sha256:graph-billing-v1")
	first, err := s.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{
		{AppID: apiApp.ID, DeploymentID: apiV1.ID}, {AppID: billingApp.ID, DeploymentID: billingV1.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ExpiresAt != nil {
		t.Fatalf("active graph unexpectedly expires: %v", first.ExpiresAt)
	}
	manifest := billingApp.Manifest
	manifest.RevisionPinTTLSeconds = 0
	if _, err := s.UpdateApp(ctx, billingApp.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	billingV2 := createDeployment(billingApp, "sha256:graph-billing-v2")
	if releaseID, depID, err := s.ResolveProjectRelease(ctx, billingApp.ID, "production", first.ID); err != nil || releaseID != first.ID || depID != billingV1.ID {
		t.Fatalf("active graph after direct pin disabled = %q/%q, %v", releaseID, depID, err)
	}
	manifest.RevisionPinTTLSeconds = 3600
	if _, err := s.UpdateApp(ctx, billingApp.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployment_revision_pins set expires_at = now() - interval '1 minute' where deployment_id = $1`, billingV1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveRevisionPin(ctx, billingApp.ID, "production", billingV1.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired direct pin = %v, want not found", err)
	}
	if count, err := s.ExpireRevisionPins(ctx); err != nil || count != 0 {
		t.Fatalf("active graph member swept: count %d err %v", count, err)
	}
	if releaseID, depID, err := s.ResolveProjectRelease(ctx, billingApp.ID, "production", first.ID); err != nil || releaseID != first.ID || depID != billingV1.ID {
		t.Fatalf("old active graph = %q/%q, %v", releaseID, depID, err)
	}
	second, err := s.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{
		{AppID: apiApp.ID, DeploymentID: apiV1.ID}, {AppID: billingApp.ID, DeploymentID: billingV2.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if releaseID, depID, err := s.ResolveProjectRelease(ctx, billingApp.ID, "production", ""); err != nil || releaseID != second.ID || depID != billingV2.ID {
		t.Fatalf("new active graph = %q/%q, %v", releaseID, depID, err)
	}
	if releaseID, depID, err := s.ResolveServiceRelease(ctx, apiApp.ID, apiV1.ID, billingApp.ID, first.ID); err != nil || releaseID != first.ID || depID != billingV1.ID {
		t.Fatalf("old service graph = %q/%q, %v", releaseID, depID, err)
	}
	queued := state.Invocation{AppID: billingApp.ID, Headers: json.RawMessage(`{"X-Gregale-Release":"` + first.ID + `"}`)}
	forwarded, selected, err := state.ResolveInvocationVersion(ctx, s, queued)
	if err != nil || selected.ReleaseID != first.ID || selected.DeploymentID != billingV1.ID {
		t.Fatalf("old queued graph = %+v, %v", selected, err)
	}
	if string(forwarded.Headers) != string(queued.Headers) {
		t.Fatalf("queued release header changed: %s", forwarded.Headers)
	}
	if _, _, err := s.ResolveServiceRelease(ctx, apiApp.ID, apiV1.ID, billingApp.ID, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("ambiguous service graph = %v, want conflict", err)
	}
	var pinCoversGraph bool
	if err := pool.QueryRow(ctx, `select p.expires_at >= rs.expires_at from deployment_revision_pins p
		join project_release_sets rs on rs.id = $2 where p.deployment_id = $1`, billingV1.ID, first.ID).Scan(&pinCoversGraph); err != nil || !pinCoversGraph {
		t.Fatalf("old pin covers retired graph = %v, %v", pinCoversGraph, err)
	}
	if _, err := pool.Exec(ctx, `update project_release_sets set expires_at = now() - interval '1 minute' where id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployment_revision_pins set expires_at = now() - interval '1 minute' where deployment_id = $1`, billingV1.ID); err != nil {
		t.Fatal(err)
	}
	if count, err := s.ExpireRevisionPins(ctx); err != nil || count != 1 {
		t.Fatalf("expired retired member sweep: count %d err %v", count, err)
	}
	if _, _, err := s.ResolveProjectRelease(ctx, billingApp.ID, "production", first.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired graph lookup = %v, want not found", err)
	}
	if _, _, err := state.ResolveInvocationVersion(ctx, s, queued); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired queued release = %v, want not found", err)
	}
	if releaseID, depID, err := s.ResolveServiceRelease(ctx, apiApp.ID, apiV1.ID, billingApp.ID, ""); err != nil || releaseID != second.ID || depID != billingV2.ID {
		t.Fatalf("remaining service graph = %q/%q, %v", releaseID, depID, err)
	}
	// Prepare an incompatible revision with zero weighted traffic, then
	// activate it only through the release graph. The old weighted route
	// remains unchanged while clients move to the new release ID.
	dark, err := s.CreateDeployment(ctx, state.Deployment{AppID: billingApp.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:graph-billing-v3", Status: state.DeployPending, Scope: "production",
		TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, dark.ID); err != nil {
		t.Fatal(err)
	}
	if weighted, err := s.LiveDeploymentForScope(ctx, billingApp.ID, "production"); err != nil || weighted.ID != billingV2.ID {
		t.Fatalf("dark deploy changed weighted route: %+v, %v", weighted, err)
	}
	third, err := s.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{
		{AppID: apiApp.ID, DeploymentID: apiV1.ID}, {AppID: billingApp.ID, DeploymentID: dark.ID},
	})
	if err != nil {
		t.Fatalf("publish dark graph: %v", err)
	}
	if releaseID, depID, err := s.ResolveProjectRelease(ctx, billingApp.ID, "production", ""); err != nil || releaseID != third.ID || depID != dark.ID {
		t.Fatalf("active dark graph = %q/%q, %v", releaseID, depID, err)
	}
	if releaseID, depID, err := s.ResolveServiceRelease(ctx, apiApp.ID, apiV1.ID, billingApp.ID, third.ID); err != nil || releaseID != third.ID || depID != dark.ID {
		t.Fatalf("dark graph service = %q/%q, %v", releaseID, depID, err)
	}
}

func TestPgLiveInstancesByHostIPRejectsAmbiguityAndSeesReuse(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	_, oldApp, oldDep := seedLiveDeploy(t, s, ctx, "identity-old", "identity-old")
	_, newApp, newDep := seedLiveDeploy(t, s, ctx, "identity-new", "identity-new")
	nodeID := resolveDefaultLocal(t, ctx, s)
	old, err := s.CreateInstance(ctx, oldApp, oldDep, string(state.StateRunning), 128, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetInstanceRuntime(ctx, old.ID, "fc-old", "10.100.0.5", 20005); err != nil {
		t.Fatal(err)
	}
	lookup := func() []state.Instance {
		t.Helper()
		instances, err := s.LiveInstancesByHostIP(ctx, state.DefaultLocalNodeName, "10.100.0.5")
		if err != nil {
			t.Fatal(err)
		}
		return instances
	}
	if instances := lookup(); len(instances) != 1 || instances[0].AppID != oldApp || instances[0].DeploymentID != oldDep {
		t.Fatalf("initial identity = %+v", instances)
	}
	newInstance, err := s.CreateInstance(ctx, newApp, newDep, string(state.StateRunning), 128, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetInstanceRuntime(ctx, newInstance.ID, "fc-new", "10.100.0.5", 20006); err != nil {
		t.Fatal(err)
	}
	if instances := lookup(); len(instances) != 2 {
		t.Fatalf("ambiguous identity = %+v", instances)
	}
	if _, err := pool.Exec(ctx, `update instances set state = 'stopped' where id = $1`, old.ID); err != nil {
		t.Fatal(err)
	}
	if instances := lookup(); len(instances) != 1 || instances[0].AppID != newApp || instances[0].DeploymentID != newDep {
		t.Fatalf("reused IP identity = %+v", instances)
	}
}
