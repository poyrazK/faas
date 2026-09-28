package imaged

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Hides the cache listing capability: cleanup must use recorded keys even
// when none of the compute-node blobs are cached on the control plane.
type snapshotUnlistedBackend struct{ storage.StorageBackend }

func TestSnapshotPublicationRejectsRAMMismatchAndCleansCandidate(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "snapshot-ram@example.com", "pro")
	app, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "snapshot-ram", RAMMB: 256, MaxConcurrency: 3, IdleTimeoutS: 60})
	dep, _ := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage})
	be := mustLocalStorage(t, t.TempDir())
	h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithStorage(be)
	key := state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "wrong-ram")
	for _, part := range []string{key, state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})} {
		if err := be.Put(ctx, part, strings.NewReader(part)); err != nil {
			t.Fatal(err)
		}
	}

	err := h.handleSnapshotWritten(ctx, snapshotWrittenPayload{
		DeploymentID: dep.ID,
		StorageKey:   key,
		FCVersion:    "1.10.0",
		Tier:         state.SnapshotTierInit,
		MemBytes:     128 << 20,
	})
	if err == nil || !strings.Contains(err.Error(), "snapshot RAM mismatch") {
		t.Fatalf("handleSnapshotWritten error = %v, want RAM mismatch", err)
	}
	if _, err := store.LatestSnapshot(ctx, dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("RAM-incompatible snapshot was published: %v", err)
	}
	for _, part := range []string{key, state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})} {
		rc, err := be.Get(ctx, part)
		if err == nil {
			_ = rc.Close()
			t.Fatalf("RAM-incompatible snapshot artifact remains: %s", part)
		}
		if !storage.IsNotFound(err) {
			t.Fatal(err)
		}
	}
}

func TestSnapshotPublicationConflictPreservesWinnerAndCleansCandidate(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "snapshot@example.com", "pro")
	app, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "snapshot-publish", RAMMB: 256, MaxConcurrency: 3, IdleTimeoutS: 60})
	dep, _ := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage})
	be := mustLocalStorage(t, t.TempDir())
	h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithStorage(snapshotUnlistedBackend{be})
	first, second := state.SnapshotCaptureMemKey(dep.ID, "init", "first"), state.SnapshotCaptureMemKey(dep.ID, "init", "second")
	for _, key := range []string{first, second} {
		for _, part := range []string{key, state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})} {
			if err := be.Put(ctx, part, strings.NewReader(part)); err != nil {
				t.Fatal(err)
			}
		}
		if err := h.handleSnapshotWritten(ctx, snapshotWrittenPayload{DeploymentID: dep.ID, StorageKey: key, FCVersion: "1.10.0", Tier: "init"}); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := store.LatestSnapshot(ctx, dep.ID)
	if err != nil || latest.StorageKey != first {
		t.Fatalf("winner changed: %+v %v", latest, err)
	}
	for _, key := range []string{second, state.SnapshotVMStateKey(state.Snapshot{StorageKey: second})} {
		rc, err := be.Get(ctx, key)
		if err == nil {
			_ = rc.Close()
			t.Fatalf("unused candidate remains: %s", key)
		}
		if !storage.IsNotFound(err) {
			t.Fatal(err)
		}
	}
	// App deletion must also remove generation keys when the local cache is empty.
	h.cleanupSnapshotCaptures(ctx, snapshotUnlistedBackend{be}, dep.ID)
	for _, key := range []string{first, state.SnapshotVMStateKey(latest)} {
		rc, err := be.Get(ctx, key)
		if err == nil {
			_ = rc.Close()
			t.Fatalf("recorded object left behind: %s", key)
		}
		if !storage.IsNotFound(err) {
			t.Fatal(err)
		}
	}
}

func TestGitHubSnapshotCannotPromoteAfterNewerDeploymentAccepted(t *testing.T) {
	checkGitDrivenSnapshotCannotPromoteAfterNewerDeploymentAccepted(t, state.DeploymentKindGitHub)
}

func TestPreviewSnapshotCannotPromoteAfterNewerDeploymentAccepted(t *testing.T) {
	checkGitDrivenSnapshotCannotPromoteAfterNewerDeploymentAccepted(t, state.DeploymentKindPreview)
}

func checkGitDrivenSnapshotCannotPromoteAfterNewerDeploymentAccepted(t *testing.T, kind state.DeploymentKind) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "github-snapshot-fence@example.test", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "github-snapshot-fence", RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	stable, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, stable.ID); err != nil {
		t.Fatal(err)
	}
	older, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: kind,
		CommitSHA: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, older.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: kind,
		CommitSHA: strings.Repeat("b", 40)}); err != nil {
		t.Fatal(err)
	}
	backend := mustLocalStorage(t, t.TempDir())
	notifier := &fakeNotifier{}
	h := New(store, notifier, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithStorage(backend)
	key := state.SnapshotCaptureMemKey(older.ID, state.SnapshotTierInit, "older")
	for _, part := range []string{key, state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})} {
		if err := backend.Put(ctx, part, strings.NewReader(part)); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.handleSnapshotWritten(ctx, snapshotWrittenPayload{DeploymentID: older.ID, StorageKey: key,
		FCVersion: "1.10.0", Tier: state.SnapshotTierInit}); err != nil {
		t.Fatalf("stale snapshot publication: %v", err)
	}
	oldRow, err := store.DeploymentByID(ctx, older.ID)
	if err != nil || oldRow.Status != state.DeploySuperseded {
		t.Fatalf("stale GitHub row = (%+v, %v)", oldRow, err)
	}
	stableRow, err := store.DeploymentByID(ctx, stable.ID)
	if err != nil || stableRow.Status != state.DeployLive {
		t.Fatalf("serving predecessor = (%+v, %v)", stableRow, err)
	}
	var supersededNotice bool
	for _, call := range notifier.calls {
		if strings.Contains(call.payload, `"kind":"candidate_route"`) {
			t.Fatalf("stale deployment route was published: %+v", call)
		}
		if strings.Contains(call.payload, `"status":"superseded"`) && strings.Contains(call.payload, older.ID) {
			supersededNotice = true
		}
	}
	if !supersededNotice {
		t.Fatalf("stale deployment did not notify its terminal status: %+v", notifier.calls)
	}
}
