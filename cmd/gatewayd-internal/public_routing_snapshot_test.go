// adr: 570
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type publicSnapshotAfterOwner struct {
	state.PublicRoutingSnapshotStore
	after func()
}

func (s publicSnapshotAfterOwner) WithPublicRoutingSnapshot(ctx context.Context, read func(state.PublicRoutingPolicyReader) error) error {
	return s.PublicRoutingSnapshotStore.WithPublicRoutingSnapshot(ctx, func(reader state.PublicRoutingPolicyReader) error {
		return read(publicSnapshotAfterOwnerReader{PublicRoutingPolicyReader: reader, after: s.after})
	})
}

type publicSnapshotAfterOwnerReader struct {
	state.PublicRoutingPolicyReader
	after func()
}

func (s publicSnapshotAfterOwnerReader) HostPolicyReader() state.PublicHostPolicyReader {
	return s.PublicRoutingPolicyReader.(state.PublicRoutingHostPolicyReader).HostPolicyReader()
}

func (s publicSnapshotAfterOwnerReader) VerifyPublicRoutingOwner(ctx context.Context, app, account, project string) error {
	err := s.PublicRoutingPolicyReader.VerifyPublicRoutingOwner(ctx, app, account, project)
	if err == nil {
		s.after()
	}
	return err
}

type publicRoutingPGFixture struct {
	t       *testing.T
	pool    *pgxpool.Pool
	store   *state.PgStore
	app     state.App
	project state.Project
}

func newPublicRoutingPGFixture(t *testing.T) publicRoutingPGFixture {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "public-routing@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "public-routing"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "public-routing",
		Type: state.AppTypeApp, RAMMB: 128, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	return publicRoutingPGFixture{t: t, pool: pool, store: store, app: app, project: project}
}

func (f publicRoutingPGFixture) deployment(t *testing.T, scope, digest string) state.Deployment {
	t.Helper()
	deployment, err := f.store.CreateDeployment(t.Context(), state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindImage,
		Scope: scope, ImageDigest: digest, Status: state.DeployPending, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkDeploymentLive(t.Context(), deployment.ID); err != nil {
		t.Fatal(err)
	}
	return deployment
}

func (f publicRoutingPGFixture) publish(t *testing.T, deployment string) state.ProjectReleaseSet {
	t.Helper()
	release, err := f.store.PublishProjectReleaseSet(t.Context(), f.app.AccountID, f.project.ID, "production", 1800,
		[]state.ProjectReleaseMember{{AppID: f.app.ID, DeploymentID: deployment}})
	if err != nil {
		t.Fatal(err)
	}
	return release
}

func (f publicRoutingPGFixture) routingApp() gateway.App {
	f.t.Helper()
	app, found, err := (pgRouter{store: f.store, appsSuffix: ".apps.gregale.dev", tenantSurfacesEnabled: func() bool { return false }}).
		ResolveHost(f.t.Context(), f.app.Slug+".apps.gregale.dev")
	if err != nil || !found {
		f.t.Fatalf("routing app policy: found=%v err=%v", found, err)
	}
	return app
}

func TestPublicRoutingSnapshotPostgresRetainsOneViewAcrossCutover(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	old := f.deployment(t, "production", "sha256:old")
	first := f.publish(t, old.ID)
	inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "production"}
	pin := newPublicRoutingPinner(f.store)
	var next state.Deployment
	during, err := newPublicRoutingPinner(publicSnapshotAfterOwner{PublicRoutingSnapshotStore: f.store, after: func() {
		next = f.deployment(t, "production", "sha256:next")
		f.publish(t, next.ID)
	}})(t.Context(), f.routingApp(), inputs)
	if err != nil || len(during.Weights) != 1 || during.Weights[0].ID != old.ID {
		t.Fatalf("mixed weight snapshot: %+v %v", during, err)
	}
	fresh, err := pin(t.Context(), f.routingApp(), inputs)
	if err != nil || len(fresh.Weights) != 1 || fresh.Weights[0].ID != next.ID {
		t.Fatalf("fresh weight snapshot: %+v %v", fresh, err)
	}
	inputs.ResolveRelease, inputs.ReleasePresent, inputs.RequestedReleaseID = true, true, first.ID
	retained, err := pin(t.Context(), f.routingApp(), inputs)
	if err != nil || retained.ReleaseVerdict != "allowed" || retained.ReleaseDeploymentID != old.ID {
		t.Fatalf("retained graph: %+v %v", retained, err)
	}
	during, err = newPublicRoutingPinner(publicSnapshotAfterOwner{PublicRoutingSnapshotStore: f.store, after: func() {
		if _, err := f.pool.Exec(t.Context(), `UPDATE project_release_sets SET expires_at=now()-interval '1 minute' WHERE id=$1 AND NOT active`, first.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(t.Context(), `UPDATE deployment_revision_pins SET expires_at=now()-interval '1 minute' WHERE deployment_id=$1`, old.ID); err != nil {
			t.Fatal(err)
		}
	}})(t.Context(), f.routingApp(), inputs)
	if err != nil || during.ReleaseVerdict != "allowed" || during.ReleaseDeploymentID != old.ID {
		t.Fatalf("mixed release snapshot: %+v %v", during, err)
	}
	fresh, err = pin(t.Context(), f.routingApp(), inputs)
	if err != nil || fresh.ReleaseVerdict != "gone" {
		t.Fatalf("fresh expired graph: %+v %v", fresh, err)
	}
	inputs.RequestedReleaseID, inputs.ReleasePresent = "", false
	fresh, err = pin(t.Context(), f.routingApp(), inputs)
	if err != nil || fresh.ReleaseVerdict != "allowed" || fresh.ReleaseDeploymentID != next.ID {
		t.Fatalf("active graph: %+v %v", fresh, err)
	}
	stage := f.deployment(t, "staging", "sha256:staging")
	for _, tc := range []struct {
		name, deployment, scope string
		want                    bool
	}{{"current", next.ID, "production", true}, {"expired", old.ID, "production", false},
		{"foreign-scope", stage.ID, "production", false}, {"missing", uuid.NewString(), "production", false}} {
		t.Run(tc.name, func(t *testing.T) {
			revision := gateway.PublicRoutingInputs{Valid: true, Scope: tc.scope, RevisionPresent: true, RequestedRevisionID: tc.deployment}
			snapshot, err := pin(t.Context(), f.routingApp(), revision)
			if err != nil || !snapshot.RevisionChecked || snapshot.RevisionAllowed != tc.want {
				t.Fatalf("revision verdict: %+v %v", snapshot, err)
			}
		})
	}
	for _, tc := range []struct {
		deployment, scope string
		want              bool
	}{{old.ID, "production", true}, {stage.ID, "production", false}, {stage.ID, "staging", true}, {uuid.NewString(), "production", false}} {
		snapshot, err := pin(t.Context(), f.routingApp(), gateway.PublicRoutingInputs{Valid: true, Scope: tc.scope, HostDeploymentID: tc.deployment, HostScope: tc.scope})
		if err != nil || !snapshot.HostChecked || snapshot.HostAllowed != tc.want {
			t.Fatalf("host verdict: %+v %v", snapshot, err)
		}
	}
	inputs = gateway.PublicRoutingInputs{Valid: true, Scope: "staging"}
	snapshot, err := pin(t.Context(), f.routingApp(), inputs)
	if err != nil || len(snapshot.Weights) != 1 || snapshot.Weights[0].ID != stage.ID {
		t.Fatalf("scoped weights: %+v %v", snapshot, err)
	}
	foreign := f.routingApp()
	foreign.AccountID = uuid.NewString()
	if _, err := pin(t.Context(), foreign, inputs); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign owner accepted: %v", err)
	}
	foreign = f.routingApp()
	foreign.ProjectID = uuid.NewString()
	if _, err := pin(t.Context(), foreign, inputs); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign project accepted: %v", err)
	}
	if f.pool.Stat().AcquiredConns() != 0 {
		t.Fatal("public routing retained a transaction")
	}
}

func TestPublicRoutingSnapshotPostgresBoundsRosterAndIncompleteGraph(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	deployment := f.deployment(t, "production", "sha256:current")
	release := f.publish(t, deployment.ID)
	before := f.routingApp()
	if _, err := f.pool.Exec(t.Context(), `DELETE FROM project_release_members WHERE release_id=$1 AND app_id=$2`, release.ID, f.app.ID); err != nil {
		t.Fatal(err)
	}
	pin := newPublicRoutingPinner(f.store)
	inputs := gateway.PublicRoutingInputs{Valid: true, Scope: "production", ResolveRelease: true}
	if _, _, err := (pgRouter{store: f.store}).resolvePublicAppSlug(t.Context(), f.app.Slug); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("incomplete production graph resolved host settings: %v", err)
	}
	snapshot, err := pin(t.Context(), before, inputs)
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("incomplete graph accepted: %+v %v", snapshot, err)
	}
	if err := f.store.WithPublicRoutingSnapshot(t.Context(), func(reader state.PublicRoutingPolicyReader) error {
		_, _, err := reader.ResolvePublicProjectRelease(t.Context(), f.app.ID, "production", "")
		return err
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("incomplete graph was not refused at the release reader: %v", err)
	}
	// Restore a complete graph before independently exercising roster bounds.
	f.publish(t, deployment.ID)
	before = f.routingApp()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO deployments (id,app_id,kind,scope,image_digest,status,traffic_percent,traffic_percent_explicit)
		SELECT gen_random_uuid(),$1,'image','production','sha256:bounded-'||n,'live',1,true FROM generate_series(1,$2) n`, f.app.ID, api.TrafficPolicyMaxDeployments); err != nil {
		t.Fatal(err)
	}
	if _, err := pin(t.Context(), before, gateway.PublicRoutingInputs{Valid: true, Scope: "production"}); err == nil {
		t.Fatal("oversized roster was truncated into verified policy")
	}
	if err := f.store.WithPublicRoutingSnapshot(t.Context(), func(reader state.PublicRoutingPolicyReader) error {
		_, err := reader.PublicDeploymentWeights(t.Context(), f.app.ID, "production")
		return err
	}); err == nil {
		t.Fatal("oversized routing roster was not refused at the reader")
	}
	if _, _, err := (pgRouter{store: f.store}).resolvePublicAppSlug(t.Context(), f.app.Slug); err == nil {
		t.Fatal("oversized ingress roster was not refused")
	}
	if f.pool.Stat().AcquiredConns() != 0 {
		t.Fatal("refused routing retained a transaction")
	}
}
