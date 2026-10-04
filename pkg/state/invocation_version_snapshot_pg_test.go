//go:build !no_pg

// adr: 531
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type invocationVersionAfterApp struct {
	state.InvocationVersionReader
	after func()
}

func (s invocationVersionAfterApp) AppByID(ctx context.Context, id string) (state.App, error) {
	app, err := s.InvocationVersionReader.AppByID(ctx, id)
	if err == nil {
		s.after()
	}
	return app, err
}

type invocationVersionMutationStore struct {
	*state.PgStore
	after func()
}

func (s invocationVersionMutationStore) WithInvocationVersionSnapshot(ctx context.Context, read func(state.InvocationVersionReader) error) error {
	return s.PgStore.WithInvocationVersionSnapshot(ctx, func(reader state.InvocationVersionReader) error {
		return read(invocationVersionAfterApp{InvocationVersionReader: reader, after: s.after})
	})
}

func TestPgInvocationVersionSnapshotRetainsCommittedReleaseView(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	account, err := store.CreateAccount(ctx, "invocation-view-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "inv-view-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "inv-view-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, RAMMB: 128, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	create := func(digest string) state.Deployment {
		t.Helper()
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage,
			ImageDigest: digest, Status: state.DeployPending, TrafficPercent: 100})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		return dep
	}
	publish := func(dep state.Deployment) state.ProjectReleaseSet {
		t.Helper()
		release, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800,
			[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}})
		if err != nil {
			t.Fatal(err)
		}
		return release
	}
	old := create("sha256:inv-view-old")
	first := publish(old)
	for _, pin := range []string{strings.ReplaceAll(first.ID, "-", ""), strings.ToUpper(first.ID), "urn:uuid:" + first.ID, "{" + first.ID + "}"} {
		request := state.Invocation{AppID: app.ID, AccountID: account.ID, Headers: json.RawMessage(`{"X-Gregale-Release":"` + pin + `"}`)}
		_, selected, err := state.ResolveInvocationVersion(ctx, store, request)
		if err != nil || selected.ReleaseID != first.ID || selected.DeploymentID != old.ID {
			t.Fatalf("release spelling %q: %+v %v", pin, selected, err)
		}
	}
	var next state.Deployment
	var second state.ProjectReleaseSet
	mutating := invocationVersionMutationStore{PgStore: store, after: func() {
		next = create("sha256:inv-view-next")
		second = publish(next)
		if _, err := pool.Exec(ctx, `UPDATE project_release_sets SET expires_at=now()-interval '1 minute' WHERE id=$1 AND NOT active`, first.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE deployment_revision_pins SET expires_at=now()-interval '1 minute' WHERE deployment_id=$1`, old.ID); err != nil {
			t.Fatal(err)
		}
	}}
	inv := state.Invocation{AppID: app.ID, AccountID: account.ID}
	prepared, selected, err := state.ResolveInvocationVersion(ctx, mutating, inv)
	if err != nil || selected.ReleaseID != first.ID || selected.DeploymentID != old.ID || selected.Scope != "production" {
		t.Fatalf("mixed committed release view: %+v %v", selected, err)
	}
	if _, _, err := state.ResolveInvocationVersion(ctx, store, prepared); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired saved graph fell through: %v", err)
	}
	_, fresh, err := state.ResolveInvocationVersion(ctx, store, inv)
	if err != nil || fresh.ReleaseID != second.ID || fresh.DeploymentID != next.ID {
		t.Fatalf("new committed graph: %+v %v", fresh, err)
	}
	if got := pool.Stat().AcquiredConns(); got != 0 {
		t.Fatalf("snapshot retained %d connections after resolution", got)
	}
}

func TestPgInvocationVersionSnapshotRevisionOwnerAndProjection(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	account, err := store.CreateAccount(ctx, "invocation-revision-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "inv-rev-" + uuid.NewString()[:8], Type: state.AppTypeApp,
		RAMMB: 128, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600, Env: map[string]string{"SECRET": "not-version-input"}}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "default", Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:inv-revision", Status: state.DeployPending, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.WithInvocationVersionSnapshot(ctx, func(reader state.InvocationVersionReader) error {
		projected, err := reader.AppByID(ctx, app.ID)
		want := state.App{ID: app.ID, AccountID: app.AccountID, Status: app.Status}
		if err != nil || !reflect.DeepEqual(projected, want) {
			t.Fatalf("projection includes unrelated metadata: %+v %v", projected, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	inv := state.Invocation{AppID: app.ID, AccountID: account.ID, Headers: json.RawMessage(`{"X-Gregale-Revision":"` + dep.ID + `"}`)}
	for _, pin := range []string{strings.ReplaceAll(dep.ID, "-", ""), strings.ToUpper(dep.ID), "urn:uuid:" + dep.ID, "{" + dep.ID + "}"} {
		t.Run(pin, func(t *testing.T) {
			request := inv
			request.Headers = json.RawMessage(`{"X-Gregale-Revision":"` + pin + `"}`)
			prepared, selected, err := state.ResolveInvocationVersion(ctx, store, request)
			if err != nil || selected.DeploymentID != dep.ID || string(prepared.Headers) != string(inv.Headers) {
				t.Fatalf("noncanonical deployment identity: %+v headers=%s err=%v", selected, prepared.Headers, err)
			}
		})
	}
	mutating := invocationVersionMutationStore{PgStore: store, after: func() {
		manifest := app.Manifest
		manifest.RevisionPinTTLSeconds = 0
		if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
			t.Fatal(err)
		}
	}}
	prepared, selected, err := state.ResolveInvocationVersion(ctx, mutating, inv)
	if err != nil || selected.DeploymentID != dep.ID {
		t.Fatalf("mixed direct revision view: %+v %v", selected, err)
	}
	if _, _, err := state.ResolveInvocationVersion(ctx, store, prepared); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("disabled revision accepted at delivery: %v", err)
	}
	for _, kind := range []string{"foreign account", "deleted status", "deleted timestamp"} {
		t.Run(kind, func(t *testing.T) {
			request := state.Invocation{AppID: app.ID, AccountID: account.ID}
			if _, err := pool.Exec(ctx, `UPDATE apps SET status='active', deleted_at=NULL WHERE id=$1`, app.ID); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "foreign account":
				request.AccountID = uuid.NewString()
			case "deleted status":
				if _, err := pool.Exec(ctx, `UPDATE apps SET status='deleted' WHERE id=$1`, app.ID); err != nil {
					t.Fatal(err)
				}
			case "deleted timestamp":
				if _, err := pool.Exec(ctx, `UPDATE apps SET deleted_at=now() WHERE id=$1`, app.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := state.ResolveInvocationVersion(ctx, store, request); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("unsafe invocation accepted: %v", err)
			}
		})
	}
	if err := store.WithInvocationVersionSnapshot(ctx, nil); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("nil snapshot callback = %v", err)
	}
	failure := errors.New("consumer refused")
	if err := store.WithInvocationVersionSnapshot(ctx, func(state.InvocationVersionReader) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("callback refusal = %v", err)
	}
	if got := pool.Stat().AcquiredConns(); got != 0 {
		t.Fatalf("failed snapshot retained %d connections", got)
	}
}

func TestPgInvocationDispatchSnapshotTargetMembership(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	account, app, dep := seedLiveDeploy(t, store, ctx, uuid.NewString(), uuid.NewString()[:8])
	node := resolveDefaultLocal(t, ctx, store)
	instance, err := store.CreateInstance(ctx, app, dep, string(state.StateRunning), 128, node, "")
	if err != nil {
		t.Fatal(err)
	}
	foreignAccount, foreignApp, foreignDep := seedLiveDeploy(t, store, ctx, uuid.NewString(), uuid.NewString()[:8])
	foreignInstance, err := store.CreateInstance(ctx, foreignApp, foreignDep, string(state.StateRunning), 128, node, "")
	if err != nil {
		t.Fatal(err)
	}
	claim := state.InvocationTarget{InstanceID: instance.ID, NodeID: node, DeploymentID: dep}
	inv := state.Invocation{AppID: app, AccountID: account}
	for _, kind := range []string{"valid", "legacy account", "missing instance", "wrong node", "wrong deployment", "foreign instance", "foreign account", "stopped", "superseded", "scope", "deleted deployment"} {
		t.Run(kind, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `UPDATE instances SET state='running' WHERE id=$1`, instance.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE deployments SET status='live', scope='default', deleted_at=NULL WHERE id=$1`, dep); err != nil {
				t.Fatal(err)
			}
			request, target := inv, claim
			switch kind {
			case "legacy account":
				request.AccountID = ""
			case "missing instance":
				target.InstanceID = uuid.NewString()
			case "wrong node":
				target.NodeID = uuid.NewString()
			case "wrong deployment":
				target.DeploymentID = foreignDep
			case "foreign instance":
				target.InstanceID, target.DeploymentID = foreignInstance.ID, foreignDep
			case "foreign account":
				request.AccountID = foreignAccount
			case "stopped":
				if err := store.UpdateInstanceState(ctx, instance.ID, string(state.StateStopped)); err != nil {
					t.Fatal(err)
				}
			case "superseded":
				if err := store.UpdateDeploymentStatus(ctx, dep, state.DeploySuperseded, ""); err != nil {
					t.Fatal(err)
				}
			case "scope":
				if _, err := pool.Exec(ctx, `UPDATE deployments SET scope='staging' WHERE id=$1`, dep); err != nil {
					t.Fatal(err)
				}
			case "deleted deployment":
				if _, err := pool.Exec(ctx, `UPDATE deployments SET deleted_at=now() WHERE id=$1`, dep); err != nil {
					t.Fatal(err)
				}
			}
			prepared, version, owner, err := state.ResolveInvocationDispatch(ctx, store, request, &target)
			if kind == "valid" || kind == "legacy account" {
				if err != nil || owner != account || version.Scope != "default" || !reflect.DeepEqual(prepared, request) {
					t.Fatalf("valid dispatch: %+v %+v %q %v", prepared, version, owner, err)
				}
			} else if !errors.Is(err, state.ErrNotFound) || owner != "" || version != (state.InvocationVersion{}) || !reflect.DeepEqual(prepared, request) {
				t.Fatalf("unsafe dispatch escaped snapshot: %+v %+v %q %v", prepared, version, owner, err)
			}
		})
	}
	if _, err := pool.Exec(ctx, `UPDATE deployments SET status='live', scope='default', deleted_at=NULL WHERE id=$1`, dep); err != nil {
		t.Fatal(err)
	}
	mutating := invocationVersionMutationStore{PgStore: store, after: func() {
		if err := store.UpdateInstanceState(ctx, instance.ID, string(state.StateStopped)); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(ctx, dep, state.DeploySuperseded, ""); err != nil {
			t.Fatal(err)
		}
	}}
	if _, _, owner, err := state.ResolveInvocationDispatch(ctx, mutating, inv, &claim); err != nil || owner != account {
		t.Fatalf("mixed target snapshot: owner=%q err=%v", owner, err)
	}
	if _, _, owner, err := state.ResolveInvocationDispatch(ctx, store, inv, &claim); !errors.Is(err, state.ErrNotFound) || owner != "" {
		t.Fatalf("fresh target view: owner=%q err=%v", owner, err)
	}
	if got := pool.Stat().AcquiredConns(); got != 0 {
		t.Fatalf("target snapshot retained %d connections", got)
	}
}
