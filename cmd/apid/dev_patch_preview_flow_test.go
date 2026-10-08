package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// writeDevPatchDelta writes a developer delta archive beneath the test spool
// root, where openDevSourceArchive accepts it.
func writeDevPatchDelta(t *testing.T, files map[string]string) string {
	t.Helper()
	f, err := os.CreateTemp(spoolRoot(), "delta-*.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
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

func TestDevPatchPreviewUsesLiveBuildSourceMap(t *testing.T) {
	const base = "1111111111111111111111111111111111111111111111111111111111111111"
	e := setup(t, api.PlanPro)
	t.Setenv(sourceSpoolRootEnv, t.TempDir())
	ctx := context.Background()
	limits := api.MustLimitsFor(api.PlanPro)
	withoutLive, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "dev-patch-none", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "dev-patch-live", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, BuildID: "build-live", Kind: state.DeploymentKindTarball,
		Status: state.DeployLive, ImageDigest: appTaskTestDigest, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.CreateBuildProvenance(ctx, state.BuildProvenance{BuildID: "build-live", DevPatch: &api.DevPatchSourceMap{
		Version: 1, Verbatim: true, ImageDir: "/app", RebuildPaths: []string{"package.json"}}}); err != nil {
		t.Fatal(err)
	}
	source := writeDevPatchDelta(t, map[string]string{"src/app.js": "console.log(1)"})
	manifest := writeDevPatchDelta(t, map[string]string{"package.json": "{}"})

	cases := []struct {
		name  string
		app   state.App
		delta string
		meta  devSourceMetadata
		want  api.DevPatchPreview
	}{
		{name: "first sync uploads a full snapshot", app: app, delta: source, meta: devSourceMetadata{},
			want: api.DevPatchPreview{Reason: api.DevPatchReasonFullSnapshot}},
		{name: "no live deployment", app: withoutLive, delta: source, meta: devSourceMetadata{base: base},
			want: api.DevPatchPreview{Reason: api.DevPatchReasonNoLiveBuild}},
		{name: "source edit against a verbatim build", app: app, delta: source, meta: devSourceMetadata{base: base},
			want: api.DevPatchPreview{Eligible: true, ChangedPaths: 1, PatchBytes: 14}},
		{name: "manifest edit needs a build", app: app, delta: manifest, meta: devSourceMetadata{base: base},
			want: api.DevPatchPreview{Reason: api.DevPatchReasonRebuildInput}},
		{name: "deletions count", app: app, delta: source, meta: devSourceMetadata{base: base, deleted: []string{"src/old.js"}},
			want: api.DevPatchPreview{Eligible: true, ChangedPaths: 2, PatchBytes: 14}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := e.s.devPatchPreview(ctx, tc.app, tc.delta, tc.meta, "", limits)
			if got == nil || *got != tc.want {
				t.Fatalf("devPatchPreview = %+v, want %+v", got, tc.want)
			}
		})
	}
}
