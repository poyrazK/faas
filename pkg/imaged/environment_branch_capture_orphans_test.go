package imaged

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635 slice 5a: a credential-free branch capture lives under
// its parent deployment's snapshot directory and must survive the orphan
// sweep after the parent's own snapshot rows are gone.
func TestRemoveLocalSnapshotOrphansKeepsEnvironmentBranchCaptures(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "capture@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "web", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:capture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequestEnvironmentBranchCapture(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "snap", dep.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.Add(-2 * localSnapshotOrphanGrace)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	loop := &Loop{store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil)), storageRoot: root}
	loop.removeLocalSnapshotOrphans(ctx, now)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("branch capture directory removed: %v", err)
	}
}
