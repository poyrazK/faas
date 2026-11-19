package apidserver_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/devpatch"
	"github.com/onebox-faas/faas/pkg/state"
)

func wrapped(dir string, files map[string]string) map[string]string {
	out := make(map[string]string, len(files))
	for name, body := range files {
		out[dir+"/"+name] = body
	}
	return out
}

// The CLI packs a project under one wrapper directory. A patch planned
// against the raw archive kept that directory in its entry names, so
// guest-init wrote /app/<dir>/src/app.js and the app kept running the old
// file, and no rebuild path matched (prod 2026-11-18). Planned against the
// logical root, entries are image-relative and a manifest edit still needs a
// build.
func TestDevPatchPlansBelowTheCLIWrapperDirectory(t *testing.T) {
	cases := []struct {
		name    string
		current map[string]string
		want    api.DevPatchPreview
	}{
		{name: "source edit is patched at the image root",
			current: map[string]string{"package.json": "{}", "src/app.js": "v2", "src/old.js": "old"},
			want:    api.DevPatchPreview{Eligible: true, ChangedPaths: 1, PatchBytes: 2}},
		{name: "manifest edit still needs a build",
			current: map[string]string{"package.json": `{"x":1}`, "src/app.js": "v1", "src/old.js": "old"},
			want:    api.DevPatchPreview{Reason: api.DevPatchReasonRebuildInput}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDevPatchFixture(t, wrapped("proj", devPatchBaseSource), true)
			t.Setenv(apidserver.DevPatchDeliveryEnv, "1")
			ctx := context.Background()
			// The live build's manifest was recorded against the same
			// logical root, as recordDevPatchSource now does.
			base, err := f.store.DevSourceManifest(ctx, f.liveID)
			if err != nil {
				t.Fatal(err)
			}
			base.SourceRoot = "proj"
			if err := f.store.RecordDevSourceManifest(ctx, base, apidserver.DevSourceManifestsKept); err != nil {
				t.Fatal(err)
			}
			sourcePath := writeDevSourceArchive(t, wrapped("proj", tc.current))
			root, err := apidserver.ArchiveLogicalRoot(sourcePath, "")
			if err != nil || root != "proj" {
				t.Fatalf("logical root = %q, %v; want the wrapper directory", root, err)
			}
			plan := (*f.e.S()).PlanDevPatch(ctx, f.app, sourcePath, root, f.limits)
			preview := *plan.Preview()
			if preview == nil || *preview != tc.want {
				t.Fatalf("preview = %+v, want %+v", preview, tc.want)
			}
			if !tc.want.Eligible {
				return
			}
			dep, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindTarball,
				Status: state.DeployPending, ImageDigest: apidserver.AppTaskTestDigest, CreatedAt: time.Now().UTC()})
			if err != nil {
				t.Fatal(err)
			}
			if !(*f.e.S()).RecordDevPatchSource(ctx, f.app, dep.ID, sourcePath, root, plan) {
				t.Fatal("patch not published")
			}
			patch, err := f.store.LatestDevSourcePatch(ctx, f.app.ID, f.liveID, 0)
			if err != nil {
				t.Fatal(err)
			}
			image := t.TempDir() // the image directory holds the project without the wrapper
			for name, body := range devPatchBaseSource {
				if err := os.MkdirAll(filepath.Join(image, filepath.Dir(name)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(image, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := devpatch.Apply(image, patch.Archive, patch.Deleted, api.DevPatchMaxBytes); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(filepath.Join(image, "src", "app.js")); string(got) != "v2" {
				t.Fatalf("src/app.js after patch = %q, want v2", got)
			}
			if _, err := os.Stat(filepath.Join(image, "proj")); !os.IsNotExist(err) {
				t.Fatalf("patch wrote below the wrapper directory: %v", err)
			}
		})
	}
}
