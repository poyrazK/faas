package grace_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Account erasure dropped the rows naming an account's rootfs layers and
// snapshots without deleting the objects: the apps were never tombstoned, so
// the app purge never ran for them (bug hunt 2026-10-28 B19).
func TestRunOnceDeletesAccountAppArtifactsBeforeErasure(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := seedAccount(t, store)
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "erased-artifacts", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{ID: "erased-artifact-deployment", AppID: app.ID, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	const key = "apps/erased-artifacts/rootfs.ext4"
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/unused/rootfs.ext4", key, 4); err != nil {
		t.Fatal(err)
	}
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Put(ctx, key, strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	p := params(store, &recordingSender{}, makeNotifier(&recordingNotifier{}), nowFrozen(time.Now().Add(31*24*time.Hour)), runOnceInterval)

	// Without an artifact backend the erasure waits rather than orphan them.
	if err := grace.New(p).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AccountByID(ctx, acct.ID); err != nil {
		t.Fatalf("account erased while its app artifacts could not be deleted: %v", err)
	}

	p.Artifacts = backend
	if err := grace.New(p).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Get(ctx, key); !storage.IsNotFound(err) {
		t.Fatalf("artifact Get error = %v, want storage.ErrNotFound", err)
	}
	if _, err := store.AccountByID(ctx, acct.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("account not erased: %v", err)
	}
}
