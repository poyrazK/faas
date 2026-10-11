package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devpatch"
	"github.com/onebox-faas/faas/pkg/sourcedelta"
	"github.com/onebox-faas/faas/pkg/state"
)

// writeDevSourceArchive writes a complete developer source archive beneath
// the test spool root, where openDevSourceArchive accepts it.
func writeDevSourceArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	f, err := os.CreateTemp(spoolRoot(), "source-*.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

type devPatchFixture struct {
	e      testEnv
	store  *state.MemStore
	app    state.App
	liveID string
	limits api.Limits
}

func newDevPatchFixture(t *testing.T, base map[string]string, withManifest bool) devPatchFixture {
	t.Helper()
	e := setup(t, api.PlanPro)
	t.Setenv(sourceSpoolRootEnv, t.TempDir())
	ctx := context.Background()
	store := e.store
	app, err := store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "dev-patch-live", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	live, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, BuildID: "build-live", Kind: state.DeploymentKindTarball,
		Status: state.DeployLive, ImageDigest: appTaskTestDigest, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateBuildProvenance(ctx, state.BuildProvenance{BuildID: "build-live", DevPatch: &api.DevPatchSourceMap{
		Version: 1, Verbatim: true, ImageDir: "/app", RebuildPaths: []string{"package.json"}}}); err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	if withManifest {
		archive, err := openDevSourceArchive(writeDevSourceArchive(t, base))
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := sourcedelta.Inspect(archive, sourceDeltaLimits(limits))
		_ = archive.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RecordDevSourceManifest(ctx, state.DevSourceManifest{DeploymentID: live.ID, AppID: app.ID,
			Entries: devPatchEntriesToState(manifest)}, devSourceManifestsKept); err != nil {
			t.Fatal(err)
		}
	}
	return devPatchFixture{e: e, store: store, app: app, liveID: live.ID, limits: limits}
}

var devPatchBaseSource = map[string]string{"package.json": "{}", "src/app.js": "v1", "src/old.js": "old"}

func TestDevPatchPlanPreview(t *testing.T) {
	cases := []struct {
		name         string
		withManifest bool
		current      map[string]string
		want         api.DevPatchPreview
	}{
		{name: "cumulative source edit", withManifest: true,
			current: map[string]string{"package.json": "{}", "src/app.js": "v2", "src/new.js": "n"},
			want:    api.DevPatchPreview{Eligible: true, ChangedPaths: 3, PatchBytes: 3}},
		{name: "manifest edit needs a build", withManifest: true,
			current: map[string]string{"package.json": `{"x":1}`, "src/app.js": "v1", "src/old.js": "old"},
			want:    api.DevPatchPreview{Reason: api.DevPatchReasonRebuildInput}},
		{name: "live build predates manifests", withManifest: false,
			current: devPatchBaseSource,
			want:    api.DevPatchPreview{Reason: api.DevPatchReasonNoBaseManifest}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDevPatchFixture(t, devPatchBaseSource, tc.withManifest)
			plan := f.e.s.planDevPatch(context.Background(), f.app, writeDevSourceArchive(t, tc.current), "", f.limits)
			if plan.preview == nil || *plan.preview != tc.want {
				t.Fatalf("preview = %+v, want %+v", plan.preview, tc.want)
			}
		})
	}
}

func TestDevPatchPlanWithoutLiveBuild(t *testing.T) {
	e := setup(t, api.PlanPro)
	t.Setenv(sourceSpoolRootEnv, t.TempDir())
	app, err := e.store.CreateApp(context.Background(), state.App{AccountID: e.acct.ID, Slug: "dev-patch-none", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	plan := e.s.planDevPatch(context.Background(), app, writeDevSourceArchive(t, devPatchBaseSource), "", api.MustLimitsFor(api.PlanPro))
	if plan.preview == nil || plan.preview.Reason != api.DevPatchReasonNoLiveBuild {
		t.Fatalf("preview = %+v, want no_live_build", plan.preview)
	}
}

func TestDevPatchPublishedOnlyWithDeliveryFlag(t *testing.T) {
	current := map[string]string{"package.json": "{}", "src/app.js": "v2", "src/new.js": "n"}
	for _, enabled := range []bool{false, true} {
		f := newDevPatchFixture(t, devPatchBaseSource, true)
		if enabled {
			t.Setenv(devPatchDeliveryEnv, "1")
		} else {
			t.Setenv(devPatchDeliveryEnv, "")
		}
		ctx := context.Background()
		sourcePath := writeDevSourceArchive(t, current)
		plan := f.e.s.planDevPatch(ctx, f.app, sourcePath, "", f.limits)
		newDeployment, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindTarball,
			Status: state.DeployPending, ImageDigest: appTaskTestDigest, CreatedAt: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		f.e.s.recordDevPatchSource(ctx, f.app, newDeployment.ID, sourcePath, "", plan)
		if _, err := f.store.DevSourceManifest(ctx, newDeployment.ID); err != nil {
			t.Fatalf("new deployment manifest not recorded: %v", err)
		}
		patch, err := f.store.LatestDevSourcePatch(ctx, f.app.ID, f.liveID, 0)
		if !enabled {
			if err == nil {
				t.Fatal("patch published without the delivery flag")
			}
			continue
		}
		if err != nil {
			t.Fatalf("patch not published: %v", err)
		}
		dir := t.TempDir()
		for name, body := range devPatchBaseSource {
			if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := devpatch.Apply(dir, patch.Archive, patch.Deleted, api.DevPatchMaxBytes); err != nil {
			t.Fatal(err)
		}
		for name, body := range current {
			if got, _ := os.ReadFile(filepath.Join(dir, name)); string(got) != body {
				t.Fatalf("%s after patch = %q, want %q", name, got, body)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "src", "old.js")); !os.IsNotExist(err) {
			t.Fatal("deleted file survived the patch")
		}
	}
}
