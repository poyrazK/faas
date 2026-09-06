package imaged

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Hides the cache listing capability: cleanup must use recorded keys even
// when none of the compute-node blobs are cached on the control plane.
type snapshotUnlistedBackend struct{ storage.StorageBackend }

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
