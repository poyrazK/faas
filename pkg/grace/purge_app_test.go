package grace_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// PurgeApp is the owner's early purge (gregale app purge): once the store has
// closed the restore window, it removes the app and its artifacts at once, on
// the sweep's path, and the owner can create the slug again.
func TestPurgeAppRemovesAnAppWhoseWindowTheOwnerClosed(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "app-purge@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "compose-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{ID: "purge-now-deployment", AppID: app.ID, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	const key = "apps/compose-api/rootfs.ext4"
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
	if _, err := store.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	g := grace.New(grace.Params{
		Store: store, Artifacts: backend, Registry: prometheus.NewRegistry(), Now: time.Now,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	// Inside the window nothing is purged: the owner has not closed it.
	if err := g.PurgeApp(ctx, app.ID, "customer:"+account.ID); !errors.Is(err, grace.ErrPurgeNotExpired) {
		t.Fatalf("PurgeApp inside the window = %v, want ErrPurgeNotExpired", err)
	}
	if _, err := store.AppByID(ctx, app.ID); err != nil {
		t.Fatalf("app gone inside its window: %v", err)
	}

	if _, err := store.EndAppDeleteGrace(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := g.PurgeApp(ctx, app.ID, "customer:"+account.ID); err != nil {
		t.Fatalf("PurgeApp: %v", err)
	}
	if _, err := backend.Get(ctx, key); !storage.IsNotFound(err) {
		t.Fatalf("artifact Get error = %v, want storage.ErrNotFound", err)
	}
	if _, err := store.AppByID(ctx, app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("app remains after PurgeApp: %v", err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "compose-api", Status: state.AppActive}); err != nil {
		t.Fatalf("owner reusing the purged slug: %v", err)
	}
}
