// adr: 375
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemRuntimeInstancePublicationOwnership(t *testing.T) {
	testRuntimeInstancePublicationOwnership(t, state.NewMemStore())
}

func seedRuntimeInstancePublication(t *testing.T, store runtimeAppEnvTestStore, f runtimeAppEnvFixture, dep state.Deployment, node string) state.RuntimeInstancePublication {
	t.Helper()
	instance, err := store.CreateInstance(t.Context(), f.app.ID, dep.ID, string(state.StateColdBooting), 256, node, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	return state.RuntimeInstancePublication{AccountID: f.account.ID, AppID: f.app.ID, InstanceID: instance.ID, NodeID: instance.NodeID, WakeID: instance.WakeID,
		ExpectedState: instance.State, Netns: "fc-" + instance.ID, HostIP: "10.100.0.8", GuestUID: 20008,
		Fence: runtimeSecretFenceForTest(t, store, f, dep)}
}

func assertRuntimePublicationUnchanged(t *testing.T, store state.Store, p state.RuntimeInstancePublication) {
	t.Helper()
	row, err := store.InstanceByID(t.Context(), p.InstanceID)
	if err != nil || row.State != p.ExpectedState || row.Netns != "" || row.HostIP != "" || row.GuestUID != 0 {
		t.Fatalf("rejected publication changed provisional instance: %+v %v", row, err)
	}
}

func testRuntimeInstancePublicationOwnership(t *testing.T, store runtimeAppEnvTestStore) {
	ctx := t.Context()
	f := seedRuntimeAppEnv(t, store)
	node := runtimeSecretNodeForTest(t, store)
	p := seedRuntimeInstancePublication(t, store, f, f.deployments["stage"], node)
	for _, change := range []struct {
		name   string
		mutate func(*state.RuntimeInstancePublication)
	}{
		{"wake", func(p *state.RuntimeInstancePublication) { p.WakeID = uuid.NewString() }},
		{"node", func(p *state.RuntimeInstancePublication) { p.NodeID = uuid.NewString() }},
		{"account", func(p *state.RuntimeInstancePublication) { p.AccountID = uuid.NewString() }},
		{"deployment", func(p *state.RuntimeInstancePublication) { p.Fence.DeploymentID = f.deployments["production"].ID }},
		{"environment", func(p *state.RuntimeInstancePublication) { p.Fence.EnvironmentID = uuid.NewString() }},
		{"scope", func(p *state.RuntimeInstancePublication) { p.Fence.Scope = "production" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			forged := p
			change.mutate(&forged)
			if _, err := store.PublishOwnedInstanceRuntime(ctx, forged); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("foreign publication: %v", err)
			}
			assertRuntimePublicationUnchanged(t, store, p)
		})
	}
	missing := p
	missing.Fence = state.RuntimeAppSecretFence{}
	if _, err := store.PublishOwnedInstanceRuntime(ctx, missing); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("missing original owner fence: %v", err)
	}
	// Editing the desired head cannot replace the pin of an admitted boot.
	head, err := store.ProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings := head.Settings
	settings.MaxConcurrency = 4
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, head.Revision, settings); err != nil {
		t.Fatal(err)
	}
	ready := time.Now().Add(-time.Second).UTC()
	if err := store.SetInstanceFrameworkReadyAt(ctx, p.InstanceID, ready); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TouchInstancesWithRequestDelta(ctx, []state.InstanceTouch{{InstanceID: p.InstanceID, LastRequest: ready, RequestDelta: 3}}); err != nil {
		t.Fatal(err)
	}
	row, err := store.PublishOwnedInstanceRuntime(ctx, p)
	if err != nil || row.State != string(state.StateRunning) || row.Netns != p.Netns || row.HostIP != p.HostIP || row.GuestUID != p.GuestUID ||
		row.DeploymentID != p.Fence.DeploymentID || row.NodeID != p.NodeID || row.WakeID != p.WakeID || row.StartedAt.IsZero() || row.FrameworkReadyAt == nil || row.RequestCount != 3 {
		t.Fatalf("owned publication lost identity or observations: %+v %v", row, err)
	}
	if _, err := store.PublishOwnedInstanceRuntime(ctx, p); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("replayed publication: %v", err)
	}
	// A boot that captured no secrets still detects a newly added input.
	rotation := seedRuntimeInstancePublication(t, store, f, f.deployments["other"], node)
	if err := store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "other", "TOKEN", []byte("new-secret")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishOwnedInstanceRuntime(ctx, rotation); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed sealed input publication: %v", err)
	}
	assertRuntimePublicationUnchanged(t, store, rotation)
}

func TestMemRuntimeInstancePublicationLifetime(t *testing.T) {
	testRuntimeInstancePublicationLifetime(t, state.NewMemStore())
}

func testRuntimeInstancePublicationLifetime(t *testing.T, store runtimeAppEnvTestStore) {
	ctx := t.Context()
	f := seedRuntimeAppEnv(t, store)
	node := runtimeSecretNodeForTest(t, store)
	original := seedRuntimeInstancePublication(t, store, f, f.deployments["stage"], node)
	production := seedRuntimeInstancePublication(t, store, f, f.deployments["production"], node)
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "stage"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishOwnedInstanceRuntime(ctx, original); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("original boot adopted replacement environment: %v", err)
	}
	assertRuntimePublicationUnchanged(t, store, original)
	if _, err := store.PublishOwnedInstanceRuntime(ctx, production); err != nil {
		t.Fatalf("stage deletion blocked production: %v", err)
	}
	replacement, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	p := seedRuntimeInstancePublication(t, store, f, replacement, node)
	if p.Fence.EnvironmentID == original.Fence.EnvironmentID {
		t.Fatal("replacement reused original ownership")
	}
	if _, err := store.PublishOwnedInstanceRuntime(ctx, p); err != nil {
		t.Fatalf("replacement boot: %v", err)
	}
}
