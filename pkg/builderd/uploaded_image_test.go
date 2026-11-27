package builderd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/ociarchive/ociarchivetest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261127120000

func seedUploadedImage(t *testing.T, store state.Store, slug, digest string, archive []byte, signed bool) (string, string) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, slug+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5, RequireSigned: signed})
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "upload.part")
	if err := os.WriteFile(src, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	profile, err := json.Marshal(frameworkprofile.ForUploadedImage(digest, "local/app:latest", 0))
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindDockerfile, SourcePath: src, SourceBytes: int64(len(archive)),
		LogPath: filepath.Join(t.TempDir(), "build.log"), InferredProfile: profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	build, err := store.CreateBuild(ctx, dep.ID, state.DeploymentKindDockerfile, int64(len(archive)), dep.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	return build.ID, dep.ID
}

func TestProcessOne_UploadedImageSkipsVM(t *testing.T) {
	store := state.NewMemStore()
	archive, digest := ociarchivetest.Canonical(t, 3000)
	buildID, depID := seedUploadedImage(t, store, "local-img", digest, archive, false)
	exports := t.TempDir()
	fvm := &fakeVM{}
	b := New(store, &fakeNotifier{}, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{ImageExportDir: exports}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := b.ProcessOne(context.Background(), buildID); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if fvm.spawnCalls != 0 {
		t.Fatalf("an uploaded image spawned %d builder VMs", fvm.spawnCalls)
	}
	build, _ := store.BuildByID(context.Background(), buildID)
	if build.Status != state.BuildSucceeded {
		t.Fatalf("build status = %s", build.Status)
	}
	dep, _ := store.DeploymentByID(context.Background(), depID)
	want := filepath.Join(exports, buildID, "build", "out", "image.tar")
	if dep.RootfsPath != want {
		t.Fatalf("rootfs_path = %q, want the canonical export %q", dep.RootfsPath, want)
	}
	got, err := os.ReadFile(want)
	if err != nil || string(got) != string(archive) {
		t.Fatalf("export differs from the upload (err=%v)", err)
	}
}

// builderd never signs bytes it did not build: an uploaded image completes
// without a build publication even when a publisher is configured.
func TestProcessOne_UploadedImageIsNeverSigned(t *testing.T) {
	store := state.NewMemStore()
	archive, digest := ociarchivetest.Canonical(t, 0)
	buildID, depID := seedUploadedImage(t, store, "unsigned", digest, archive, false)
	signer, _ := buildPublicationSigner(t)
	b := New(store, &fakeNotifier{}, &fakeVM{}, NewCache(t.TempDir()), NewDetector(), nil, Config{ImageExportDir: t.TempDir(), BuilderNodeID: "builder-one"}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithBuildPublisher(signer)
	if _, err := b.ProcessOne(context.Background(), buildID); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	dep, _ := store.DeploymentByID(context.Background(), depID)
	app, _ := store.AppByID(context.Background(), dep.AppID)
	if present, err := store.HasBuildExportPublication(context.Background(), app.AccountID, app.ID, depID); err != nil || present {
		t.Fatalf("uploaded image publication present=%v err=%v", present, err)
	}
}

func TestProcessOne_UploadedImageRefusals(t *testing.T) {
	archive, digest := ociarchivetest.Canonical(t, 0)
	cases := map[string]struct {
		digest  string
		signed  bool
		exports bool
		want    string
	}{
		"digest changed":     {"sha256:" + strings.Repeat("0", 64), false, true, "not the accepted"},
		"signed app":         {digest, true, true, "requires signed images"},
		"export dir missing": {digest, false, false, "not configured"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := state.NewMemStore()
			buildID, depID := seedUploadedImage(t, store, "refuse", tc.digest, archive, tc.signed)
			cfg := Config{}
			if tc.exports {
				cfg.ImageExportDir = t.TempDir()
			}
			fvm := &fakeVM{}
			b := New(store, &fakeNotifier{}, fvm, NewCache(t.TempDir()), NewDetector(), nil, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
			_, _ = b.ProcessOne(context.Background(), buildID)
			build, _ := store.BuildByID(context.Background(), buildID)
			dep, _ := store.DeploymentByID(context.Background(), depID)
			log, _ := os.ReadFile(dep.LogPath)
			if build.Status != state.BuildFailed || !strings.Contains(string(log), tc.want) || fvm.spawnCalls != 0 {
				t.Fatalf("status=%s log=%q spawns=%d, want failed with %q", build.Status, log, fvm.spawnCalls, tc.want)
			}
		})
	}
}
