package builderd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestCopySourceBoundedRejectsOversizedStream(t *testing.T) {
	var got bytes.Buffer
	n, err := copySourceBounded(&got, strings.NewReader("12345"), 0, 4)
	if !errors.Is(err, ErrSourceBoundary) {
		t.Fatalf("copySourceBounded error = %v, want ErrSourceBoundary", err)
	}
	if n != 4 {
		t.Errorf("copied bytes = %d, want 4", n)
	}
	if got.String() != "1234" {
		t.Errorf("copied data = %q, want %q", got.String(), "1234")
	}
}

func TestValidateSourceFileRejectsEscapeSymlinkAndSizeMismatch(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "nested", "source.tar.gz")
	if err := os.MkdirAll(filepath.Dir(inside), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("1234"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := validateSourceFile(inside, root, 4, 8); err != nil {
		t.Fatalf("valid source: %v", err)
	}

	if _, err := validateSourceFile(filepath.Join(root, "..", "outside.tar.gz"), root, 0, 8); !errors.Is(err, ErrSourceBoundary) {
		t.Fatalf("path escape error = %v, want ErrSourceBoundary", err)
	}
	if _, err := validateSourceFile(inside, root, 5, 8); !errors.Is(err, ErrSourceBoundary) {
		t.Fatalf("size mismatch error = %v, want ErrSourceBoundary", err)
	}

	target := filepath.Join(t.TempDir(), "target.tar.gz")
	if err := os.WriteFile(target, []byte("1234"), 0o640); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "link.tar.gz")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := validateSourceFile(symlink, root, 4, 8); !errors.Is(err, ErrSourceBoundary) {
		t.Fatalf("symlink error = %v, want ErrSourceBoundary", err)
	}
}

func TestMaterializeSourceRejectsOversizedStream(t *testing.T) {
	storageRoot := t.TempDir()
	be, err := storage.NewLocalStorageBackend(storageRoot)
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	buildID := "550e8400-e29b-41d4-a716-446655440000"
	if err := be.Put(context.Background(), "sources/"+buildID+".tar.gz", strings.NewReader("12345")); err != nil {
		t.Fatalf("Put source: %v", err)
	}
	root := t.TempDir()
	dst := filepath.Join(root, buildID+".tar.gz")
	b := New(state.NewMemStore(), nil, nil, NewCache(t.TempDir()), NewDetector(), nil,
		Config{SourceSpoolDir: root}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithSourceStorage(be)
	if err := b.materializeSourceBounded(context.Background(), buildID, dst, 0, 4); !errors.Is(err, ErrSourceBoundary) {
		t.Fatalf("materializeSourceBounded error = %v, want ErrSourceBoundary", err)
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized destination stat = %v, want not exist", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary source files left behind: %v", entries)
	}
}

func TestAppendLogBoundedRejectsEscapeSymlinkAndCaps(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "logs@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "logs", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "deployment", "build.log")
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: filepath.Join(root, "source.tar.gz"), SourceBytes: 4, LogPath: logPath})
	if err != nil {
		t.Fatal(err)
	}
	build, err := store.CreateBuild(ctx, dep.ID, state.DeploymentKindTarball, dep.SourceBytes, dep.LogPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := appendLogBounded(ctx, store, build.ID, "123456", root, 5); err != nil {
		t.Fatalf("appendLogBounded: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "12345" {
		t.Errorf("capped log = %q, want %q", data, "12345")
	}
	if err := appendLogBounded(ctx, store, build.ID, "more", root, 5); err != nil {
		t.Fatalf("append after cap: %v", err)
	}
	data, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 5 {
		t.Errorf("log size after cap = %d, want 5", len(data))
	}

	outside := filepath.Join(t.TempDir(), "outside.log")
	escapeDep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: filepath.Join(root, "escape-source.tar.gz"), SourceBytes: 4, LogPath: outside})
	if err != nil {
		t.Fatal(err)
	}
	escapeBuild, err := store.CreateBuild(ctx, escapeDep.ID, state.DeploymentKindTarball, escapeDep.SourceBytes, escapeDep.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := appendLogBounded(ctx, store, escapeBuild.ID, "escape", root, 5); !errors.Is(err, ErrSourceBoundary) {
		t.Fatalf("path escape log error = %v, want ErrSourceBoundary", err)
	}

	if err := os.Remove(logPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, logPath); err != nil {
		t.Fatal(err)
	}
	if err := appendLogBounded(ctx, store, build.ID, "symlink", root, 64); !errors.Is(err, ErrSourceBoundary) {
		t.Fatalf("symlink log error = %v, want ErrSourceBoundary", err)
	}
}
