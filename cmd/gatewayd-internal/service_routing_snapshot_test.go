// adr: 531
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *serviceSnapshotAfterReadReader) ResolveServiceRelease(ctx context.Context, caller, source, target, requested string) (string, string, error) {
	return s.ServicePolicyReader.(state.ServiceRoutingPolicyReader).ResolveServiceRelease(ctx, caller, source, target, requested)
}
func (s *serviceSnapshotAfterReadReader) ServiceDeploymentOverrideAllowed(ctx context.Context, app, deployment string) (bool, error) {
	return s.ServicePolicyReader.(state.ServiceRoutingPolicyReader).ServiceDeploymentOverrideAllowed(ctx, app, deployment)
}
func (s *serviceSnapshotAfterReadReader) ServiceDeploymentWeights(ctx context.Context, app string) ([]state.ServiceDeploymentWeight, error) {
	return s.ServicePolicyReader.(state.ServiceRoutingPolicyReader).ServiceDeploymentWeights(ctx, app)
}

func TestServiceRoutingSnapshotPostgresRetainsReleaseViewAcrossCutover(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "service-routing-snapshot@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "service-routing"})
	if err != nil {
		t.Fatal(err)
	}
	createApp := func(slug string) state.App {
		t.Helper()
		app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: slug, WorkloadName: slug,
			Type: state.AppTypeApp, RAMMB: 128, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	caller, target := createApp("service-routing-client"), createApp("service-routing-target")
	createDeployment := func(app state.App, digest string) state.Deployment {
		t.Helper()
		deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
			Scope: "production", ImageDigest: digest, Status: state.DeployPending, TrafficPercent: 100})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(t.Context(), deployment.ID); err != nil {
			t.Fatal(err)
		}
		return deployment
	}
	source, old := createDeployment(caller, "sha256:source"), createDeployment(target, "sha256:old")
	publish := func(deployment string) state.ProjectReleaseSet {
		t.Helper()
		release, err := store.PublishProjectReleaseSet(t.Context(), account.ID, project.ID, "production", 1800,
			[]state.ProjectReleaseMember{{AppID: caller.ID, DeploymentID: source.ID}, {AppID: target.ID, DeploymentID: deployment}})
		if err != nil {
			t.Fatal(err)
		}
		return release
	}
	first := publish(old.ID)
	ctx := gateway.WithServicePolicyRoutingInputs(t.Context(), gateway.ServicePolicyRoutingInputs{
		CallerDeploymentID: source.ID, ResolveRelease: true, RequestedReleaseID: first.ID, ReleasePresent: true, ReleaseValid: true, Method: "GET", TargetPath: "/"})
	before, err := newServicePolicyPinner(store)(ctx, caller.ID, target.Slug, false)
	if err != nil || before.Routing == nil || before.Routing.ReleaseDeploymentID != old.ID {
		t.Fatalf("initial release = %+v %v", before.Routing, err)
	}
	var next state.Deployment
	var ambiguity *gateway.ServiceRoutingSnapshot
	pin := newServicePolicyPinner(serviceSnapshotAfterRead{ServicePolicySnapshotStore: store, after: func() {
		next = createDeployment(target, "sha256:next")
		publish(next.ID)
		implicitCtx := gateway.WithServicePolicyRoutingInputs(t.Context(), gateway.ServicePolicyRoutingInputs{CallerDeploymentID: source.ID, ResolveRelease: true, ReleaseValid: true, Method: "GET", TargetPath: "/"})
		ambiguous, err := newServicePolicyPinner(store)(implicitCtx, caller.ID, target.Slug, false)
		if err != nil {
			t.Fatal(err)
		}
		ambiguity = ambiguous.Routing
		if _, err := pool.Exec(t.Context(), `UPDATE project_release_sets SET expires_at=now()-interval '1 minute' WHERE id=$1 AND NOT active`, first.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), `UPDATE deployment_revision_pins SET expires_at=now()-interval '1 minute' WHERE deployment_id=$1`, old.ID); err != nil {
			t.Fatal(err)
		}
	}})
	during, err := pin(ctx, caller.ID, target.Slug, false)
	if err != nil || during.Routing == nil || during.Routing.ReleaseID != first.ID || during.Routing.ReleaseDeploymentID != old.ID || during.Routing.ReleaseError != nil {
		t.Fatalf("mixed release snapshot = %+v %v", during.Routing, err)
	}
	if ambiguity == nil || !errors.Is(ambiguity.ReleaseError, gateway.ErrReleaseConflict) || ambiguity.ReleaseVerdict != "conflict" {
		t.Fatalf("ambiguous source membership = %+v", ambiguity)
	}
	fresh, err := newServicePolicyPinner(store)(ctx, caller.ID, target.Slug, false)
	if err != nil || fresh.Routing == nil || !errors.Is(fresh.Routing.ReleaseError, gateway.ErrReleaseGone) || fresh.Routing.ReleaseVerdict != "gone" {
		t.Fatalf("fresh expired release = %+v %v", fresh.Routing, err)
	}
	weightCtx := gateway.WithServicePolicyRoutingInputs(t.Context(), gateway.ServicePolicyRoutingInputs{ReleaseValid: true, Method: "GET", TargetPath: "/"})
	weighted, err := newServicePolicyPinner(store)(weightCtx, caller.ID, target.Slug, false)
	if err != nil || weighted.Routing == nil || len(weighted.Routing.Weights) != 1 || weighted.Routing.Weights[0].ID != next.ID {
		t.Fatalf("fresh weight roster = %+v %v", weighted.Routing, err)
	}
	for _, tc := range []struct {
		name, deployment string
		want             bool
	}{{"current", next.ID, true}, {"expired", old.ID, false}, {"foreign", source.ID, false}, {"missing", uuid.NewString(), false}} {
		t.Run(tc.name, func(t *testing.T) {
			overrideCtx := gateway.WithServicePolicyRoutingInputs(t.Context(), gateway.ServicePolicyRoutingInputs{ReleaseValid: true, OverridePresent: true, OverrideValid: true, OverrideDeploymentID: tc.deployment, Method: "GET", TargetPath: "/"})
			policy, err := newServicePolicyPinner(store)(overrideCtx, caller.ID, target.Slug, false)
			if err != nil || policy.Routing == nil || !policy.Routing.OverrideChecked || policy.Routing.OverrideAllowed != tc.want {
				t.Fatalf("override = %+v %v", policy.Routing, err)
			}
		})
	}
	if pool.Stat().AcquiredConns() != 0 {
		t.Fatal("routing snapshot retained a transaction")
	}
}
