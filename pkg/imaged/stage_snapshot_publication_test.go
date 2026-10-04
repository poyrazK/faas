// adr: 581
package imaged

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestSnapshotPublicationRejectsRecreatedStageAndPreservesProductionArtifacts(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "stage-publication@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "stage-publication"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "stage-publication", RAMMB: 256, MaxConcurrency: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "stage", app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	stage, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	production, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateInstance(ctx, app.ID, stage.ID, string(state.StateParked), 256, "node", "")
	if err != nil {
		t.Fatal(err)
	}
	productionSnapshot, err := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: production.ID, FCVersion: "1.10", StorageKey: state.SnapshotCaptureMemKey(production.ID, state.SnapshotTierInit, "production")})
	if err != nil {
		t.Fatal(err)
	}
	candidate := state.Snapshot{DeploymentID: stage.ID, FCVersion: "1.10", StorageKey: state.SnapshotCaptureMemKey(stage.ID, state.SnapshotTierInit, "delayed")}
	parts := func(snapshot state.Snapshot) []string {
		return []string{snapshot.StorageKey, state.SnapshotVMStateKey(snapshot), state.SnapshotDriveKey(snapshot)}
	}
	backend := mustLocalStorage(t, t.TempDir())
	for _, snapshot := range []state.Snapshot{productionSnapshot, candidate} {
		for _, key := range parts(snapshot) {
			if err := backend.Put(ctx, key, strings.NewReader(key)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := store.MarkDeploymentSuperseded(ctx, stage.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "stage"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "stage", app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretInScope(ctx, account.ID, app.ID, "stage", "TOKEN", []byte("replacement-sealed")); err != nil {
		t.Fatal(err)
	}
	h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithStorage(backend)
	if err := h.handleSnapshotWritten(ctx, snapshotWrittenPayload{DeploymentID: stage.ID, SourceInstanceID: source.ID, SourceStartedAt: source.StartedAt, StorageKey: candidate.StorageKey, FCVersion: candidate.FCVersion, Tier: state.SnapshotTierInit}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("old stage adopted replacement snapshot policy: %v", err)
	}
	if _, err := store.LatestSnapshotForTier(ctx, stage.ID, state.SnapshotTierInit); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("old stage snapshot became restorable: %v", err)
	}
	got, err := store.LatestSnapshotForTier(ctx, production.ID, state.SnapshotTierInit)
	if err != nil || got.ID != productionSnapshot.ID || got.Stale {
		t.Fatalf("stage rejection changed production snapshot: %+v %v", got, err)
	}
	for _, key := range parts(productionSnapshot) {
		reader, err := backend.Get(ctx, key)
		if err != nil {
			t.Fatalf("stage rejection deleted production artifact %s: %v", key, err)
		}
		_ = reader.Close()
	}
	for _, key := range parts(candidate) {
		reader, err := backend.Get(ctx, key)
		if err == nil {
			_ = reader.Close()
			t.Fatalf("rejected stage artifact survived: %s", key)
		}
		if !storage.IsNotFound(err) {
			t.Fatal(err)
		}
	}
	secret, err := store.GetAppSecretInScope(ctx, account.ID, app.ID, "stage", "TOKEN")
	if err != nil || string(secret.Ciphertext) != "replacement-sealed" {
		t.Fatalf("rejection modified replacement secret: %+v %v", secret, err)
	}
}
